package consolidate

import (
	"context"
	"fmt"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

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
			return wrapErr(err)
		}
		return tx.Units().Insert(t.Context(), []domain.Unit{{
			ID: uid("f3"), Bank: bank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "Ola dives",
			Tags: []string{"z"}, Embedding: e.emb.Vector("Ola dives"), Seq: seq,
		}})
	})
	e.observation("obs-x", "Max surfs", []string{"x"}, "f1")
	e.observation("obs-y", "Ned skis", []string{"y"}, "f2")
	e.observation("obs-z", "Ola dives", []string{"z"}, "f3")
}

// TestInvalidatorClearScopes: ClearScopes removes only the named scopes and returns the deleted
// observations; an explicit empty ClearScopes (the RPC's "every scope") clears the rest.
func TestInvalidatorClearScopes(t *testing.T) {
	e := newEnv(t, domain.BankConfig{})
	seedDocScopes(t, e)
	inv := NewInvalidator()
	var deleted []port.UnitID
	e.tx(func(tx port.Tx) error {
		var err error
		deleted, err = inv.ClearScopes(t.Context(), tx, bank, []port.ScopeKey{scopeOf("x")})
		return wrapErr(err)
	})
	if ids := obsIDs(e.observations()); !slices.Equal(ids, []port.UnitID{"obs-y", "obs-z"}) ||
		!slices.Equal(names(deleted), []string{"obs-x"}) {
		t.Fatalf("after ClearScopes(x): left %v, deleted %v", ids, deleted)
	}
	if e.unit("f1").ConsolidatedAt != nil {
		t.Fatal("f1 not reset")
	}
	e.wantPending(domain.OperationKindConsolidate, 0) // ClearScopes leaves the enqueue to its caller
	e.tx(func(tx port.Tx) error {
		var err error
		deleted, err = inv.ClearScopes(t.Context(), tx, bank, nil)
		return wrapErr(err)
	})
	if left := e.observations(); len(left) != 0 || !slices.Equal(sortedNames(deleted), []string{"obs-y", "obs-z"}) {
		t.Fatalf("after ClearScopes(all): left %+v, deleted %v", left, deleted)
	}
}

// callLog records, in order, the Seq and observation-delete calls a transaction sees.
type callLog []string

// loggingTx wraps the store's transaction and logs Seq and the evidence calls the Invalidator makes.
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

// loggingEvidence logs the evidence calls.
type loggingEvidence struct {
	port.EvidenceRepo
	log *callLog
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

// TestInvalidatorSeqBeforeDelete: ClearScopes takes tx.Seq before its first delete, and maps an empty list to
// DeleteAllObservations and a non-empty one to DeleteScopes.
func TestInvalidatorSeqBeforeDelete(t *testing.T) {
	inv := NewInvalidator()
	for _, c := range []struct {
		name string
		call func(ctx context.Context, tx port.Tx) error
		want callLog
	}{
		{"ClearScopes(x)", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearScopes(ctx, tx, bank, []port.ScopeKey{scopeOf("x")})
			return wrapErr(err)
		}, callLog{"Seq", "DeleteScopes"}},
		{"ClearScopes(all)", func(ctx context.Context, tx port.Tx) error {
			_, err := inv.ClearScopes(ctx, tx, bank, nil)
			return wrapErr(err)
		}, callLog{"Seq", "DeleteAllObservations"}},
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

// wrapErr wraps a non-nil error from the Invalidator or the store for the test's transaction.
func wrapErr(err error) error {
	if err != nil {
		return fmt.Errorf("in test tx: %w", err)
	}
	return nil
}

// obsIDs returns sorted observation ids.
func obsIDs(us []domain.Unit) []port.UnitID {
	var ids []port.UnitID
	for _, u := range us {
		ids = append(ids, port.UnitID(pgtest.Name(u.ID)))
	}
	slices.Sort(ids)
	return ids
}
