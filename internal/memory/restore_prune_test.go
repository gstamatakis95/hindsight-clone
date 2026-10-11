package memory

import (
	"memd/internal/store"
	"testing"
)

// TestRestoreAfterEntityPruneKeepsEntities runs graph maintenance's entity prune while f1 is archived (its
// only entity, Alice, becomes an orphan and is deleted), then restores f1: the restore's transaction
// re-creates Alice from the archive snapshot's names.
func TestRestoreAfterEntityPruneKeepsEntities(t *testing.T) {
	e := newCurateEnv(t)
	s := e.service(nil)
	e.invalidate(s)
	e.inTx(func(tx store.Tx) error {
		ids, err := tx.Entities().PendingMaintenance(e.ctx, testBank, 100)
		if err != nil {
			return err
		}
		n, err := tx.Entities().PruneOrphans(e.ctx, testBank, ids)
		if err == nil && n != 1 {
			t.Errorf("pruned %d entities, want 1 (Alice)", n)
		}
		return err
	})
	if _, _, err := s.RestoreMemory(e.ctx, RestoreRequest{Bank: testBank, ID: idF1}); err != nil {
		t.Fatal(err)
	}
	e.rederiveEdited([]string{"Alice"}) // re-created in the restore's own transaction
}
