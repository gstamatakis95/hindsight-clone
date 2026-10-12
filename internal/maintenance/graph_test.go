package maintenance

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"sync"
	"testing"
	"time"
)

var testBank = domain.NewBankRef("acme", "org", "notes")

// stubLinker records the units it is asked to top up; advance moves the clock on its first call.
type stubLinker struct {
	mu      sync.Mutex
	clock   *clock.Manual
	advance time.Duration
	calls   [][]domain.UnitID
}

func (l *stubLinker) Link(
	_ context.Context, _ store.Tx, _ domain.BankRef, units []domain.Unit, _ linking.CauseMap,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]domain.UnitID, len(units))
	for i, u := range units {
		ids[i] = u.ID
	}
	l.calls = append(l.calls, ids)
	if len(l.calls) == 1 && l.advance > 0 {
		l.clock.Advance(l.advance)
	}
	return nil
}

// LinkWith is Link; the graph job never passes a config.
func (l *stubLinker) LinkWith(
	ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes linking.CauseMap,
	_ domain.BankConfig,
) error {
	return l.Link(ctx, tx, b, units, causes)
}

// linked returns every unit the linker saw, sorted.
func (l *stubLinker) linked() []domain.UnitID {
	var out []domain.UnitID
	for _, c := range l.calls {
		out = append(out, c...)
	}
	slices.Sort(out)
	return out
}

// seedHub stores a hub unit (document "hub") linked to n neighbour units (document "rest") and two entities:
// "orphan" attached only to the hub and "kept" attached to the hub and the first neighbour. It then deletes
// the hub's document, which queues the n neighbours in graph_dirty and both entities in entity_maintenance.
func seedHub(t *testing.T, db *postgres.DB, n int) (orphan, kept domain.EntityID) {
	t.Helper()
	ctx := context.Background()
	hub := pgtest.ID[domain.UnitID]("hub")
	err := db.InTx(ctx, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, testBank); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		for _, d := range []domain.DocumentID{"hub", "rest"} {
			if err := tx.Documents().Upsert(ctx, domain.Document{ID: d, Bank: testBank}); err != nil {
				return err
			}
		}
		units := []domain.Unit{{ID: hub, Bank: testBank, DocumentID: "hub", Type: domain.FactTypeWorld, Seq: seq}}
		var links []graph.Link
		for i := range n {
			id := pgtest.ID[domain.UnitID](fmt.Sprintf("n%03d", i))
			units = append(units, domain.Unit{
				ID: id, Bank: testBank, DocumentID: "rest", Type: domain.FactTypeWorld,
				Seq: seq,
			})
			links = append(links, graph.Link{Bank: testBank, From: id, To: hub, Kind: graph.KindTemporal, Weight: 1})
		}
		if err := tx.Units().Insert(ctx, units); err != nil {
			return err
		}
		if err := tx.Graph().AddLinks(ctx, links); err != nil {
			return err
		}
		if orphan, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: testBank, CanonicalName: "Orphan"}); err != nil {
			return err
		}
		if kept, err = tx.Entities().Upsert(ctx, domain.Entity{Bank: testBank, CanonicalName: "Kept"}); err != nil {
			return err
		}
		if err := tx.Entities().Attach(ctx, hub, []domain.EntityID{orphan, kept}); err != nil {
			return err
		}
		if err := tx.Entities().Attach(ctx, pgtest.ID[domain.UnitID]("n000"), []domain.EntityID{kept}); err != nil {
			return err
		}
		_, err = tx.Units().DeleteByDocument(ctx, testBank, "hub")
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return orphan, kept
}

// queues returns what is left in graph_dirty and entity_maintenance.
func queues(t *testing.T, db *postgres.DB) (dirty []domain.UnitID, pending []domain.EntityID) {
	t.Helper()
	ctx := context.Background()
	err := db.InTx(ctx, func(tx store.Tx) error {
		var err error
		if dirty, err = tx.Units().DirtyLinks(ctx, testBank, 1000); err != nil {
			return err
		}
		pending, err = tx.Entities().PendingMaintenance(ctx, testBank, 1000)
		return err
	})
	if err != nil {
		t.Fatalf("queues: %v", err)
	}
	return dirty, pending
}

// entityNames returns the canonical names of the entities resembling "Orphan" or "Kept".
func entityNames(t *testing.T, db *postgres.DB) []string {
	t.Helper()
	ctx := context.Background()
	var names []string
	err := db.InTx(ctx, func(tx store.Tx) error {
		for _, q := range []string{"Orphan", "Kept"} {
			cs, err := tx.Entities().Candidates(ctx, testBank, q, 10)
			if err != nil {
				return err
			}
			for _, c := range cs {
				names = append(names, c.Entity.CanonicalName)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	return names
}

func TestGraphMaintenanceTopsUpAndPrunes(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	pgtest.CreateOrgs(t, db,
		domain.NewBankRef("acme", "org", ""), domain.NewBankRef("t1", "org", ""), domain.NewBankRef("t2", "org", ""))
	seedHub(t, db, 3)
	if d, p := queues(t, db); len(d) != 3 || len(p) != 2 {
		t.Fatalf("seeded queues: %d dirty, %d entities; want 3 and 2", len(d), len(p))
	}
	l := &stubLinker{clock: clock}
	g := New(Deps{Store: db, Linker: l, Clock: clock})

	done, err := g.drainGraph(ctx, testBank, DefaultGraphBudget)
	if err != nil || !done {
		t.Fatalf("Run = %v, %v; want done", done, err)
	}
	got := pgtest.Names(l.linked())
	slices.Sort(got) // the dirty queue drains in id order
	if want := []string{"n000", "n001", "n002"}; !slices.Equal(got, want) {
		t.Fatalf("linker topped up %v, want %v", got, want)
	}
	if d, p := queues(t, db); len(d) != 0 || len(p) != 0 {
		t.Fatalf("queues after run: %v dirty, %v entities; want both empty", d, p)
	}
	if names := entityNames(t, db); !slices.Equal(names, []string{"Kept"}) {
		t.Fatalf("entities left = %v, want only Kept (Orphan pruned)", names)
	}
	if done, err := g.drainGraph(ctx, testBank, DefaultGraphBudget); err != nil || !done || len(l.calls) != 1 {
		t.Fatalf("idle rerun = %v, %v with %d linker calls; want done and no new call", done, err, len(l.calls))
	}
}

func TestGraphMaintenanceResumesAfterBudget(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	pgtest.CreateOrgs(t, db,
		domain.NewBankRef("acme", "org", ""), domain.NewBankRef("t1", "org", ""), domain.NewBankRef("t2", "org", ""))
	n := DirtyBatch + 50
	seedHub(t, db, n)
	// The first batch "takes" longer than the whole budget.
	l := &stubLinker{clock: clock, advance: DefaultGraphBudget + time.Second}
	g := New(Deps{Store: db, Linker: l, Clock: clock})

	done, err := g.drainGraph(ctx, testBank, DefaultGraphBudget)
	if err != nil || done {
		t.Fatalf("first Run = %v, %v; want stopped by the budget (done=false)", done, err)
	}
	if d, _ := queues(t, db); len(d) != n-DirtyBatch {
		t.Fatalf("after the first run %d units left dirty, want %d", len(d), n-DirtyBatch)
	}

	done, err = g.drainGraph(ctx, testBank, DefaultGraphBudget)
	if err != nil || !done {
		t.Fatalf("second Run = %v, %v; want it to resume and finish", done, err)
	}
	if d, p := queues(t, db); len(d) != 0 || len(p) != 0 {
		t.Fatalf("queues after the second run: %d dirty, %d entities; want both empty", len(d), len(p))
	}
	if got := l.linked(); len(got) != n || len(slices.Compact(got)) != n {
		t.Fatalf("linker saw %d units (%d distinct), want each of %d once", len(got), len(slices.Compact(got)), n)
	}
}

func TestGraphMaintenanceStopsOnTombstone(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	pgtest.CreateOrgs(t, db,
		domain.NewBankRef("acme", "org", ""), domain.NewBankRef("t1", "org", ""), domain.NewBankRef("t2", "org", ""))
	seedHub(t, db, 2)
	g := New(Deps{Store: db, Linker: &stubLinker{clock: clock}, Clock: clock})

	if err := db.InTx(ctx, func(tx store.Tx) error {
		return tx.Banks().MarkDeleting(ctx, testBank, clock.Now())
	}); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := g.drainGraph(ctx, testBank, DefaultGraphBudget); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Run on a deleting bank: %v, want ErrCancelled", err)
	}
	if d, _ := queues(t, db); len(d) != 2 {
		t.Fatalf("a run on a deleting bank touched graph_dirty: %d left", len(d))
	}
	gone := domain.NewBankRef("acme", "org", "gone")
	if _, err := g.drainGraph(ctx, gone, DefaultGraphBudget); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Run on an unknown bank: %v, want ErrCancelled", err)
	}
}

// archivingLinker archives unit victim inside the maintainer's own transaction on its first call, which
// queues victim's live neighbours in graph_dirty after this batch already read the queue.
type archivingLinker struct {
	stubLinker
	victim domain.UnitID
}

func (l *archivingLinker) Link(
	ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes linking.CauseMap,
) error {
	if len(l.calls) == 0 {
		if err := tx.Units().Archive(ctx, b, l.victim, "test"); err != nil {
			return err
		}
	}
	return l.stubLinker.Link(ctx, tx, b, units, causes)
}

// TestGraphMaintenanceRechecksAfterFinalBatch: work queued during the last working batch (here inside it)
// is drained before the drain reports done.
func TestGraphMaintenanceRechecksAfterFinalBatch(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	pgtest.CreateOrgs(t, db,
		domain.NewBankRef("acme", "org", ""), domain.NewBankRef("t1", "org", ""), domain.NewBankRef("t2", "org", ""))
	seedHub(t, db, 2)
	z, w := pgtest.ID[domain.UnitID]("z"), pgtest.ID[domain.UnitID]("w")
	err := db.InTx(ctx, func(tx store.Tx) error {
		units := []domain.Unit{
			{ID: z, Bank: testBank, DocumentID: "rest", Type: domain.FactTypeWorld},
			{ID: w, Bank: testBank, DocumentID: "rest", Type: domain.FactTypeWorld},
		}
		if err := tx.Units().Insert(ctx, units); err != nil {
			return err
		}
		return tx.Graph().AddLinks(ctx, []graph.Link{
			{Bank: testBank, From: z, To: w, Kind: graph.KindTemporal, Weight: 1},
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	l := &archivingLinker{stubLinker: stubLinker{clock: clock}, victim: w}
	g := New(Deps{Store: db, Linker: l, Clock: clock})

	done, err := g.drainGraph(ctx, testBank, DefaultGraphBudget)
	if err != nil || !done {
		t.Fatalf("Run = %v, %v; want done", done, err)
	}
	if !slices.Contains(l.linked(), z) {
		t.Fatalf("linker saw %v; want z, queued during the first batch, topped up too", l.linked())
	}
	if d, p := queues(t, db); len(d) != 0 || len(p) != 0 {
		t.Fatalf("queues after run: %v dirty, %v entities; want both empty", d, p)
	}
}

// dirtyNeighbour inserts two linked facts and archives one, which records the other in graph_dirty.
func dirtyNeighbour(t *testing.T, db *postgres.DB) {
	t.Helper()
	ctx := t.Context()
	a, b := pgtest.ID[domain.UnitID]("a"), pgtest.ID[domain.UnitID]("b")
	if err := db.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		if err := tx.Units().Insert(ctx, []domain.Unit{
			{ID: a, Bank: testBank, Type: domain.FactTypeWorld, Text: "a", Seq: seq},
			{ID: b, Bank: testBank, Type: domain.FactTypeWorld, Text: "b", Seq: seq},
		}); err != nil {
			return err
		}
		return tx.Graph().AddLinks(ctx, []graph.Link{
			{Bank: testBank, From: a, To: b, Kind: graph.KindTemporal, Weight: 1},
			{Bank: testBank, From: b, To: a, Kind: graph.KindTemporal, Weight: 1},
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InTx(ctx, func(tx store.Tx) error {
		if _, err := tx.Seq(ctx, testBank); err != nil {
			return err
		}
		return tx.Units().Archive(ctx, testBank, a, "test")
	}); err != nil {
		t.Fatal(err)
	}
}

// pendingGraphJobs returns testBank's PENDING graph_maintenance operations.
func pendingGraphJobs(t *testing.T, db *postgres.DB) []domain.Operation {
	t.Helper()
	var ops []domain.Operation
	if err := db.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(t.Context(), testBank, store.OperationFilter{
			Kind: domain.OperationKindGraphMaintenance, Status: domain.OperationStatusPending, PageSize: 100,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ops
}

// TestGraphJobReenqueuesAndRechecks: a run stopped by its budget re-enqueues itself; a run that drains both
// queues does not; and a finished drain whose re-check finds queued rows (a removal that joined the running
// job after its final empty batch) re-enqueues too.
func TestGraphJobReenqueuesAndRechecks(t *testing.T) {
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	pgtest.CreateOrgs(t, db,
		domain.NewBankRef("acme", "org", ""), domain.NewBankRef("t1", "org", ""), domain.NewBankRef("t2", "org", ""))
	seedHub(t, db, DirtyBatch+50)
	l := &stubLinker{clock: clock, advance: DefaultGraphBudget + time.Second}
	g := New(Deps{Store: db, Linker: l, Clock: clock})
	job := store.Job{Bank: testBank, Kind: domain.OperationKindGraphMaintenance, SerialKey: "graph"}

	if err := g.RunGraphJob(t.Context(), jobs.Inline(job)); err != nil {
		t.Fatal(err)
	}
	ops := pendingGraphJobs(t, db)
	if len(ops) != 1 || ops[0].SerialKey != "graph" {
		t.Fatalf("budget ran out: continuations %+v, want one", ops)
	}
	if err := db.InTx(t.Context(), func(tx store.Tx) error {
		_, err := tx.Queue().Cancel(t.Context(), testBank, ops[0].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.RunGraphJob(t.Context(), jobs.Inline(job)); err != nil {
		t.Fatal(err)
	}
	if ops := pendingGraphJobs(t, db); len(ops) != 0 {
		t.Fatalf("drained with empty queues: re-enqueued %+v", ops)
	}
	dirtyNeighbour(t, db) // queues fresh graph work, as a removal that joined the running job would
	if err := g.continueGraph(t.Context(), job, true); err != nil {
		t.Fatal(err)
	}
	if ops := pendingGraphJobs(t, db); len(ops) != 1 {
		t.Fatalf("done with queued rows: %d pending, want 1", len(ops))
	}
}
