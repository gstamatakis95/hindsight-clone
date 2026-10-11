package postgres_test

import (
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
)

// orphanEntityAndUnit creates bank b with an orphan entity "John" (its last unit was replaced; it sits in
// entity_maintenance) and a fresh unit u that mentions John.
func orphanEntityAndUnit(t *testing.T, s *postgres.DB, b domain.BankRef) (domain.EntityID, domain.Unit) {
	t.Helper()
	var e domain.EntityID
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), b); err != nil {
			return err
		}
		var err error
		e, err = tx.Entities().Upsert(t.Context(), domain.Entity{Bank: b, CanonicalName: "John", Kind: "person"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	u := fuUnit(b, "John likes tea")
	fuInsert(t, s, u)
	return e, u
}

// resolveJohn is a retain batch's entity step: the first candidate named John, else a new John entity.
func resolveJohn(t *testing.T, tx store.Tx, b domain.BankRef) (domain.EntityID, error) {
	t.Helper()
	cands, err := tx.Entities().Candidates(t.Context(), b, "John", 5)
	if err != nil {
		return "", err
	}
	if len(cands) > 0 {
		return cands[0].Entity.ID, nil
	}
	return tx.Entities().Upsert(t.Context(), domain.Entity{Bank: b, CanonicalName: "John", Kind: "person"})
}

// pruneOne prunes entity e of b in its own transaction and checks that it was deleted.
func pruneOne(t *testing.T, s *postgres.DB, b domain.BankRef, e domain.EntityID) {
	t.Helper()
	var n int
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		n, err = tx.Entities().PruneOrphans(t.Context(), b, []domain.EntityID{e})
		return err
	}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d entities, want 1", n)
	}
}

// TestAttachAfterConcurrentPruneReresolves: a retain batch resolves "John" to entity E (Candidates), graph
// maintenance prunes E (an orphan at that moment) in a concurrent transaction, and the batch then attaches
// E. Attach must not skip E silently: it fails with ErrConflict, InTx re-runs the transaction, and the
// re-run resolves "John" afresh, creating a new entity that the unit carries (decision 2501).
func TestAttachAfterConcurrentPruneReresolves(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	b := domain.BankRef{Tenant: "t-race", Bank: "b-ent"}
	e, u := orphanEntityAndUnit(t, s, b)

	resolved, pruned, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var attachErr error
	var attempts int
	var attached domain.EntityID
	go func() {
		defer close(done)
		attachErr = s.InTx(t.Context(), func(tx store.Tx) error {
			attempts++
			if _, err := tx.Seq(t.Context(), b); err != nil {
				return err
			}
			var err error
			if attached, err = resolveJohn(t, tx, b); err != nil {
				return err
			}
			if attempts == 1 {
				close(resolved)
				<-pruned // graph maintenance commits its prune between resolve and attach
			}
			return tx.Entities().Attach(t.Context(), u.ID, []domain.EntityID{attached})
		})
	}()
	<-resolved
	pruneOne(t, s, b, e)
	close(pruned)
	<-done
	if attachErr != nil {
		t.Fatalf("attach transaction: %v", attachErr)
	}
	if attempts != 2 {
		t.Errorf("attach transaction ran %d times, want 2 (the pruned entity's conflict re-runs it once)", attempts)
	}
	if attached == e {
		t.Errorf("the re-run attached the pruned entity %s, want a freshly resolved one", e)
	}
	got, err := s.Read().GetUnits(t.Context(), b, []domain.UnitID{u.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("get unit: %v %d", err, len(got))
	}
	if len(got[0].Entities) != 1 || got[0].Entities[0] != "John" {
		t.Fatalf("unit entities = %v, want [John]", got[0].Entities)
	}
}
