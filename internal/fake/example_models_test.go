package fake_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/port"
	"time"
)

// ExampleLLM scripts structured replies per (slot, prompt name), a fallback, and a timeout.
func ExampleLLM() {
	ctx := context.Background()
	l := fake.NewLLM()
	l.Script(domain.ModelSlotExtract, "extract_facts",
		fake.Reply{JSON: `{"facts":["Alice likes tea"]}`},
		fake.Reply{JSON: `{"facts":[]}`}) // the last reply repeats
	l.Timeout(domain.ModelSlotConsolidate, "") // every consolidate prompt times out

	var out struct {
		Facts []string `json:"facts"`
	}
	// Scripted output is validated against req.Schema, as the gateway does.
	schema := json.RawMessage(`{"type":"object","required":["facts"],` +
		`"properties":{"facts":{"type":"array","items":{"type":"string"}}}}`)
	req := port.StructuredRequest{Slot: domain.ModelSlotExtract, Name: "extract_facts", Schema: schema}
	for range 3 {
		_, err := l.Structured(ctx, req, &out)
		fmt.Println(out.Facts, err)
	}
	decide := port.StructuredRequest{Slot: domain.ModelSlotConsolidate, Name: "decide", Schema: schema}
	_, err := l.Structured(ctx, decide, &out)
	fmt.Println(errors.Is(err, context.DeadlineExceeded), errors.Is(err, domain.ErrUnavailable))
	fmt.Println(len(l.CallsFor(domain.ModelSlotExtract, "extract_facts")))
	// Output:
	// [Alice likes tea] <nil>
	// [] <nil>
	// [] <nil>
	// true true
	// 3
}

// ExampleEmbedder shows that EmbedSimilar maps texts sharing words to nearby vectors.
func ExampleEmbedder() {
	e := fake.NewEmbedder(fake.EmbedSimilar)
	cos := func(a, b string) float64 {
		va, vb := e.Vector(a), e.Vector(b)
		var dot float64
		for i := range va {
			dot += float64(va[i]) * float64(vb[i])
		}
		return math.Round(dot*100) / 100
	}
	fmt.Println(len(e.Vector("x")))
	fmt.Println(cos("alice likes green tea", "alice likes tea"))
	fmt.Println(cos("alice likes green tea", "quarterly revenue grew"))
	// Output:
	// 768
	// 0.87
	// 0
}

// ExampleReranker scripts scores; a score outside [0,1] makes every score go through a sigmoid.
func ExampleReranker() {
	r := fake.NewReranker()
	r.SetScore("what does alice drink?", "alice likes tea", 4)
	r.SetDocScore("bob likes coffee", -2)
	scores, _ := r.Rerank(context.Background(), port.ModelBinding{Model: "rerank"},
		"what does alice drink?", []string{"alice likes tea", "bob likes coffee"})
	fmt.Printf("%.3f %.3f\n", scores[0], scores[1])

	r = fake.NewReranker() // default: share of the query's words found in the doc
	scores, _ = r.Rerank(context.Background(), port.ModelBinding{}, "green tea", []string{"green tea", "tea", "x"})
	fmt.Println(scores)
	// Output:
	// 0.982 0.119
	// [1 0.5 0]
}

// ExampleClock moves a fake clock by hand.
func ExampleClock() {
	c := fake.NewClock(time.Time{})
	fmt.Println(c.Now().Format(time.RFC3339))
	fmt.Println(c.Advance(36 * time.Hour).Format(time.RFC3339))
	c.Set(time.Date(2030, time.May, 4, 9, 30, 0, 0, time.UTC))
	fmt.Println(c.Now().Format(time.RFC3339))
	// Output:
	// 2025-01-01T00:00:00Z
	// 2025-01-02T12:00:00Z
	// 2030-05-04T09:30:00Z
}
