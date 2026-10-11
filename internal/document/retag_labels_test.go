package document_test

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// addLabelledUnit adds unit f4 to d1, tagged with d1's tags [a b] plus the write_to_tags label tag topic:x,
// consolidated, and archived unit f5 with the same tags.
func (f fixture) addLabelledUnit(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tags := []string{"a", "b", "topic:x"}
	err := f.store.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		if err := tx.Units().Insert(ctx, []domain.Unit{
			{ID: uid("f4"), Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "four", Tags: tags, Seq: seq},
			{ID: uid("f5"), Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "five", Tags: tags, Seq: seq},
		}); err != nil {
			return err
		}
		if err := tx.Units().MarkConsolidated(ctx, uids("f4"), f.clock.Now()); err != nil {
			return err
		}
		return tx.Units().Archive(ctx, testBank, uid("f5"), "test")
	})
	if err != nil {
		t.Fatalf("seed labelled units: %v", err)
	}
}

// restore brings archived unit id back.
func (f fixture) restore(t *testing.T, id domain.UnitID) {
	t.Helper()
	ctx := context.Background()
	err := f.store.InTx(ctx, func(tx store.Tx) error {
		if _, err := tx.Seq(ctx, testBank); err != nil {
			return err
		}
		return tx.Units().Restore(ctx, testBank, uid(string(id)))
	})
	if err != nil {
		t.Fatalf("Restore %s: %v", id, err)
	}
}

// TestRetagKeepsLabelTags: retagging d1 [a b]→[c] replaces only the document-derived
// tags, so a unit's write_to_tags label tag survives, on live and archived units, through UpdateDocument
// and through retag_document (run twice). The live unit is re-consolidated. A set-equal retag afterwards is
// still a no-op that bumps no change seq.
func TestRetagKeepsLabelTags(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.addLabelledUnit(t)
	r := newRetagger(f.store)
	want := []string{"c", "topic:x"}

	if id, err := r.Retag(ctx, testBank, "d1", []string{"C"}); err != nil || id == "" {
		t.Fatalf("Retag = %q, %v; want an operation", id, err)
	}
	if us := f.units(t, "f1", "f4"); !slices.Equal(us["f4"].Tags, want) || !slices.Equal(us["f1"].Tags, []string{"c"}) {
		t.Fatalf("after UpdateDocument: f4 %v, f1 %v; want %v and [c]", us["f4"].Tags, us["f1"].Tags, want)
	}
	job := f.claimRetag(t)
	for i := range 2 {
		if err := r.Apply(ctx, job); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}
	us := f.units(t, "f4")
	if !slices.Equal(us["f4"].Tags, want) || us["f4"].ConsolidatedAt != nil {
		t.Fatalf("after retag_document: f4 tags %v consolidated %v; want %v, unconsolidated", us["f4"].Tags,
			us["f4"].ConsolidatedAt, want)
	}
	if err := f.store.Complete(ctx, job.ID, nil, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	seqBefore, _ := f.store.Read().BankSeq(ctx, testBank)
	if id, err := r.Retag(ctx, testBank, "d1", []string{" c "}); err != nil || id != "" {
		t.Fatalf("set-equal retag = %q, %v; want a no-op", id, err)
	}
	if seqAfter, _ := f.store.Read().BankSeq(ctx, testBank); seqAfter != seqBefore {
		t.Fatalf("set-equal retag bumped change_seq %d -> %d", seqBefore, seqAfter)
	}
	if us := f.units(t, "f4"); !slices.Equal(us["f4"].Tags, want) {
		t.Fatalf("after set-equal retag: f4 tags %v, want %v", us["f4"].Tags, want)
	}

	f.restore(t, "f5")
	if us := f.units(t, "f5"); !slices.Equal(us["f5"].Tags, want) {
		t.Fatalf("archived f5 after retag and Restore: tags %v, want %v", us["f5"].Tags, want)
	}
}
