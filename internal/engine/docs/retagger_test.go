package docs_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

var testBank = port.BankRef{Tenant: "acme", Bank: "notes"}

// fixture is a bank with document d1 (units f1, f2), document d2 (unit f3) and observation o1 citing f1
// and f3; f3 is consolidated so a reset is visible.
type fixture struct {
	clock *fake.Clock
	store *fake.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	clock := fake.NewClock(time.Time{})
	store := fake.NewStore(clock)
	err := store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, testBank); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		ab := []string{"a", "b"}
		for _, d := range []domain.Document{
			{ID: "d1", Bank: testBank, Tags: ab, BlobKey: "k1"},
			{ID: "d2", Bank: testBank, Tags: []string{"x"}},
		} {
			if err := tx.Documents().Upsert(ctx, d); err != nil {
				return err
			}
		}
		units := []domain.Unit{
			{ID: "f1", Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "one", Tags: ab, Seq: seq},
			{ID: "f2", Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "two", Tags: ab, Seq: seq},
			{
				ID: "f3", Bank: testBank, DocumentID: "d2", Type: domain.FactTypeWorld, Text: "three",
				Tags: []string{"x"}, Seq: seq,
			},
			{ID: "o1", Bank: testBank, Type: domain.FactTypeObservation, Text: "obs", Seq: seq},
		}
		if err := tx.Units().Insert(ctx, units); err != nil {
			return err
		}
		if err := tx.Evidence().Add(ctx, "o1", []domain.Evidence{{Unit: "f1"}, {Unit: "f3"}}); err != nil {
			return err
		}
		return tx.Units().MarkConsolidated(ctx, []port.UnitID{"f1", "f2", "f3"}, clock.Now())
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return fixture{clock: clock, store: store}
}

// units reads live units by id.
func (f fixture) units(t *testing.T, ids ...port.UnitID) map[port.UnitID]domain.Unit {
	t.Helper()
	us, err := f.store.Read().GetUnits(context.Background(), testBank, ids)
	if err != nil {
		t.Fatalf("GetUnits: %v", err)
	}
	out := map[port.UnitID]domain.Unit{}
	for _, u := range us {
		out[u.ID] = u
	}
	return out
}

// jobs lists the bank's operations of kind.
func (f fixture) jobs(t *testing.T, kind port.JobKind) []domain.Operation {
	t.Helper()
	var ops []domain.Operation
	err := f.store.InTx(context.Background(), func(tx port.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(context.Background(), testBank, port.OperationFilter{Kind: kind})
		return err
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return ops
}

// docTags reads a document's tags.
func (f fixture) docTags(t *testing.T, id port.DocumentID) []string {
	t.Helper()
	var d domain.Document
	err := f.store.InTx(context.Background(), func(tx port.Tx) error {
		var err error
		d, err = tx.Documents().Get(context.Background(), testBank, id)
		return err
	})
	if err != nil {
		t.Fatalf("Documents.Get: %v", err)
	}
	return d.Tags
}

// claimRetag claims the pending retag_document job.
func (f fixture) claimRetag(t *testing.T) port.Job {
	t.Helper()
	jobs, err := f.store.Claim(context.Background(), "w1", []port.JobKind{domain.OperationKindRetagDocument}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim retag: %v, %d jobs", err, len(jobs))
	}
	return jobs[0]
}

func TestRetagSetEqualIsNoop(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := docs.NewRetagger(f.store)
	seqBefore, _ := f.store.Read().BankSeq(ctx, testBank)

	id, err := r.Retag(ctx, testBank, "d1", []string{" B", "a", "A"})
	if err != nil {
		t.Fatalf("Retag: %v", err)
	}
	if id != "" {
		t.Fatalf("set-equal retag returned operation %q, want none", id)
	}
	if ops := f.jobs(t, domain.OperationKindRetagDocument); len(ops) != 0 {
		t.Fatalf("set-equal retag enqueued %d retag jobs", len(ops))
	}
	if got := f.docTags(t, "d1"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("document tags = %v, want [a b]", got)
	}
	us := f.units(t, "f1", "f2", "o1")
	if len(us) != 3 {
		t.Fatalf("units after no-op = %v, want f1, f2, o1 live", us)
	}
	if !slices.Equal(us["f1"].Tags, []string{"a", "b"}) || us["f1"].ConsolidatedAt == nil {
		t.Fatalf("f1 changed by a no-op retag: %+v", us["f1"])
	}
	if seqAfter, _ := f.store.Read().BankSeq(ctx, testBank); seqAfter != seqBefore {
		t.Fatalf("no-op retag bumped change_seq %d -> %d", seqBefore, seqAfter)
	}
}

func TestRetagChangeClearsObservations(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := docs.NewRetagger(f.store)

	id, err := r.Retag(ctx, testBank, "d1", []string{"C"})
	if err != nil || id == "" {
		t.Fatalf("Retag = %q, %v; want an operation", id, err)
	}
	if got := f.docTags(t, "d1"); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("document tags = %v, want [c]", got)
	}
	ops := f.jobs(t, domain.OperationKindRetagDocument)
	if len(ops) != 1 || ops[0].ID != id || ops[0].SerialKey != "doc:d1" {
		t.Fatalf("retag jobs = %+v, want one with id %s, serial doc:d1", ops, id)
	}
	if us := f.units(t, "f1"); !slices.Equal(us["f1"].Tags, []string{"a", "b"}) {
		t.Fatalf("unit tags changed before the job ran: %v", us["f1"].Tags)
	}

	job := f.claimRetag(t)
	for i := range 2 { // a second run (after a reclaim) is harmless
		if err := r.Apply(ctx, job); err != nil {
			t.Fatalf("Apply #%d: %v", i+1, err)
		}
	}
	f.assertRetagged(t)
}

// assertRetagged checks the state after d1 was retagged to [c]: o1 gone, f1/f2 retagged and unconsolidated,
// co-source f3 reset but not retagged, one consolidate job.
func (f fixture) assertRetagged(t *testing.T) {
	t.Helper()
	us := f.units(t, "f1", "f2", "f3", "o1")
	if _, ok := us["o1"]; ok {
		t.Fatal("observation citing a retagged unit survived")
	}
	for _, id := range []port.UnitID{"f1", "f2"} {
		if !slices.Equal(us[id].Tags, []string{"c"}) || us[id].ConsolidatedAt != nil {
			t.Fatalf("unit %s = tags %v consolidated %v; want [c] and unconsolidated", id, us[id].Tags,
				us[id].ConsolidatedAt)
		}
	}
	if us["f3"].ConsolidatedAt != nil {
		t.Fatal("co-source f3 of the deleted observation was not reset")
	}
	if !slices.Equal(us["f3"].Tags, []string{"x"}) {
		t.Fatalf("unit of another document retagged: %v", us["f3"].Tags)
	}
	cons := f.jobs(t, domain.OperationKindConsolidate)
	if len(cons) != 1 || cons[0].SerialKey != "consolidate" {
		t.Fatalf("consolidate jobs = %+v, want one (deduplicated)", cons)
	}
}

func TestRetagJobReadsCurrentTags(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := docs.NewRetagger(f.store)

	first, err := r.Retag(ctx, testBank, "d1", []string{"b2"})
	if err != nil {
		t.Fatalf("Retag A->B: %v", err)
	}
	second, err := r.Retag(ctx, testBank, "d1", []string{"c3"})
	if err != nil {
		t.Fatalf("Retag B->C: %v", err)
	}
	if first != second {
		t.Fatalf("second retag got %s, want it to join the pending %s", second, first)
	}
	if err := r.Apply(ctx, f.claimRetag(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if us := f.units(t, "f1"); !slices.Equal(us["f1"].Tags, []string{"c3"}) {
		t.Fatalf("unit tags = %v, want the latest [c3]", us["f1"].Tags)
	}
}

func TestRetagUnknownDocument(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := docs.NewRetagger(f.store)

	if _, err := r.Retag(ctx, testBank, "nope", []string{"a"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown document: %v, want ErrNotFound", err)
	}
	other := port.BankRef{Tenant: "acme", Bank: "other"}
	if _, err := r.Retag(ctx, other, "d1", []string{"a"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown bank: %v, want ErrNotFound", err)
	}
	if err := r.Apply(ctx, port.Job{Bank: testBank, Payload: []byte(`{"document_id":"nope"}`)}); err != nil {
		t.Fatalf("Apply on a gone document: %v, want a no-op", err)
	}
	if err := r.Apply(ctx, port.Job{Bank: testBank, Payload: []byte(`{}`)}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Apply without document_id: %v, want ErrInvalidArgument", err)
	}

	tomb := func(tx port.Tx) error { return tx.Banks().MarkDeleting(ctx, testBank, f.clock.Now()) }
	if err := f.store.InTx(ctx, tomb); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := r.Retag(ctx, testBank, "d1", []string{"z"}); !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("retag on a deleting bank: %v, want ErrBankDeleting", err)
	}
	err := r.Apply(ctx, port.Job{Bank: testBank, Payload: []byte(`{"document_id":"d1"}`)})
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("Apply on a deleting bank: %v, want ErrBankDeleting", err)
	}
}
