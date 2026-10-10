package fake_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"testing"
)

// rerankSigmoid is the logistic function the reranker applies to out-of-range scores.
func rerankSigmoid(x float64) float32 { return float32(1 / (1 + math.Exp(-x))) }

func TestRerankerPrecedence(t *testing.T) {
	r := fake.NewReranker()
	r.SetFunc(func(_, doc string) float32 {
		if doc == "by func" || doc == "doc and func" {
			return 0.3
		}
		return 0.05
	})
	r.SetDocScore("pair and doc", 0.2)
	r.SetDocScore("doc and func", 0.4)
	r.SetScore("q", "pair and doc", 0.9)
	r.SetScore("other query", "by func", 0.8)
	docs := []string{"pair and doc", "doc and func", "by func", "unscripted"}
	got, err := r.Rerank(t.Context(), port.ModelBinding{}, "q", docs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float32{0.9, 0.4, 0.3, 0.05}; !slices.Equal(got, want) {
		t.Errorf("Rerank = %v, want %v", got, want)
	}
	r.SetFunc(nil)
	docs = []string{"by func", "I like GREEN things", "tea green"}
	got, err = r.Rerank(t.Context(), port.ModelBinding{}, "Green tea, green!", docs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []float32{0, 0.5, 1}; !slices.Equal(got, want) {
		t.Errorf("default token-overlap Rerank = %v, want %v", got, want)
	}
	got, _ = r.Rerank(t.Context(), port.ModelBinding{}, "...", []string{"anything"})
	if want := []float32{0}; !slices.Equal(got, want) {
		t.Errorf("Rerank with a token-less query = %v, want %v", got, want)
	}
}

func TestRerankerSigmoid(t *testing.T) {
	cases := []struct {
		name   string
		scores []float32
		want   []float32
	}{
		{"logits", []float32{3, -1}, []float32{rerankSigmoid(3), rerankSigmoid(-1)}},
		{"one above 1", []float32{1.5, 0.5}, []float32{rerankSigmoid(1.5), rerankSigmoid(0.5)}},
		{"in range", []float32{0, 0.25, 1}, []float32{0, 0.25, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := fake.NewReranker()
			docs := make([]string, len(c.scores))
			for i, s := range c.scores {
				docs[i] = fmt.Sprintf("doc-%d", i)
				r.SetDocScore(docs[i], s)
			}
			got, err := r.Rerank(t.Context(), port.ModelBinding{}, "q", docs)
			if err != nil {
				t.Fatal(err)
			}
			for i := range c.want {
				if math.Abs(float64(got[i]-c.want[i])) > 1e-6 {
					t.Errorf("score %d = %v, want %v", i, got[i], c.want[i])
				}
			}
			for i := 1; i < len(got); i++ {
				if (got[i] < got[i-1]) != (c.scores[i] < c.scores[i-1]) {
					t.Errorf("order changed: raw %v, got %v", c.scores, got)
				}
			}
		})
	}
}

func TestRerankerFailWithAndCalls(t *testing.T) {
	r := fake.NewReranker()
	boom := errors.New("boom")
	r.FailWith(boom)
	b := port.ModelBinding{Model: "rerank-1"}
	if _, err := r.Rerank(t.Context(), b, "q1", []string{"d1"}); !errors.Is(err, boom) {
		t.Errorf("Rerank error = %v, want boom", err)
	}
	r.FailWith(nil)
	if _, err := r.Rerank(t.Context(), b, "q2", []string{"d2", "d3"}); err != nil {
		t.Errorf("Rerank after FailWith(nil): %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Rerank(ctx, b, "q3", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Rerank error = %v, want context.Canceled", err)
	}
	calls := r.Calls()
	if len(calls) != 3 {
		t.Fatalf("Calls() = %+v, want 3 calls", calls)
	}
	if c := calls[1]; c.Binding.Model != "rerank-1" || c.Query != "q2" || !slices.Equal(c.Docs, []string{"d2", "d3"}) {
		t.Errorf("call 1 = %+v", calls[1])
	}
	calls[1].Docs[0] = "mutated"
	if r.Calls()[1].Docs[0] != "d2" {
		t.Error("Calls() returned a shared slice")
	}
}
