package memory

import (
	"context"
	"maps"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/prompts"
	"memd/internal/store"
	"testing"
	"time"
)

// TestRunRetainJobResult pins the result a retain_document run records: GetOperation shows it, so its keys
// are part of the API. The run's EXTRACT usage is recorded with it.
func TestRunRetainJobResult(t *testing.T) {
	e := newEnv(t, 0)
	e.llm.RespondJSON(domain.ModelSlotExtract, prompts.ExtractConcise, factsBody(t, fact("a"), fact("b")))
	e.ingest(RetainRequest{RunAsync: true, Items: []RetainItem{{DocumentID: "d1", Content: "some text"}}})
	run := jobs.Inline(e.claimRetain())
	if err := e.svc.RunRetainJob(e.ctx, run); err != nil {
		t.Fatal(err)
	}
	result, usage := run.Result()
	want := `{"chunks_skipped":0,"chunks_total":1,"document_id":"d1","superseded":false,"unchanged":false,` +
		`"units_created":2}`
	if string(result) != want || len(usage) != 1 || usage[0].Slot != domain.ModelSlotExtract {
		t.Fatalf("result %s usage %+v, want %s and the EXTRACT usage", result, usage, want)
	}
}

// strandedSource is a store.Maintenance whose StrandedFacts returns fixed units.
type strandedSource struct {
	store.Maintenance
	units []domain.Unit
	after time.Duration
}

// StrandedFacts records the retry delay and returns the fixed units.
func (s *strandedSource) StrandedFacts(_ context.Context, retryAfter time.Duration, _ int) ([]domain.Unit, error) {
	s.after = retryAfter
	return s.units, nil
}

// TestRederiveStranded: the sweep re-enqueues rederive_memory for facts a failed rederive left with a NULL
// embedding, with curation's payload and serial key, after the retry delay.
func TestRederiveStranded(t *testing.T) {
	e := newEnv(t, 0)
	e.inTx(func(store.Tx) error { return nil }) // creates the bank
	src := &strandedSource{Maintenance: e.store, units: []domain.Unit{
		{ID: "u1", Bank: testBank, DocumentID: "d1"}, {ID: "u2", Bank: testBank},
	}}
	e.svc.d.Maintenance = src
	if n, err := e.svc.RederiveStranded(e.ctx); err != nil || n != 2 {
		t.Fatalf("RederiveStranded = %d, %v; want 2", n, err)
	}
	claimed, err := e.store.Claim(e.ctx, "w", []domain.OperationKind{domain.OperationKindRederiveMemory}, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range claimed {
		got[j.SerialKey] = string(j.Payload)
	}
	want := map[string]string{"doc:d1": `{"unit_id": "u1"}`, "unit:u2": `{"unit_id": "u2"}`}
	if !maps.Equal(got, want) {
		t.Fatalf("rederives %v, want %v", got, want)
	}
	if src.after != strandedRetryAfter {
		t.Fatalf("retry-after %s", src.after)
	}
}
