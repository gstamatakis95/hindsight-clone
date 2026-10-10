package fake_test

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// Fake-specific tests of the CCR WP-15-1 reads: details the porttest suites can't reach through the port
// (an entity pruned after the archive, the bank-delete path of observation_scopes).

// TestArchivedUnitKeepsSnapshotNames: an archived unit reports the entity names it had when archived (even
// after the entity is pruned), the archive time as UpdatedAt and the archiving transaction's seq.
func TestArchivedUnitKeepsSnapshotNames(t *testing.T) {
	f := newFixture(t)
	u := f.unit(mine, "u1", "")
	var ent port.EntityID
	f.tx(func(tx port.Tx) error {
		var err error
		if ent, err = tx.Entities().Upsert(t.Context(), domain.Entity{Bank: mine, CanonicalName: "Ann Lee"}); err != nil {
			return err
		}
		return tx.Entities().Attach(t.Context(), u.ID, []port.EntityID{ent})
	})
	archivedAt := f.clock.Advance(time.Hour)
	var seq int64
	f.tx(func(tx port.Tx) error {
		var err error
		if seq, err = tx.Seq(t.Context(), mine); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), mine, u.ID, "wrong")
	})
	f.clock.Advance(time.Hour)
	f.tx(func(tx port.Tx) error {
		_, err := tx.Entities().PruneOrphans(t.Context(), mine, []port.EntityID{ent})
		return err
	})
	got, err := f.s.Read().GetUnit(t.Context(), mine, u.ID)
	if err != nil {
		t.Fatalf("GetUnit(archived): %v", err)
	}
	if got.State != domain.MemoryStateInvalidated || !slices.Equal(got.Entities, []string{"Ann Lee"}) ||
		!got.UpdatedAt.Equal(archivedAt) || got.Seq != seq {
		t.Errorf("GetUnit(archived) = state %q entities %q updated %v seq %d, want invalidated [Ann Lee] %v %d",
			got.State, got.Entities, got.UpdatedAt, got.Seq, archivedAt, seq)
	}
}

// TestBankDeleteDropsObservationScopes: observation_scopes rows survive DeleteAllObservations but not a bank
// delete, so a recreated bank starts with no scopes.
func TestBankDeleteDropsObservationScopes(t *testing.T) {
	f := newFixture(t)
	obs := domain.Unit{ID: "o1", Bank: mine, Type: domain.FactTypeObservation, Text: "o", Tags: []string{"x"}}
	f.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(t.Context(), mine); err != nil {
			return err
		}
		if err := tx.Units().UpsertObservation(t.Context(), obs); err != nil {
			return err
		}
		_, _, err := tx.Evidence().DeleteAllObservations(t.Context(), mine)
		return err
	})
	if got := f.scopes(); len(got) != 1 || got[0].Observations != 0 {
		t.Fatalf("ListScopes after DeleteAllObservations = %+v, want the {x} scope with 0 observations", got)
	}
	f.tx(func(tx port.Tx) error {
		if err := tx.Banks().MarkDeleting(t.Context(), mine, f.clock.Now()); err != nil {
			return err
		}
		if _, err := f.s.Read().ListScopes(t.Context(), mine, "", 10); err != nil {
			t.Errorf("Read().ListScopes while a tx tombstones the bank: %v; the reader must not see the tx", err)
		}
		if _, err := tx.ListScopes(t.Context(), mine, "", 10); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Tx.ListScopes(tombstoned in this tx) error = %v, want ErrNotFound", err)
		}
		return tx.Banks().Delete(t.Context(), mine)
	})
	f.tx(func(tx port.Tx) error {
		_, err := tx.Banks().Ensure(t.Context(), mine)
		return err
	})
	if got := f.scopes(); len(got) != 0 {
		t.Errorf("ListScopes of the recreated bank = %+v, want none", got)
	}
}

// scopes lists mine's observation scopes.
func (f fixture) scopes() []domain.ScopeInfo {
	f.t.Helper()
	got, err := f.s.Read().ListScopes(f.t.Context(), mine, "", 10)
	if err != nil {
		f.t.Fatalf("ListScopes: %v", err)
	}
	return got
}
