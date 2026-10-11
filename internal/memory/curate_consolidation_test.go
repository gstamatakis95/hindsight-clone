package memory

import (
	"context"
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
	"time"
)

// unconsolidated reports whether id is among the bank's units waiting for consolidation.
func (e *curateEnv) unconsolidated(id domain.UnitID) bool {
	e.t.Helper()
	var found bool
	e.inTx(func(tx store.Tx) error {
		us, err := tx.Units().Unconsolidated(e.ctx, testBank, nil, 100)
		found = slices.ContainsFunc(us, func(u domain.Unit) bool { return u.ID == id })
		return err
	})
	return found
}

// rederiveNext claims the one due rederive job, runs it and completes it.
func (e *curateEnv) rederiveNext() {
	e.t.Helper()
	jobs := e.claim(domain.OperationKindRederiveMemory)
	if len(jobs) != 1 {
		e.t.Fatalf("rederive jobs = %d, want 1", len(jobs))
	}
	if err := e.rederiver(nil).Rederive(e.ctx, jobs[0]); err != nil {
		e.t.Fatal(err)
	}
	if err := e.fake.Complete(e.ctx, jobs[0].ID, nil, nil); err != nil {
		e.t.Fatal(err)
	}
}

// failingEmbedder fails every call, standing in for a transient embedder outage.
type failingEmbedder struct{ ai.Embedder }

// EmbedDocuments fails with domain.ErrUnavailable.
func (failingEmbedder) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, domain.ErrUnavailable
}

// entitiesOf returns f1's attached entity names, sorted.
func (e *curateEnv) entitiesOf(id domain.UnitID) []string {
	e.t.Helper()
	u, _ := e.unit(id)
	return slices.Sorted(slices.Values(u.Entities))
}

// TestRederiveBackoffRetryKeepsNewerEntities: E1 sets [Alice, Carol] and its rederive R1 fails
// transiently and backs off; E2 sets [Bob] and its rederive R2 runs first; R1's retry must not bring back
// E1's names. Entity edits commit in the RPC's own transaction, and rederive never touches entities.
func TestRederiveBackoffRetryKeepsNewerEntities(t *testing.T) {
	e := newCurateEnv(t)
	c := e.curator()
	edit := func(names ...string) {
		t.Helper()
		if _, _, err := c.Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Entities: names}); err != nil {
			t.Fatal(err)
		}
		if got := e.entitiesOf(idF1); !slices.Equal(got, names) {
			t.Fatalf("entities after the RPC = %v, want %v", got, names)
		}
	}
	edit("Alice", "Carol")
	r1 := e.claim(domain.OperationKindRederiveMemory)
	if len(r1) != 1 {
		t.Fatalf("R1 jobs = %d", len(r1))
	}
	err := e.rederiver(failingEmbedder{e.emb}).Rederive(e.ctx, r1[0])
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("R1 err = %v", err)
	}
	retryAt := e.clock.Now().Add(time.Minute)
	if err := e.fake.Fail(e.ctx, r1[0].ID, err, &retryAt); err != nil {
		t.Fatal(err)
	}
	edit("Bob")
	e.rederiveNext() // R2 runs while R1 is parked
	e.clock.Advance(2 * time.Minute)
	e.rederiveNext() // R1's retry
	if got := e.entitiesOf(idF1); !slices.Equal(got, []string{"Bob"}) {
		t.Errorf("entities after R1's retry = %v, want [Bob]", got)
	}
	if u, _ := e.unit(idF1); u.Embedding == nil {
		t.Error("f1 not embedded")
	}
}

// TestRemovalPathsQueueGraphMaintenance: an edit and its rederive both queue graph_maintenance.
func TestRemovalPathsQueueGraphMaintenance(t *testing.T) {
	e := newCurateEnv(t)
	queued := func(step string) {
		t.Helper()
		if n := len(e.claim(domain.OperationKindGraphMaintenance)); n != 1 {
			t.Errorf("%s: graph_maintenance jobs = %d, want 1", step, n)
		}
		e.drainClaimed()
	}
	text := "Alice likes oolong tea"
	if _, _, err := e.curator().Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Text: &text}); err != nil {
		t.Fatal(err)
	}
	queued("edit")
	e.rederiveNext()
	queued("rederive")
}

// drainClaimed completes every processing graph_maintenance job (claim already moved them to processing).
func (e *curateEnv) drainClaimed() {
	e.t.Helper()
	for _, op := range e.ops(domain.OperationKindGraphMaintenance) {
		if op.Status == domain.OperationStatusProcessing {
			if err := e.fake.Complete(e.ctx, op.ID, nil, nil); err != nil {
				e.t.Fatal(err)
			}
		}
	}
}

// TestEditKeepsUnitOutOfConsolidationUntilRederive: an edited or restored unit is never
// handed to consolidation with an empty embedding; the rederive makes it consolidate again.
func TestEditKeepsUnitOutOfConsolidationUntilRederive(t *testing.T) {
	e := newCurateEnv(t)
	text := "Alice likes oolong tea"
	if _, _, err := e.curator().Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Text: &text}); err != nil {
		t.Fatal(err)
	}
	if e.unconsolidated(idF1) {
		t.Error("edited f1 offered to consolidation before rederive")
	}
	e.rederiveNext()
	if f1, _ := e.unit(idF1); !e.unconsolidated(idF1) || f1.Embedding == nil {
		t.Errorf("after rederive: unconsolidated %v, embedding set %v", e.unconsolidated(idF1), f1.Embedding != nil)
	}

	e.invalidate(e.service(nil))
	req := UpdateRequest{Bank: testBank, ID: idF1, State: domain.MemoryStateValid}
	if _, _, err := e.curator().Update(e.ctx, req); err != nil {
		t.Fatal(err)
	}
	if e.unconsolidated(idF1) {
		t.Error("restored f1 offered to consolidation before rederive")
	}
	e.rederiveNext()
	if !e.unconsolidated(idF1) {
		t.Error("restored f1 not offered to consolidation after rederive")
	}
}

// TestEditDateClearsTimeless: setting an occurrence date clears timeless.
func TestEditDateClearsTimeless(t *testing.T) {
	e := newCurateEnv(t)
	e.inTx(func(tx store.Tx) error {
		u, _ := e.unit(idF1)
		u.Timeless = true
		return tx.Units().Update(e.ctx, u)
	})
	at := e.clock.Now().Add(-time.Hour)
	u, _, err := e.curator().Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, OccurredStart: &at})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.unit(idF1); u.Timeless || got.Timeless {
		t.Errorf("timeless after dating: returned %v, stored %v", u.Timeless, got.Timeless)
	}
}

// cancellingEmbedder embeds, then cancels the job's context, like a cancel arriving mid-job.
type cancellingEmbedder struct {
	ai.Embedder
	cancel context.CancelFunc
}

// EmbedDocuments embeds and cancels.
func (c cancellingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	defer c.cancel()
	return c.Embedder.EmbedDocuments(ctx, texts)
}

// TestRederiveCancelledMidTx: a transaction failure on a cancelled context is ErrCancelled.
func TestRederiveCancelledMidTx(t *testing.T) {
	e := newCurateEnv(t)
	text := "Alice likes oolong tea"
	if _, _, err := e.curator().Update(e.ctx, UpdateRequest{Bank: testBank, ID: idF1, Text: &text}); err != nil {
		t.Fatal(err)
	}
	jobs := e.claim(domain.OperationKindRederiveMemory)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	err := e.rederiver(cancellingEmbedder{Embedder: e.emb, cancel: cancel}).Rederive(ctx, jobs[0])
	if !errors.Is(err, domain.ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}
