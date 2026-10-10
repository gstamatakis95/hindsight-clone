package fake_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"
)

// Tests for WP-02 review round 1: places where the fake must be as strict as production.

// TestLinkRejectsSelfLink: a link from a unit to itself is ErrInvalidArgument (memory_links_no_self_link).
func TestLinkRejectsSelfLink(t *testing.T) {
	f := newFixture(t)
	u := f.unit(mine, "u", "")
	err := f.txErr(func(tx port.Tx) error {
		return tx.Links().Insert(t.Context(), []domain.Link{{Bank: mine, From: u.ID, To: u.ID, Kind: domain.LinkSemantic}})
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("self-link Insert = %v, want ErrInvalidArgument", err)
	}
}

// TestLabelEntityUpsertUpdatesExisting: a label entity always resolves to the (bank, kind, label value) row,
// even with another or no id, and the update path runs (LastSeen advances; no duplicate row).
func TestLabelEntityUpsertUpdatesExisting(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	first, later := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	red := domain.Entity{Bank: mine, CanonicalName: "color:red", Kind: "color", LabelValue: "red", LastSeen: &first}
	var id1, id2 port.EntityID
	f.tx(func(tx port.Tx) error { var err error; id1, err = tx.Entities().Upsert(ctx, red); return err })
	red.ID, red.LastSeen = "some-other-id", &later
	f.tx(func(tx port.Tx) error { var err error; id2, err = tx.Entities().Upsert(ctx, red); return err })
	var cs []domain.EntityCandidate
	f.tx(func(tx port.Tx) error {
		var err error
		cs, err = tx.Entities().Candidates(ctx, mine, "color:red", 10)
		return err
	})
	if id1 != id2 || len(cs) != 1 || cs[0].Entity.ID != id1 || cs[0].Entity.LastSeen == nil ||
		!cs[0].Entity.LastSeen.Equal(later) {
		t.Errorf("ids %s/%s, candidates %+v; want one row %s with LastSeen %v", id1, id2, cs, id1, later)
	}
}

// TestRestoreRequiresSeq: Restore without Tx.Seq for the bank is ErrFailedPrecondition and restores nothing.
func TestRestoreRequiresSeq(t *testing.T) {
	f := newFixture(t)
	u := f.unit(mine, "u", "")
	f.tx(func(tx port.Tx) error {
		if _, err := tx.Seq(t.Context(), mine); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), mine, u.ID, "wrong")
	})
	err := f.txErr(func(tx port.Tx) error { return tx.Units().Restore(t.Context(), mine, u.ID) })
	if !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Errorf("Restore without Seq = %v, want ErrFailedPrecondition", err)
	}
	if us, _ := f.s.Read().GetUnits(t.Context(), mine, []port.UnitID{u.ID}); len(us) != 0 {
		t.Errorf("unit restored without Seq: %+v", us)
	}
}

// TestInTxContextCancelledDuringFn: a context cancelled while the callback runs aborts the commit.
func TestInTxContextCancelledDuringFn(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	err := f.s.InTx(ctx, func(tx port.Tx) error {
		_, err := tx.Seq(ctx, mine)
		cancel()
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("InTx with ctx cancelled during fn = %v, want context.Canceled", err)
	}
	if seq, _ := f.s.Read().BankSeq(t.Context(), mine); seq != 0 {
		t.Errorf("BankSeq = %d, want 0 (nothing committed)", seq)
	}
}

// TestUnnormalizedTagsRejected: unit and document writes with tags not in domain.NormalizeTags form are
// ErrInvalidArgument.
func TestUnnormalizedTagsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.unit(mine, "u", "doc-1")
	bad := []string{"Travel", "people "}
	writes := map[string]func(tx port.Tx) error{
		"Units.Insert": func(tx port.Tx) error {
			return tx.Units().Insert(ctx, []domain.Unit{{ID: "v", Bank: mine, Type: domain.FactTypeWorld, Tags: bad}})
		},
		"Units.SetDocumentTags": func(tx port.Tx) error {
			_, err := tx.Units().SetDocumentTags(ctx, mine, "doc-1", bad, 1)
			return err
		},
		"Documents.Upsert": func(tx port.Tx) error {
			return tx.Documents().Upsert(ctx, domain.Document{ID: "doc-2", Bank: mine, Tags: bad})
		},
		"Documents.SetTags": func(tx port.Tx) error { return tx.Documents().SetTags(ctx, mine, "doc-1", bad) },
	}
	for name, write := range writes {
		if err := f.txErr(write); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s with un-normalized tags = %v, want ErrInvalidArgument", name, err)
		}
	}
}

// TestFactTypeChecks: Update edits facts only, UpsertObservation takes observations only, and evidence ties an
// observation to facts.
func TestFactTypeChecks(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	fact := f.unit(mine, "f", "")
	obs := domain.Unit{ID: "o", Bank: mine, Type: domain.FactTypeObservation, Text: "obs"}
	f.tx(func(tx port.Tx) error { return tx.Units().Insert(ctx, []domain.Unit{obs}) })
	writes := map[string]func(tx port.Tx) error{
		"Update(observation)":         func(tx port.Tx) error { return tx.Units().Update(ctx, obs) },
		"UpsertObservation(fact)":     func(tx port.Tx) error { return tx.Units().UpsertObservation(ctx, fact) },
		"Evidence.Add(owner is fact)": func(tx port.Tx) error { return tx.Evidence().Add(ctx, fact.ID, nil) },
		"Evidence.Add(cites observation)": func(tx port.Tx) error {
			return tx.Evidence().Add(ctx, obs.ID, []domain.Evidence{{Unit: obs.ID}})
		},
	}
	for name, write := range writes {
		if err := f.txErr(write); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
}
