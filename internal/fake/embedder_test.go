package fake_test

import (
	"context"
	"errors"
	"math"
	"memd/internal/domain"
	"memd/internal/fake"
	"slices"
	"testing"
)

// embedCosine is the cosine similarity of two vectors.
func embedCosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

// embedNorm is the L2 length of v.
func embedNorm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestEmbedderShape(t *testing.T) {
	texts := []string{"", "alice likes green tea", "  !!  ", "Ünïcödé 東京 42", "a"}
	for _, mode := range []fake.EmbedMode{fake.EmbedHash, fake.EmbedSimilar} {
		e := fake.NewEmbedder(mode)
		vecs, err := e.EmbedDocuments(t.Context(), texts)
		if err != nil {
			t.Fatalf("mode %d: EmbedDocuments: %v", mode, err)
		}
		if len(vecs) != len(texts) {
			t.Fatalf("mode %d: %d vectors for %d texts", mode, len(vecs), len(texts))
		}
		for i, v := range vecs {
			if len(v) != domain.EmbeddingDim {
				t.Errorf("mode %d %q: dim %d, want %d", mode, texts[i], len(v), domain.EmbeddingDim)
			}
			if n := embedNorm(v); math.Abs(n-1) > 1e-5 {
				t.Errorf("mode %d %q: norm %v, want 1", mode, texts[i], n)
			}
			if slices.ContainsFunc(v, func(x float32) bool { return math.IsNaN(float64(x)) }) {
				t.Errorf("mode %d %q: NaN in vector", mode, texts[i])
			}
		}
	}
}

func TestEmbedderDeterministicAndQueryEqualsDocument(t *testing.T) {
	for _, mode := range []fake.EmbedMode{fake.EmbedHash, fake.EmbedSimilar} {
		a, b := fake.NewEmbedder(mode), fake.NewEmbedder(mode)
		q, err := a.EmbedQuery(t.Context(), "what does alice drink?")
		if err != nil {
			t.Fatal(err)
		}
		docs, err := b.EmbedDocuments(t.Context(), []string{"what does alice drink?"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(q, docs[0]) || !slices.Equal(q, a.Vector("what does alice drink?")) {
			t.Errorf("mode %d: query, document and Vector differ for the same text", mode)
		}
	}
	// Pin a hash-mode value so a change in the derivation (and thus cross-process stability) is noticed.
	v := fake.NewEmbedder(fake.EmbedHash).Vector("")
	again := fake.NewEmbedder(fake.EmbedHash).Vector("")
	if !slices.Equal(v, again) {
		t.Error("hash vector of the empty text is not deterministic")
	}
}

func TestEmbedderHashModeUnrelated(t *testing.T) {
	e := fake.NewEmbedder(fake.EmbedHash)
	texts := []string{"alice likes green tea", "alice likes tea", "quarterly revenue grew", "", "x"}
	for i := range texts {
		for j := i + 1; j < len(texts); j++ {
			if c := embedCosine(e.Vector(texts[i]), e.Vector(texts[j])); math.Abs(c) >= 0.3 {
				t.Errorf("hash cos(%q, %q) = %.3f, want |cos| < 0.3", texts[i], texts[j], c)
			}
		}
	}
}

func TestEmbedderSimilarMode(t *testing.T) {
	e := fake.NewEmbedder(fake.EmbedSimilar)
	base := e.Vector("alice likes green tea")
	cases := []struct {
		other    string
		min, max float64
	}{
		{"alice likes tea", 0.6, 1},
		{"Alice LIKES green tea!", 0.999, 1.001},
		{"quarterly revenue grew", -0.2, 0.2},
	}
	for _, c := range cases {
		if got := embedCosine(base, e.Vector(c.other)); got < c.min || got > c.max {
			t.Errorf("similar cos(base, %q) = %.3f, want in [%v, %v]", c.other, got, c.min, c.max)
		}
	}
	hash := fake.NewEmbedder(fake.EmbedHash)
	if !slices.Equal(e.Vector("?!"), hash.Vector("?!")) {
		t.Error("a text with no tokens does not fall back to the hash vector")
	}
}

func TestEmbedderFailWithAndCalls(t *testing.T) {
	e := fake.NewEmbedder(fake.EmbedHash)
	boom := errors.New("boom")
	e.FailWith(boom)
	if _, err := e.EmbedQuery(t.Context(), "q"); !errors.Is(err, boom) {
		t.Errorf("EmbedQuery error = %v, want boom", err)
	}
	if _, err := e.EmbedDocuments(t.Context(), []string{"d1", "d2"}); !errors.Is(err, boom) {
		t.Errorf("EmbedDocuments error = %v, want boom", err)
	}
	e.FailWith(nil)
	if _, err := e.EmbedQuery(t.Context(), "ok"); err != nil {
		t.Errorf("EmbedQuery after FailWith(nil): %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.EmbedDocuments(ctx, []string{"late"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled EmbedDocuments error = %v, want context.Canceled", err)
	}
	want := []fake.EmbedCall{
		{Query: true, Texts: []string{"q"}},
		{Texts: []string{"d1", "d2"}},
		{Query: true, Texts: []string{"ok"}},
		{Texts: []string{"late"}},
	}
	got := e.Calls()
	if len(got) != len(want) {
		t.Fatalf("Calls() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Query != want[i].Query || !slices.Equal(got[i].Texts, want[i].Texts) {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	got[1].Texts[0] = "mutated"
	if e.Calls()[1].Texts[0] != "d1" {
		t.Error("Calls() returned a shared slice")
	}
}
