package maintenance_test

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/engine/maintenance"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"sync"
	"testing"
	"time"
)

var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// stubLinker records the units it is asked to top up; advance moves the clock on its first call.
type stubLinker struct {
	mu      sync.Mutex
	clock   *clock.Manual
	advance time.Duration
	calls   [][]domain.UnitID
}

func (l *stubLinker) Link(
	_ context.Context, _ store.Tx, _ domain.BankRef, units []domain.Unit, _ map[domain.UnitID][]domain.UnitID,
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
		var links []domain.Link
		for i := range n {
			id := pgtest.ID[domain.UnitID](fmt.Sprintf("n%03d", i))
			units = append(units, domain.Unit{
				ID: id, Bank: testBank, DocumentID: "rest", Type: domain.FactTypeWorld,
				Seq: seq,
			})
			links = append(links, domain.Link{Bank: testBank, From: id, To: hub, Kind: domain.LinkTemporal, Weight: 1})
		}
		if err := tx.Units().Insert(ctx, units); err != nil {
			return err
		}
		if err := tx.Links().Insert(ctx, links); err != nil {
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
		if dirty, err = tx.Links().Dirty(ctx, testBank, 1000); err != nil {
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
	seedHub(t, db, 3)
	if d, p := queues(t, db); len(d) != 3 || len(p) != 2 {
		t.Fatalf("seeded queues: %d dirty, %d entities; want 3 and 2", len(d), len(p))
	}
	l := &stubLinker{clock: clock}
	g := maintenance.NewGraphMaintainer(db, l, clock)

	done, err := g.Run(ctx, testBank, maintenance.DefaultGraphBudget)
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
	if done, err := g.Run(ctx, testBank, maintenance.DefaultGraphBudget); err != nil || !done || len(l.calls) != 1 {
		t.Fatalf("idle rerun = %v, %v with %d linker calls; want done and no new call", done, err, len(l.calls))
	}
}

func TestGraphMaintenanceResumesAfterBudget(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
	n := maintenance.DirtyBatch + 50
	seedHub(t, db, n)
	// The first batch "takes" longer than the whole budget.
	l := &stubLinker{clock: clock, advance: maintenance.DefaultGraphBudget + time.Second}
	g := maintenance.NewGraphMaintainer(db, l, clock)

	done, err := g.Run(ctx, testBank, maintenance.DefaultGraphBudget)
	if err != nil || done {
		t.Fatalf("first Run = %v, %v; want stopped by the budget (done=false)", done, err)
	}
	if d, _ := queues(t, db); len(d) != n-maintenance.DirtyBatch {
		t.Fatalf("after the first run %d units left dirty, want %d", len(d), n-maintenance.DirtyBatch)
	}

	done, err = g.Run(ctx, testBank, maintenance.DefaultGraphBudget)
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
	seedHub(t, db, 2)
	g := maintenance.NewGraphMaintainer(db, &stubLinker{clock: clock}, clock)

	if err := db.InTx(ctx, func(tx store.Tx) error {
		return tx.Banks().MarkDeleting(ctx, testBank, clock.Now())
	}); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := g.Run(ctx, testBank, maintenance.DefaultGraphBudget); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Run on a deleting bank: %v, want ErrCancelled", err)
	}
	if d, _ := queues(t, db); len(d) != 2 {
		t.Fatalf("a run on a deleting bank touched graph_dirty: %d left", len(d))
	}
	gone := domain.BankRef{Tenant: "acme", Bank: "gone"}
	if _, err := g.Run(ctx, gone, maintenance.DefaultGraphBudget); !errors.Is(err, domain.ErrCancelled) {
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
	ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes map[domain.UnitID][]domain.UnitID,
) error {
	if len(l.calls) == 0 {
		if err := tx.Units().Archive(ctx, b, l.victim, "test"); err != nil {
			return err
		}
	}
	return l.stubLinker.Link(ctx, tx, b, units, causes)
}

// TestGraphMaintenanceRechecksAfterFinalBatch: work queued during the last working batch (here inside it)
// is drained before Run reports done.
func TestGraphMaintenanceRechecksAfterFinalBatch(t *testing.T) {
	ctx := context.Background()
	clock := clock.NewManual(time.Time{})
	db := pgtest.NewStore(t)
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
		return tx.Links().Insert(ctx, []domain.Link{
			{Bank: testBank, From: z, To: w, Kind: domain.LinkTemporal, Weight: 1},
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	l := &archivingLinker{stubLinker: stubLinker{clock: clock}, victim: w}
	g := maintenance.NewGraphMaintainer(db, l, clock)

	done, err := g.Run(ctx, testBank, maintenance.DefaultGraphBudget)
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
