package document_test

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/document"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
	"time"
)

var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// uid is the unit id of a readable fixture name (Postgres stores unit ids as uuid).
func uid(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

// uids maps uid over names.
func uids(names ...string) []domain.UnitID { return pgtest.IDs[domain.UnitID](names...) }

// retagger drives a Service over db the way the retag tests read best: Retag returns the operation id ("" for
// a no-op) and Apply runs a claimed retag_document job inline.
type retagger struct{ s *document.Service }

// newRetagger builds the Service over db.
func newRetagger(db store.DB) retagger {
	return retagger{s: document.New(document.Deps{DB: db, Blob: fake.NewBlob(), Clock: clock.NewManual(time.Time{})})}
}

// Retag runs RetagDocument and returns the operation id.
func (r retagger) Retag(ctx context.Context, b domain.BankRef, doc domain.DocumentID, tags []string) (
	domain.OperationID, error,
) {
	op, err := r.s.RetagDocument(ctx, b, doc, tags)
	if err != nil {
		return "", fmt.Errorf("retag: %w", err)
	}
	return op.ID, nil
}

// Apply runs RunRetagJob on j inline.
func (r retagger) Apply(ctx context.Context, j store.Job) error {
	if err := r.s.RunRetagJob(ctx, jobs.Inline(j)); err != nil {
		return fmt.Errorf("retag job: %w", err)
	}
	return nil
}

// fixture is a bank with document d1 (units f1, f2), document d2 (unit f3) and observation o1 citing f1
// and f3; f3 is consolidated so a reset is visible.
type fixture struct {
	clock *clock.Manual
	store *postgres.DB
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	err := db.InTx(ctx, func(tx store.Tx) error {
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
			{ID: uid("f1"), Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "one", Tags: ab, Seq: seq},
			{ID: uid("f2"), Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld, Text: "two", Tags: ab, Seq: seq},
			{
				ID: uid("f3"), Bank: testBank, DocumentID: "d2", Type: domain.FactTypeWorld, Text: "three",
				Tags: []string{"x"}, Seq: seq,
			},
			{ID: uid("o1"), Bank: testBank, Type: domain.FactTypeObservation, Text: "obs", Seq: seq},
		}
		if err := tx.Units().Insert(ctx, units); err != nil {
			return err
		}
		if err := tx.Evidence().Add(ctx, uid("o1"), []domain.Evidence{{Unit: uid("f1")}, {Unit: uid("f3")}}); err != nil {
			return err
		}
		return tx.Units().MarkConsolidated(ctx, uids("f1", "f2", "f3"), clock.Now())
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return fixture{clock: clock, store: db}
}

// units reads live units by fixture name, keyed by name.
func (f fixture) units(t *testing.T, names ...domain.UnitID) map[domain.UnitID]domain.Unit {
	t.Helper()
	ids := make([]domain.UnitID, len(names))
	for i, n := range names {
		ids[i] = uid(string(n))
	}
	us, err := f.store.Read().GetUnits(context.Background(), testBank, ids)
	if err != nil {
		t.Fatalf("GetUnits: %v", err)
	}
	out := map[domain.UnitID]domain.Unit{}
	for _, u := range us {
		out[domain.UnitID(pgtest.Name(u.ID))] = u
	}
	return out
}

// jobs lists the bank's operations of kind.
func (f fixture) jobs(t *testing.T, kind domain.OperationKind) []domain.Operation {
	t.Helper()
	var ops []domain.Operation
	err := f.store.InTx(context.Background(), func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(context.Background(), testBank, store.OperationFilter{Kind: kind})
		return err
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return ops
}

// docTags reads a document's tags.
func (f fixture) docTags(t *testing.T, id domain.DocumentID) []string {
	t.Helper()
	var d domain.Document
	err := f.store.InTx(context.Background(), func(tx store.Tx) error {
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
func (f fixture) claimRetag(t *testing.T) store.Job {
	t.Helper()
	jobs, err := f.store.Claim(context.Background(), "w1", []domain.OperationKind{domain.OperationKindRetagDocument}, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("Claim retag: %v, %d jobs", err, len(jobs))
	}
	return jobs[0]
}

func TestRetagSetEqualIsNoop(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := newRetagger(f.store)
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
	r := newRetagger(f.store)

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
	// RetagDocument's transaction retags the units (decision 1900); the observation stays until the job.
	if us := f.units(t, "f1", "o1"); !slices.Equal(us["f1"].Tags, []string{"c"}) || us["o1"].ID == "" {
		t.Fatalf("before the job: f1 tags %v, o1 present %t; want [c] and o1 kept", us["f1"].Tags, us["o1"].ID != "")
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
	for _, id := range []domain.UnitID{"f1", "f2"} {
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
	if graph := f.jobs(t, domain.OperationKindGraphMaintenance); len(graph) != 1 || graph[0].SerialKey != "graph" {
		t.Fatalf("graph_maintenance jobs = %+v, want one (the deleted observation removed a unit)", graph)
	}
}

func TestRetagJobReadsCurrentTags(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := newRetagger(f.store)

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
	r := newRetagger(f.store)

	if _, err := r.Retag(ctx, testBank, "nope", []string{"a"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown document: %v, want ErrNotFound", err)
	}
	other := domain.BankRef{Tenant: "acme", Bank: "other"}
	if _, err := r.Retag(ctx, other, "d1", []string{"a"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown bank: %v, want ErrNotFound", err)
	}
	if err := r.Apply(ctx, store.Job{Bank: testBank, Payload: []byte(`{"document_id":"nope"}`)}); err != nil {
		t.Fatalf("Apply on a gone document: %v, want a no-op", err)
	}
	gone := store.Job{Bank: other, Payload: []byte(`{"document_id":"d1"}`)}
	if err := r.Apply(ctx, gone); !errors.Is(err, domain.ErrNotFound) || !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Apply on a gone bank: %v, want ErrCancelled wrapping ErrNotFound (not a silent no-op)", err)
	}
	if err := r.Apply(ctx, store.Job{Bank: testBank, Payload: []byte(`{}`)}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RunRetagJob without document_id: %v, want ErrInvalidArgument", err)
	}

	tomb := func(tx store.Tx) error { return tx.Banks().MarkDeleting(ctx, testBank, f.clock.Now()) }
	if err := f.store.InTx(ctx, tomb); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := r.Retag(ctx, testBank, "d1", []string{"z"}); !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("retag on a deleting bank: %v, want ErrBankDeleting", err)
	}
	err := r.Apply(ctx, store.Job{Bank: testBank, Payload: []byte(`{"document_id":"d1"}`)})
	if !errors.Is(err, domain.ErrBankDeleting) {
		t.Fatalf("Apply on a deleting bank: %v, want ErrBankDeleting", err)
	}
}

// TestRetagSetEqualAfterFailedRetagReenqueues: the tags were committed on the document (and its units) but
// the retag job failed, so the observations derived under the old tags were never cleared; repeating the
// same tags must re-enqueue the job.
func TestRetagSetEqualAfterFailedRetagReenqueues(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	r := newRetagger(f.store)

	first, err := r.Retag(ctx, testBank, "d1", []string{"c"})
	if err != nil || first == "" {
		t.Fatalf("Retag = %q, %v", first, err)
	}
	job := f.claimRetag(t)
	if err := f.store.Fail(ctx, job.ID, errors.New("boom"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	again, err := r.Retag(ctx, testBank, "d1", []string{"C"})
	if err != nil {
		t.Fatalf("Retag after failure: %v", err)
	}
	if again == "" || again == first {
		t.Fatalf("same-tags retag after a failed one = %q, want a new operation (first %s)", again, first)
	}
	if err := r.Apply(ctx, f.claimRetag(t)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if us := f.units(t, "f1"); !slices.Equal(us["f1"].Tags, []string{"c"}) {
		t.Fatalf("unit tags = %v, want [c]", us["f1"].Tags)
	}
	// Now the newest retag completed (well, ran): mark it done, after which the same tags are a no-op.
	jobs := f.jobs(t, domain.OperationKindRetagDocument)
	for _, op := range jobs {
		if op.ID == again && op.Status == domain.OperationStatusProcessing {
			if err := f.store.Complete(ctx, again, nil, nil); err != nil {
				t.Fatalf("Complete: %v", err)
			}
		}
	}
	if id, err := r.Retag(ctx, testBank, "d1", []string{"c"}); err != nil || id != "" {
		t.Fatalf("same tags after a completed retag = %q, %v; want a no-op", id, err)
	}
}
