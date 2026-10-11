package consolidate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

// TestInvalidatorClearForMemory: the observations citing the unit are deleted, their other sources reset,
// the automatic consolidate queued and the scopes returned (EvidenceRepo.CitingScopes on the fake).
func TestInvalidatorClearForMemory(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	e.fact("f1", "Kim sails", "x")
	e.fact("f2", "Kim sails often", "x")
	e.fact("f3", "Lee rows", "y")
	e.observation("obs-sail", "Kim sails", []string{"x"}, "f1", "f2")
	e.observation("obs-row", "Lee rows", []string{"y"}, "f3")
	var scopes []port.ScopeKey
	scopes = e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return NewInvalidator().ClearForMemory(t.Context(), tx, bank, "f1")
	})
	if len(scopes) != 1 || scopes[0].String() != scopeOf("x").String() {
		t.Fatalf("scopes = %v", scopes)
	}
	obs := e.observations()
	if len(obs) != 1 || obs[0].ID != "obs-row" {
		t.Fatalf("observations left = %+v", obs)
	}
	if e.unit("f2").ConsolidatedAt != nil || e.unit("f3").ConsolidatedAt == nil {
		t.Fatal("want f2 reset and f3 untouched")
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 1 {
		t.Fatalf("pending consolidate = %d, want 1", n)
	}
	// Nothing to clear: no job, no scopes.
	scopes = e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return NewInvalidator().ClearForMemory(t.Context(), tx, bank, "f3x")
	})
	if len(scopes) != 0 || len(e.jobs(domain.OperationKindConsolidate)) != 1 {
		t.Fatalf("no-op clear: scopes %v", scopes)
	}
}

// seedDocScopes seeds scopes x, y and z, each with one fact and one observation; f3 belongs to document d1.
func seedDocScopes(t *testing.T, e *env) {
	t.Helper()
	e.tx(func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{ID: "d1", Bank: bank})
	})
	e.fact("f1", "Max surfs", "x")
	e.fact("f2", "Ned skis", "y")
	e.texts["f3"] = "Ola dives"
	e.tx(func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), bank)
		if err != nil {
			return err
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: "f3", Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Ola dives",
			Tags: []string{"z"}, Embedding: e.emb.Vector("Ola dives"), Seq: seq,
		}})
	})
	e.observation("obs-x", "Max surfs", []string{"x"}, "f1")
	e.observation("obs-y", "Ned skis", []string{"y"}, "f2")
	e.observation("obs-z", "Ola dives", []string{"z"}, "f3")
}

// TestInvalidatorClearScopesAndDocument: ClearScopes removes only the named scopes and returns the deleted
// observations; ClearDocumentUnits clears what the document's units (UnitRepo.IDsByDocument) support; an
// explicit empty ClearScopes (the RPC's "every scope") clears the rest.
func TestInvalidatorClearScopesAndDocument(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	seedDocScopes(t, e)
	inv := NewInvalidator()
	var deleted []port.UnitID
	e.tx(func(tx port.Tx) error {
		var err error
		deleted, err = inv.ClearScopes(t.Context(), tx, bank, []port.ScopeKey{scopeOf("x")})
		return err
	})
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-y", "obs-z"}) ||
		!slices.Equal(deleted, []port.UnitID{"obs-x"}) {
		t.Fatalf("after ClearScopes(x): left %v, deleted %v", ids, deleted)
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("f1 not reset")
	}
	e.wantPending(domain.OperationKindConsolidate, 0) // ClearScopes leaves the enqueue to its caller
	scopes := e.scopesTx(func(tx port.Tx) ([]port.ScopeKey, error) {
		return inv.ClearDocumentUnits(t.Context(), tx, bank, "d1")
	})
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-y"}) || len(scopes) != 1 ||
		scopes[0].String() != scopeOf("z").String() {
		t.Fatalf("after ClearDocumentUnits(d1): %v, scopes %v", ids, scopes)
	}
	if n := len(e.jobs(domain.OperationKindConsolidate)); n != 1 {
		t.Fatalf("pending consolidate = %d, want 1 (from ClearDocumentUnits)", n)
	}
	e.tx(func(tx port.Tx) error {
		var err error
		deleted, err = inv.ClearScopes(t.Context(), tx, bank, nil)
		return err
	})
	if left := e.observations(); len(left) != 0 || !slices.Equal(deleted, []port.UnitID{"obs-y"}) {
		t.Fatalf("after ClearScopes(all): left %+v, deleted %v", left, deleted)
	}
}

// callLog records, in order, the Seq and observation-delete calls a transaction sees.
type callLog []string

// loggingTx wraps the fake's transaction and logs Seq and the evidence and unit calls the Invalidator makes.
type loggingTx struct {
	port.Tx
	log *callLog
}

// Seq logs "Seq".
func (t loggingTx) Seq(ctx context.Context, b port.BankRef) (int64, error) {
	*t.log = append(*t.log, "Seq")
	return t.Tx.Seq(ctx, b)
}

// Evidence wraps the evidence repository.
func (t loggingTx) Evidence() port.EvidenceRepo { return loggingEvidence{t.Tx.Evidence(), t.log} }

// Units wraps the unit repository.
func (t loggingTx) Units() port.UnitRepo { return loggingUnits{t.Tx.Units(), t.log} }

// loggingEvidence logs the evidence calls.
type loggingEvidence struct {
	port.EvidenceRepo
	log *callLog
}

// CitingScopes logs and delegates.
func (r loggingEvidence) CitingScopes(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]port.ScopeKey, error) {
	*r.log = append(*r.log, "CitingScopes")
	return r.EvidenceRepo.CitingScopes(ctx, b, ids)
}

// DeleteCiting logs and delegates.
func (r loggingEvidence) DeleteCiting(ctx context.Context, b port.BankRef, ids []port.UnitID) (
	[]port.UnitID, []port.UnitID, error,
) {
	*r.log = append(*r.log, "DeleteCiting")
	return r.EvidenceRepo.DeleteCiting(ctx, b, ids)
}

// DeleteScopes logs and delegates.
func (r loggingEvidence) DeleteScopes(ctx context.Context, b port.BankRef, s []port.ScopeKey) (
	[]port.UnitID, []port.UnitID, error,
) {
	*r.log = append(*r.log, "DeleteScopes")
	return r.EvidenceRepo.DeleteScopes(ctx, b, s)
}

// DeleteAllObservations logs and delegates.
func (r loggingEvidence) DeleteAllObservations(ctx context.Context, b port.BankRef) (
	[]port.UnitID, []port.UnitID, error,
) {
	*r.log = append(*r.log, "DeleteAllObservations")
	return r.EvidenceRepo.DeleteAllObservations(ctx, b)
}

// loggingUnits logs IDsByDocument.
type loggingUnits struct {
	port.UnitRepo
	log *callLog
}

// IDsByDocument logs and delegates.
func (r loggingUnits) IDsByDocument(ctx context.Context, b port.BankRef, doc port.DocumentID) ([]port.UnitID, error) {
	*r.log = append(*r.log, "IDsByDocument")
	return r.UnitRepo.IDsByDocument(ctx, b, doc)
}

// TestInvalidatorSeqBeforeDelete: every Invalidator method takes tx.Seq before its first read or delete,
// and ClearScopes maps an empty list to DeleteAllObservations and a non-empty one to DeleteScopes.
func TestInvalidatorSeqBeforeDelete(t *testing.T) {
	inv := NewInvalidator()
	for _, c := range []struct {
		name string
		call func(ctx context.Context, tx port.Tx) error
		want callLog
	}{
		{"ClearScopes(x)", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearScopes(ctx, tx, bank, []port.ScopeKey{scopeOf("x")})
			return err
		}, callLog{"Seq", "DeleteScopes"}},
		{"ClearScopes(all)", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearScopes(ctx, tx, bank, nil)
			return err
		}, callLog{"Seq", "DeleteAllObservations"}},
		{"ClearForMemory", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearForMemory(ctx, tx, bank, "f1")
			return err
		}, callLog{"Seq", "CitingScopes", "DeleteCiting"}},
		{"ClearDocumentUnits", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearDocumentUnits(ctx, tx, bank, "d1")
			return err
		}, callLog{"Seq", "IDsByDocument", "CitingScopes", "DeleteCiting"}},
	} {
		e := newEnv(t, domain.BankConfig{})
		seedDocScopes(t, e)
		var log callLog
		e.tx(func(tx port.Tx) error { return c.call(t.Context(), loggingTx{tx, &log}) })
		if !slices.Equal(log, c.want) {
			t.Errorf("%s: calls %v, want %v", c.name, log, c.want)
		}
	}
}

// TestInvalidatorDataDerivedEmptyNeverClearsAll: the data-derived paths (a unit no observation cites, a
// document with no units, a document whose units nothing cites) never reach DeleteAllObservations and leave
// every observation in place.
func TestInvalidatorDataDerivedEmptyNeverClearsAll(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	seedDocScopes(t, e)
	e.tx(func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{ID: "d-empty", Bank: bank})
	})
	inv := NewInvalidator()
	var log callLog
	e.tx(func(tx port.Tx) error {
		ltx := loggingTx{tx, &log}
		if _, err := inv.ClearForMemory(t.Context(), ltx, bank, "uncited"); err != nil {
			return err
		}
		if _, err := inv.ClearDocumentUnits(t.Context(), ltx, bank, "d-empty"); err != nil {
			return err
		}
		_, err := inv.ClearDocumentUnits(t.Context(), ltx, bank, "d-unknown")
		return err
	})
	if slices.Contains(log, "DeleteAllObservations") || slices.Contains(log, "DeleteScopes") {
		t.Fatalf("data-derived clears called %v", log)
	}
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-x", "obs-y", "obs-z"}) {
		t.Fatalf("observations after data-derived empty clears: %v, want all three", ids)
	}
	e.wantPending(domain.OperationKindConsolidate, 0)
}

// obsIDs returns sorted observation ids.
func obsIDs(us []domain.Unit) []port.UnitID {
	var ids []port.UnitID
	for _, u := range us {
		ids = append(ids, u.ID)
	}
	slices.Sort(ids)
	return ids
}
