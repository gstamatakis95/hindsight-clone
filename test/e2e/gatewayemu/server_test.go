package gatewayemu_test

import (
	"context"
	"errors"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"memd/internal/prompts"
	"memd/test/e2e/gatewayemu"
	"reflect"
	"testing"
	"time"
)

// cosine is the cosine similarity of two unit vectors.
func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// TestEmbeddingsSimilarity checks 768-dimensional unit vectors, determinism, and that shared tokens make texts
// similar while the nomic prefixes don't.
func TestEmbeddingsSimilarity(t *testing.T) {
	_, client := newEmu(t)
	docs := []string{"Alice lives in Paris.", "Bob plays chess on Sundays.", ""}
	vecs, err := client.EmbedDocuments(t.Context(), docs)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.EmbedDocuments(t.Context(), docs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vecs, again) {
		t.Fatal("embeddings are not deterministic")
	}
	for i, v := range vecs {
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if len(v) != domain.EmbeddingDim || math.Abs(norm-1) > 1e-4 {
			t.Fatalf("vector %d: %d dims, norm %v", i, len(v), norm)
		}
	}
	q, err := client.EmbedQuery(t.Context(), "Where does Alice live in Paris?")
	if err != nil {
		t.Fatal(err)
	}
	related, unrelated := cosine(q, vecs[0]), cosine(q, vecs[1])
	if related < 0.5 || unrelated > 0.1 {
		t.Fatalf("similarity related=%v unrelated=%v", related, unrelated)
	}
	h1 := gatewayemu.Embed(gatewayemu.EmbedTextHash, "same text")
	h2 := gatewayemu.Embed(gatewayemu.EmbedTextHash, "same text!")
	if cosine(h1, h1) < 0.999 || cosine(h1, h2) > 0.2 {
		t.Fatalf("text-hash mode: self %v, other %v", cosine(h1, h1), cosine(h1, h2))
	}
}

// TestRerankOverlap checks rerank scores are the share of query tokens a document contains.
func TestRerankOverlap(t *testing.T) {
	_, client := newEmu(t)
	scores, err := client.Rerank(t.Context(), domain.ModelBinding{Model: gatewayemu.ModelFor("rerank")},
		"Alice Paris", []string{"Alice lives in Paris.", "Alice plays chess.", "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []float32{1, 0.5, 0}; !reflect.DeepEqual(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}
}

// TestFaults checks scripted failures, invalid JSON, delays (cancelled with the request) and the call log.
func TestFaults(t *testing.T) {
	emu, client := newEmu(t)
	schema, err := prompts.Schema(prompts.DedupSystem)
	if err != nil {
		t.Fatal(err)
	}
	req := port.StructuredRequest{
		Slot: domain.ModelSlotDedupCheck, Binding: binding(domain.ModelSlotDedupCheck), Name: prompts.DedupSystem,
		User: "[NEW] a\n[EXISTING] b", Schema: schema,
	}

	// Two 500s, then success within the client's two retries.
	emu.Inject(gatewayemu.Fault{Op: prompts.DedupSystem, Times: 2, Status: 500})
	structured(t, client, req)
	if n := emu.Count(prompts.DedupSystem); n != 3 {
		t.Fatalf("dedup calls = %d, want 3", n)
	}

	// Invalid JSON once: the client retries with the validation error and succeeds.
	emu.Inject(gatewayemu.Fault{Op: prompts.DedupSystem, Times: 1, InvalidJSON: true})
	structured(t, client, req)

	// A permanent 400 on embeddings fails at once; other ops are unaffected.
	emu.Inject(gatewayemu.Fault{Op: gatewayemu.OpEmbeddings, Status: 400, Body: "context_length_exceeded"})
	if _, err := client.EmbedQuery(t.Context(), "x"); !errors.Is(err, domain.ErrContextOverflow) {
		t.Fatalf("embed with a 400 overflow fault: %v", err)
	}
	structured(t, client, req)
	emu.ClearFaults()
	if _, err := client.EmbedQuery(t.Context(), "x"); err != nil {
		t.Fatalf("embed after ClearFaults: %v", err)
	}

	faulted := 0
	for _, c := range emu.Calls() {
		if c.Fault {
			faulted++
		}
	}
	if faulted != 4 {
		t.Fatalf("%d calls marked as faulted, want 4", faulted)
	}

	// A delay ends with the caller's context.
	emu.Inject(gatewayemu.Fault{Op: gatewayemu.OpRerank, Delay: time.Hour})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = client.Rerank(ctx, domain.ModelBinding{Model: "m"}, "q", []string{"d"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("delayed rerank: %v after %v", err, time.Since(start))
	}
}
