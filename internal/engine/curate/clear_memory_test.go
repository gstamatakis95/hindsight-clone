package curate

import (
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// unconsolidated reports whether id is among the bank's units waiting for consolidation.
func (e *env) unconsolidated(id domain.UnitID) bool {
	e.t.Helper()
	var found bool
	e.inTx(func(tx store.Tx) error {
		us, err := tx.Units().Unconsolidated(e.ctx, testBank, nil, 100)
		found = slices.ContainsFunc(us, func(u domain.Unit) bool { return u.ID == id })
		return err
	})
	return found
}

// setEmbedding writes f1 with emb under a fresh seq: nil is what an edit leaves while its rederive is
// pending (the edit also stamps the unit consolidated), a vector is the rederive's write.
func (e *env) setEmbedding(emb []float32) {
	e.t.Helper()
	e.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		u, _ := e.unit(idF1)
		u.Embedding, u.Seq = emb, seq
		if err := tx.Units().Update(e.ctx, u); err != nil {
			return err
		}
		if emb != nil {
			return nil
		}
		return tx.Units().MarkConsolidated(e.ctx, []domain.UnitID{idF1}, e.clock.Now())
	})
}

// TestClearMemoryLeavesPendingRederiveAlone: an edited fact waits for its rederive with no embedding and
// stays stamped consolidated; clearing its observations must not make it consolidatable before the rederive
// writes the embedding.
func TestClearMemoryLeavesPendingRederiveAlone(t *testing.T) {
	e, a := adminEnv(t)
	e.setEmbedding(nil)
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, idF1, false); err != nil {
		t.Fatal(err)
	}
	if e.unconsolidated(idF1) {
		t.Error("f1 is consolidatable with a NULL embedding after the clear")
	}
	e.setEmbedding(e.emb.Vector("Alice likes green tea"))
	if !e.unconsolidated(idF1) {
		t.Error("f1 not consolidatable after its rederive")
	}
}

// TestClearMemoryResetsOwnConsolidation: clearing a memory's observations also makes the memory itself
// consolidate again, with its embedding intact.
func TestClearMemoryResetsOwnConsolidation(t *testing.T) {
	e, a := adminEnv(t)
	e.inTx(func(tx store.Tx) error {
		return tx.Units().MarkConsolidated(e.ctx, []domain.UnitID{idF1}, e.clock.Now())
	})
	if f1, _ := e.unit(idF1); f1.ConsolidatedAt == nil {
		t.Fatal("seed: f1 not consolidated")
	}
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, idF1, false); err != nil {
		t.Fatal(err)
	}
	f1, _ := e.unit(idF1)
	if f1.ConsolidatedAt != nil || f1.ConsolidationFailedAt != nil || f1.Embedding == nil {
		t.Errorf("f1 after clear: consolidated_at %v, failed_at %v, embedding set %v",
			f1.ConsolidatedAt, f1.ConsolidationFailedAt, f1.Embedding != nil)
	}
}

// TestClearsQueueGraphMaintenance: both clears queue graph_maintenance.
func TestClearsQueueGraphMaintenance(t *testing.T) {
	e, a := adminEnv(t)
	queued := func(step string) {
		t.Helper()
		if n := len(e.claim(domain.OperationKindGraphMaintenance)); n != 1 {
			t.Errorf("%s: graph_maintenance jobs = %d, want 1", step, n)
		}
		for _, op := range e.ops(domain.OperationKindGraphMaintenance) {
			if op.Status == domain.OperationStatusProcessing {
				if err := e.fake.Complete(e.ctx, op.ID, nil, nil); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, idF2, false); err != nil {
		t.Fatal(err)
	}
	queued("clear memory")
	if _, err := a.ClearObservations(e.ctx, testBank, nil, false); err != nil {
		t.Fatal(err)
	}
	queued("clear scopes")
}
