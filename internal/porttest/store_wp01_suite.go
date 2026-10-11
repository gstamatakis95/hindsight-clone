package porttest

import (
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
)

// RunStoreGapsSuite runs the store contract cases added in WP-01: tenant isolation of the write ports added in
// the WP-00 review rounds (UnitRepo.SetDocumentTags, EntityRepo.PendingMaintenance and PruneOrphans,
// LinkRepo.Dirty and ClearDirty, DirectiveRepo). Each case writes the same ids or names into bankA and
// bankOtherTenant (bankA's bank id under another tenant), acts on one and checks the other is untouched.
// BankRepo.DeleteBatch and EventRepo.List are already covered by RunStoreSuite (DeleteBatchCoversAllTables,
// TenantIsolation). Every port.Store implementation runs it next to RunStoreSuite; WP-02's in-memory fake
// must run it too. newStore is called once per subtest and must return a store over an empty database.
func RunStoreGapsSuite(t *testing.T, newStore StoreFactory) {
	t.Helper()
	runStoreCases(t, newStore, []storeCase{
		{"TenantIsolationSetDocumentTags", testGapSetDocumentTags},
		{"TenantIsolationDirtyClearDirty", testGapDirty},
		{"TenantIsolationEntityMaintenance", testGapEntityMaintenance},
		{"TenantIsolationDirectives", testGapDirectives},
	})
}

// gapBanks are the two banks every gap case writes to: the same bank id under two tenants.
var gapBanks = []port.BankRef{bankA, bankOtherTenant}

// testGapSetDocumentTags: SetDocumentTags on bankA's doc-1 retags only bankA's units, never the units of the
// other tenant's doc-1.
func testGapSetDocumentTags(t *testing.T, s port.Store) {
	ensureBanks(t, s, gapBanks...)
	units := map[port.BankRef]domain.Unit{}
	seqs := map[port.BankRef]int64{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		u := fact(b, "u")
		u.DocumentID, u.Tags = "doc-1", []string{"old"}
		seqs[b] = insertUnits(t, s, u)
		units[b] = u
	}
	tags := []string{"new"}
	var ids []port.UnitID
	inTx(t, s, func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), bankA)
		if err != nil {
			return err
		}
		ids, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", []string{"old"}, tags, seq)
		return err
	})
	wantSet(t, "SetDocumentTags(bank A) ids", ids, []port.UnitID{units[bankA].ID})
	if got := getUnits(t, s, bankA, units[bankA].ID); len(got) != 1 || !slices.Equal(got[0].Tags, tags) {
		t.Errorf("bank A unit after SetDocumentTags = %+v, want tags %v", got, tags)
	}
	theirs := units[bankOtherTenant]
	got := getUnits(t, s, bankOtherTenant, theirs.ID)
	if len(got) != 1 || !slices.Equal(got[0].Tags, []string{"old"}) || got[0].Seq != seqs[bankOtherTenant] {
		t.Errorf("other tenant's unit after bank A's SetDocumentTags = %+v, want tags [old] seq %d", got,
			seqs[bankOtherTenant])
	}
}

// gapDirty returns up to limit of bank b's units queued in graph_dirty.
func gapDirty(t *testing.T, s port.Store, b port.BankRef, limit int) []port.UnitID {
	t.Helper()
	var ids []port.UnitID
	inTx(t, s, func(tx port.Tx) error {
		var err error
		ids, err = tx.Links().Dirty(t.Context(), b, limit)
		return err
	})
	return ids
}

// gapClearDirty removes ids from bank b's graph_dirty.
func gapClearDirty(t *testing.T, s port.Store, b port.BankRef, ids ...port.UnitID) {
	t.Helper()
	inTx(t, s, func(tx port.Tx) error { return tx.Links().ClearDirty(t.Context(), b, ids) })
}

// gapDeleteDocUnits runs DeleteByDocument on bank b's doc in a transaction that takes the bank's Seq.
func gapDeleteDocUnits(t *testing.T, s port.Store, b port.BankRef, doc port.DocumentID) {
	t.Helper()
	inTx(t, s, func(tx port.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		_, err := tx.Units().DeleteByDocument(t.Context(), b, doc)
		return err
	})
}

// gapNeighbours writes, into each gap bank, a unit x of doc-1 linked to a unit n, and returns the n units.
func gapNeighbours(t *testing.T, s port.Store) map[port.BankRef]port.UnitID {
	t.Helper()
	out := map[port.BankRef]port.UnitID{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		x, n := fact(b, "x"), fact(b, "n")
		x.DocumentID = "doc-1"
		insertUnits(t, s, x, n)
		insertLinks(t, s, link(b, x.ID, n.ID, domain.LinkSemantic, 0.9))
		out[b] = n.ID
	}
	return out
}

// testGapDirty: Dirty returns only the bank's own queued units, and ClearDirty with another tenant's ids leaves
// that tenant's queue alone.
func testGapDirty(t *testing.T, s port.Store) {
	ensureBanks(t, s, gapBanks...)
	ns := gapNeighbours(t, s)
	for _, b := range gapBanks {
		gapClearDirty(t, s, b, ns[bankA], ns[bankOtherTenant]) // a baseline whatever Insert queued
		gapDeleteDocUnits(t, s, b, "doc-1")
	}
	mine, theirs := ns[bankA], ns[bankOtherTenant]
	wantSet(t, "Dirty(bank A)", gapDirty(t, s, bankA, 10), []port.UnitID{mine})
	wantSet(t, "Dirty(other tenant)", gapDirty(t, s, bankOtherTenant, 10), []port.UnitID{theirs})
	gapClearDirty(t, s, bankA, theirs)
	wantSet(t, "Dirty(other tenant) after bank A's ClearDirty of its id", gapDirty(t, s, bankOtherTenant, 10),
		[]port.UnitID{theirs})
	gapClearDirty(t, s, bankA, mine)
	wantSet(t, "Dirty(bank A) after ClearDirty", gapDirty(t, s, bankA, 10), nil)
	wantSet(t, "Dirty(other tenant) after bank A's ClearDirty", gapDirty(t, s, bankOtherTenant, 10),
		[]port.UnitID{theirs})
}

// gapPending returns up to limit of bank b's entities queued in entity_maintenance.
func gapPending(t *testing.T, s port.Store, b port.BankRef, limit int) []port.EntityID {
	t.Helper()
	var ids []port.EntityID
	inTx(t, s, func(tx port.Tx) error {
		var err error
		ids, err = tx.Entities().PendingMaintenance(t.Context(), b, limit)
		return err
	})
	return ids
}

// gapPrune runs PruneOrphans on bank b and returns how many entities it deleted.
func gapPrune(t *testing.T, s port.Store, b port.BankRef, ids ...port.EntityID) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx port.Tx) error {
		var err error
		n, err = tx.Entities().PruneOrphans(t.Context(), b, ids)
		return err
	})
	return n
}

// gapOrphans writes, into each gap bank, an entity with the same name attached only to a unit of doc-1, then
// deletes doc-1's units so the entity is an orphan queued in entity_maintenance. It returns the entities.
func gapOrphans(t *testing.T, s port.Store) map[port.BankRef]port.EntityID {
	t.Helper()
	out := map[port.BankRef]port.EntityID{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		u := fact(b, "u")
		u.DocumentID = "doc-1"
		insertUnits(t, s, u)
		e := upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Zanzibar Expedition", Kind: "event"})
		inTx(t, s, func(tx port.Tx) error { return tx.Entities().Attach(t.Context(), u.ID, []port.EntityID{e}) })
		if n := gapPrune(t, s, b, e); n != 0 { // a baseline whatever Attach queued
			t.Errorf("PruneOrphans of a referenced entity deleted %d, want 0", n)
		}
		gapDeleteDocUnits(t, s, b, "doc-1")
		out[b] = e
	}
	return out
}

// testGapEntityMaintenance: PendingMaintenance returns only the bank's own queued entities, and PruneOrphans
// with another tenant's ids neither deletes that tenant's orphan nor clears its queue.
func testGapEntityMaintenance(t *testing.T, s port.Store) {
	ensureBanks(t, s, gapBanks...)
	es := gapOrphans(t, s)
	mine, theirs := es[bankA], es[bankOtherTenant]
	wantSet(t, "PendingMaintenance(bank A)", gapPending(t, s, bankA, 10), []port.EntityID{mine})
	wantSet(t, "PendingMaintenance(other tenant)", gapPending(t, s, bankOtherTenant, 10), []port.EntityID{theirs})
	if n := gapPrune(t, s, bankA, theirs); n != 0 {
		t.Errorf("PruneOrphans(bank A, other tenant's orphan) deleted %d, want 0", n)
	}
	wantSet(t, "PendingMaintenance(other tenant) after bank A's PruneOrphans of its id",
		gapPending(t, s, bankOtherTenant, 10), []port.EntityID{theirs})
	if n := gapPrune(t, s, bankA, mine); n != 1 {
		t.Errorf("PruneOrphans(bank A, own orphan) deleted %d, want 1", n)
	}
	wantSet(t, "PendingMaintenance(other tenant) after bank A's PruneOrphans",
		gapPending(t, s, bankOtherTenant, 10), []port.EntityID{theirs})
	cs := candidates(t, s, bankOtherTenant, "Zanzibar Expedition", 5)
	if len(cs) != 1 || cs[0].Entity.ID != theirs {
		t.Errorf("Candidates(other tenant) after bank A's PruneOrphans = %+v, want its own entity %s", cs, theirs)
	}
	if cs := candidates(t, s, bankA, "Zanzibar Expedition", 5); len(cs) != 0 {
		t.Errorf("Candidates(bank A) after PruneOrphans = %+v, want none", cs)
	}
}

// testGapDirectives: the same directive id under two tenants is two directives; Upsert and Delete in one
// tenant leave the other's alone, and Delete of an id only the other tenant has is ErrNotFound.
func testGapDirectives(t *testing.T, s port.Store) {
	ensureBanks(t, s, gapBanks...)
	mine := domain.Directive{ID: "dir-1", Bank: bankA, Name: "mine", Rules: []string{"be brief"}, Active: true}
	theirs := domain.Directive{ID: "dir-1", Bank: bankOtherTenant, Name: "theirs", Rules: []string{"x"}, Active: true}
	only := domain.Directive{ID: "dir-only-a", Bank: bankA, Name: "only", Rules: []string{"y"}, Active: true}
	for _, d := range []domain.Directive{mine, theirs, only} {
		if id := upsertDirective(t, s, d); id != d.ID {
			t.Errorf("Upsert(%s in %v) returned %s", d.ID, d.Bank, id)
		}
	}
	wantTheirs := func(step string) {
		t.Helper()
		got := listDirectives(t, s, bankOtherTenant, false)
		wantSeq(t, step+": List(other tenant)", directiveIDs(got), []port.DirectiveID{theirs.ID})
		if len(got) == 1 {
			compareDirective(t, got[0], theirs)
		}
	}
	wantTheirs("after Upsert")
	wantSeq(t, "List(bank A)", directiveIDs(listDirectives(t, s, bankA, false)), []port.DirectiveID{mine.ID, only.ID})
	mine.Name, mine.Active = "mine v2", false
	upsertDirective(t, s, mine)
	wantTheirs("after bank A's replace")
	err := txErr(t, s, func(tx port.Tx) error { return tx.Directives().Delete(t.Context(), bankOtherTenant, only.ID) })
	wantErrIs(t, "Delete(other tenant, id only bank A has)", err, domain.ErrNotFound)
	wantSeq(t, "List(bank A) after the other tenant's failed Delete",
		directiveIDs(listDirectives(t, s, bankA, false)), []port.DirectiveID{mine.ID, only.ID})
	inTx(t, s, func(tx port.Tx) error { return tx.Directives().Delete(t.Context(), bankA, mine.ID) })
	wantTheirs("after bank A's Delete")
	wantSeq(t, "List(bank A, active) after Delete", directiveIDs(listDirectives(t, s, bankA, true)),
		[]port.DirectiveID{only.ID})
}
