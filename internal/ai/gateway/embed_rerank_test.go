package gateway_test

import (
	"context"
	"errors"
	"math"
	"memd/internal/ai/gateway"
	"memd/internal/domain"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestEmbedDocumentsPrefixAndBatches(t *testing.T) {
	g := newFakeGateway(t)
	g.embed = embedEcho(0)
	texts := []string{"a", "b", "c", "d", "e", "f", "g"}
	vecs, err := g.client(gateway.Config{EmbedBatchSize: 3}).EmbedDocuments(t.Context(), texts)
	if err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != domain.EmbeddingDim || v[0] != float32(i%3) {
			t.Errorf("vector %d: len %d first %v, want len 768 first %d (ordered by index)", i, len(v), v[0], i%3)
		}
	}
	reqs := g.bodies("embed")
	var sizes []int
	var inputs []string
	for _, r := range reqs {
		in, _ := r["input"].([]any)
		sizes = append(sizes, len(in))
		for _, x := range in {
			inputs = append(inputs, str(x))
		}
		if r["model"] != gateway.DefaultEmbedModel {
			t.Errorf("model = %v, want %s", r["model"], gateway.DefaultEmbedModel)
		}
	}
	if !slices.Equal(sizes, []int{3, 3, 1}) {
		t.Errorf("batch sizes = %v, want [3 3 1]", sizes)
	}
	for i, in := range inputs {
		if in != "search_document: "+texts[i] {
			t.Errorf("input %d = %q, want search_document: prefix", i, in)
		}
	}
	if vecs, err := g.client(gateway.Config{}).EmbedDocuments(t.Context(), nil); err != nil || len(vecs) != 0 {
		t.Errorf("EmbedDocuments(nil) = %v, %v; want empty without a call", vecs, err)
	}
	if n := len(g.bodies("embed")); n != 3 {
		t.Errorf("got %d requests, want 3", n)
	}
}

func TestEmbedQueryPrefix(t *testing.T) {
	g := newFakeGateway(t)
	g.embed = embedEcho(4)
	v, err := g.client(gateway.Config{}).EmbedQuery(t.Context(), "where is it?")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(v) != domain.EmbeddingDim || v[0] != 4 {
		t.Errorf("vector len %d first %v", len(v), v[0])
	}
	in, _ := g.bodies("embed")[0]["input"].([]any)
	if len(in) != 1 || in[0] != "search_query: where is it?" {
		t.Errorf("input = %v, want [search_query: where is it?]", in)
	}
}

func TestEmbedRejectsWrongDimension(t *testing.T) {
	g := newFakeGateway(t)
	g.embed = func(int, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{"data": []any{
			map[string]any{"index": 0, "embedding": vec(1, domain.EmbeddingDim-1)},
		}}
	}
	_, err := g.client(gateway.Config{}).EmbedQuery(t.Context(), "q")
	if !errors.Is(err, domain.ErrUnavailable) || !strings.Contains(err.Error(), "767") {
		t.Errorf("err = %v, want ErrUnavailable naming 767 dimensions", err)
	}
}

func TestRerankNormalization(t *testing.T) {
	sigmoid := func(x float64) float32 { return float32(1 / (1 + math.Exp(-x))) }
	for _, tc := range []struct {
		name string
		raw  []float64
		want []float32
	}{
		{"in range passes through", []float64{0.9, 0.1, 0.5}, []float32{0.9, 0.1, 0.5}},
		{"boundaries pass through", []float64{0, 1, 0.25}, []float32{0, 1, 0.25}},
		{"one logit sigmoids all", []float64{2.0, 0.5, -1.0}, []float32{sigmoid(2), sigmoid(0.5), sigmoid(-1)}},
		{"above one sigmoids all", []float64{1.5, 0.2, 0.9}, []float32{sigmoid(1.5), sigmoid(0.2), sigmoid(0.9)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGateway(t)
			g.rerank = func(int, map[string]any) (int, any) {
				results := []any{} // reverse order, to check results are placed by index
				for i := len(tc.raw) - 1; i >= 0; i-- {
					results = append(results, map[string]any{"index": i, "relevance_score": tc.raw[i]})
				}
				return http.StatusOK, map[string]any{"results": results}
			}
			got, err := g.client(gateway.Config{}).Rerank(t.Context(), binding("rr"), "q", []string{"x", "y", "z"})
			if err != nil {
				t.Fatalf("Rerank: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("scores = %v, want %v", got, tc.want)
			}
			body := g.bodies("rerank")[0]
			if body["model"] != "rr" || body["query"] != "q" || body["top_n"] != 3.0 ||
				mustJSON(t, body["documents"]) != `["x","y","z"]` {
				t.Errorf("rerank body = %s", mustJSON(t, body))
			}
		})
	}
}

func TestRerankUnscoredIsZero(t *testing.T) {
	g := newFakeGateway(t)
	g.rerank = func(int, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{"results": []any{map[string]any{"index": 1, "relevance_score": 3.0}}}
	}
	got, err := g.client(gateway.Config{}).Rerank(t.Context(), binding("rr"), "q", []string{"x", "y"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if got[0] != 0 || got[1] < 0.95 {
		t.Errorf("scores = %v, want [0, sigmoid(3)]", got)
	}
	if got, err := g.client(gateway.Config{}).Rerank(t.Context(), binding("rr"), "q", nil); err != nil || len(got) != 0 {
		t.Errorf("Rerank(no docs) = %v, %v", got, err)
	}
}

func TestUsageHookPerCall(t *testing.T) {
	g := newFakeGateway(t)
	g.chat = func(int, map[string]any) (int, any) { return http.StatusOK, chatReply("hi", 7, 2) }
	g.embed = embedEcho(0)
	g.rerank = func(int, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{
			"model": "rr", "results": []any{map[string]any{"index": 0, "relevance_score": 0.5}},
			"usage": map[string]any{"total_tokens": 9},
		}
	}
	var mu sync.Mutex
	var got []domain.Usage
	c := g.client(gateway.Config{OnUsage: func(_ context.Context, u domain.Usage) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, u)
	}})
	if _, err := c.Chat(t.Context(), chatRequest(domain.ModelSlotReflect, "m")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EmbedQuery(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Rerank(t.Context(), binding("rr"), "q", []string{"d"}); err != nil {
		t.Fatal(err)
	}
	want := []domain.Usage{
		{Slot: domain.ModelSlotReflect, Model: "served-model", InputTokens: 7, OutputTokens: 2},
		{Slot: domain.ModelSlotEmbed, Model: "nomic", InputTokens: 1},
		{Slot: domain.ModelSlotRerank, Model: "rr", InputTokens: 9},
	}
	if !slices.Equal(got, want) {
		t.Errorf("usage records = %+v, want %+v", got, want)
	}
}
