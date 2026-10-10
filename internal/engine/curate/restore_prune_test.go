package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

// archivedStub is a UnitGetter returning a fixed archived snapshot.
type archivedStub struct{ u domain.Unit }

// GetUnit returns the snapshot for its id, else domain.ErrNotFound.
func (s archivedStub) GetUnit(_ context.Context, _ port.BankRef, id port.UnitID) (domain.Unit, error) {
	if id != s.u.ID {
		return domain.Unit{}, domain.ErrNotFound
	}
	return s.u, nil
}

// TestRestoreAfterEntityPruneKeepsEntities runs graph maintenance's entity prune while f1 is archived (its
// only entity, Alice, becomes an orphan and is deleted), then restores f1: the restore's transaction
// re-creates Alice from the archive snapshot's names.
func TestRestoreAfterEntityPruneKeepsEntities(t *testing.T) {
	e := newEnv(t)
	e.invalidate(e.curator())
	e.inTx(func(tx port.Tx) error {
		ids, err := tx.Entities().PendingMaintenance(e.ctx, testBank, 0)
		if err != nil {
			return err
		}
		n, err := tx.Entities().PruneOrphans(e.ctx, testBank, ids)
		if err == nil && n != 1 {
			t.Errorf("pruned %d entities, want 1 (Alice)", n)
		}
		return err
	})
	c := NewCurator(CuratorDeps{
		Store: e.store, Clock: e.clock, Citing: e.citing, Refresh: refreshStub{}, Resolver: resolverStub{},
		Config: configStub{},
		Archived: archivedStub{u: domain.Unit{
			ID: "f1", Bank: testBank, State: domain.MemoryStateInvalidated, Entities: []string{"Alice"},
		}},
	})
	req := UpdateMemoryRequest{Bank: testBank, ID: "f1", State: domain.MemoryStateValid}
	if _, _, err := c.Update(e.ctx, req); err != nil {
		t.Fatal(err)
	}
	e.rederiveEdited([]string{"Alice"}) // re-created in the restore's own transaction
}
