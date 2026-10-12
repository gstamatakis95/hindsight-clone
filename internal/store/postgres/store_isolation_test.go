package postgres_test

import (
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"slices"
	"testing"
)

// isolationCases check that a bank shares nothing with another bank of the same bank id: the reads and searches
// (testTenantIsolation), then the write ports UnitRepo.SetDocumentTags, EntityRepo.PendingMaintenance and
// PruneOrphans, UnitRepo.DirtyLinks and ClearDirtyLinks, and DirectiveRepo. Each case writes the same ids or names
// into bankA and the other bank, acts on one and checks the other is untouched. Every case runs twice: against the
// same bank id under another tenant (bankOtherTenant) and under another org of the same tenant (bankOtherOrg).
func isolationCases() []storeCase {
	var out []storeCase
	for _, scope := range []struct {
		name  string
		other domain.BankRef
	}{{"Tenant", bankOtherTenant}, {"Org", bankOtherOrg}} {
		for _, c := range []struct {
			name string
			run  func(*testing.T, store.DB, domain.BankRef)
		}{
			{"Isolation", testTenantIsolation},
			{"IsolationSetDocumentTags", testGapSetDocumentTags},
			{"IsolationDirtyClearDirty", testGapDirty},
			{"IsolationEntityMaintenance", testGapEntityMaintenance},
			{"IsolationDirectives", testGapDirectives},
		} {
			other, run := scope.other, c.run
			out = append(out, storeCase{scope.name + c.name, func(t *testing.T, s store.DB) { run(t, s, other) }})
		}
	}
	return out
}

// testGapSetDocumentTags: SetDocumentTags on bankA's doc-1 retags only bankA's units, never the units of the
// other bank's doc-1.
func testGapSetDocumentTags(t *testing.T, s store.DB, other domain.BankRef) {
	gapBanks := []domain.BankRef{bankA, other}
	ensureBanks(t, s, gapBanks...)
	units := map[domain.BankRef]domain.Unit{}
	seqs := map[domain.BankRef]int64{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		u := fact(b, "u")
		u.DocumentID, u.Tags = "doc-1", []string{"old"}
		seqs[b] = insertUnits(t, s, u)
		units[b] = u
	}
	tags := []string{"new"}
	var ids []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		seq, err := tx.Seq(t.Context(), bankA)
		if err != nil {
			return err
		}
		ids, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", []string{"old"}, tags, seq)
		return err
	})
	wantSet(t, "SetDocumentTags(bank A) ids", ids, []domain.UnitID{units[bankA].ID})
	if got := getUnits(t, s, bankA, units[bankA].ID); len(got) != 1 || !slices.Equal(got[0].Tags, tags) {
		t.Errorf("bank A unit after SetDocumentTags = %+v, want tags %v", got, tags)
	}
	theirs := units[other]
	got := getUnits(t, s, other, theirs.ID)
	if len(got) != 1 || !slices.Equal(got[0].Tags, []string{"old"}) || got[0].Seq != seqs[other] {
		t.Errorf("other bank's unit after bank A's SetDocumentTags = %+v, want tags [old] seq %d", got,
			seqs[other])
	}
}

// gapDirty returns up to limit of bank b's units queued in graph_dirty.
func gapDirty(t *testing.T, s store.DB, b domain.BankRef, limit int) []domain.UnitID {
	t.Helper()
	var ids []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ids, err = tx.Units().DirtyLinks(t.Context(), b, limit)
		return err
	})
	return ids
}

// gapClearDirty removes ids from bank b's graph_dirty.
func gapClearDirty(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Units().ClearDirtyLinks(t.Context(), b, ids) })
}

// gapDeleteDocUnits runs DeleteByDocument on bank b's doc in a transaction that takes the bank's Seq.
func gapDeleteDocUnits(t *testing.T, s store.DB, b domain.BankRef, doc domain.DocumentID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		_, err := tx.Units().DeleteByDocument(t.Context(), b, doc)
		return err
	})
}

// gapNeighbours writes, into each gap bank, a unit x of doc-1 linked to a unit n, and returns the n units.
func gapNeighbours(t *testing.T, s store.DB, gapBanks []domain.BankRef) map[domain.BankRef]domain.UnitID {
	t.Helper()
	out := map[domain.BankRef]domain.UnitID{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		x, n := fact(b, "x"), fact(b, "n")
		x.DocumentID = "doc-1"
		insertUnits(t, s, x, n)
		insertLinks(t, s, link(b, x.ID, n.ID, graph.KindSemantic, 0.9))
		out[b] = n.ID
	}
	return out
}

// testGapDirty: Dirty returns only the bank's own queued units, and ClearDirty with another bank's ids leaves
// that tenant's queue alone.
func testGapDirty(t *testing.T, s store.DB, other domain.BankRef) {
	gapBanks := []domain.BankRef{bankA, other}
	ensureBanks(t, s, gapBanks...)
	ns := gapNeighbours(t, s, gapBanks)
	for _, b := range gapBanks {
		gapClearDirty(t, s, b, ns[bankA], ns[other]) // a baseline whatever Insert queued
		gapDeleteDocUnits(t, s, b, "doc-1")
	}
	mine, theirs := ns[bankA], ns[other]
	wantSet(t, "Dirty(bank A)", gapDirty(t, s, bankA, 10), []domain.UnitID{mine})
	wantSet(t, "Dirty(other bank)", gapDirty(t, s, other, 10), []domain.UnitID{theirs})
	gapClearDirty(t, s, bankA, theirs)
	wantSet(t, "Dirty(other bank) after bank A's ClearDirty of its id", gapDirty(t, s, other, 10),
		[]domain.UnitID{theirs})
	gapClearDirty(t, s, bankA, mine)
	wantSet(t, "Dirty(bank A) after ClearDirty", gapDirty(t, s, bankA, 10), nil)
	wantSet(t, "Dirty(other bank) after bank A's ClearDirty", gapDirty(t, s, other, 10),
		[]domain.UnitID{theirs})
}

// gapPending returns up to limit of bank b's entities queued in entity_maintenance.
func gapPending(t *testing.T, s store.DB, b domain.BankRef, limit int) []domain.EntityID {
	t.Helper()
	var ids []domain.EntityID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ids, err = tx.Entities().PendingMaintenance(t.Context(), b, limit)
		return err
	})
	return ids
}

// gapPrune runs PruneOrphans on bank b and returns how many entities it deleted.
func gapPrune(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.EntityID) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		n, err = tx.Entities().PruneOrphans(t.Context(), b, ids)
		return err
	})
	return n
}

// gapOrphans writes, into each gap bank, an entity with the same name attached only to a unit of doc-1, then
// deletes doc-1's units so the entity is an orphan queued in entity_maintenance. It returns the entities.
func gapOrphans(t *testing.T, s store.DB, gapBanks []domain.BankRef) map[domain.BankRef]domain.EntityID {
	t.Helper()
	out := map[domain.BankRef]domain.EntityID{}
	for _, b := range gapBanks {
		upsertDocs(t, s, b, "doc-1")
		u := fact(b, "u")
		u.DocumentID = "doc-1"
		insertUnits(t, s, u)
		e := upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Zanzibar Expedition", Kind: "event"})
		inTx(t, s, func(tx store.Tx) error { return tx.Entities().Attach(t.Context(), u.ID, []domain.EntityID{e}) })
		if n := gapPrune(t, s, b, e); n != 0 { // a baseline whatever Attach queued
			t.Errorf("PruneOrphans of a referenced entity deleted %d, want 0", n)
		}
		gapDeleteDocUnits(t, s, b, "doc-1")
		out[b] = e
	}
	return out
}

// testGapEntityMaintenance: PendingMaintenance returns only the bank's own queued entities, and PruneOrphans
// with another bank's ids neither deletes that tenant's orphan nor clears its queue.
func testGapEntityMaintenance(t *testing.T, s store.DB, other domain.BankRef) {
	gapBanks := []domain.BankRef{bankA, other}
	ensureBanks(t, s, gapBanks...)
	es := gapOrphans(t, s, gapBanks)
	mine, theirs := es[bankA], es[other]
	wantSet(t, "PendingMaintenance(bank A)", gapPending(t, s, bankA, 10), []domain.EntityID{mine})
	wantSet(t, "PendingMaintenance(other bank)", gapPending(t, s, other, 10), []domain.EntityID{theirs})
	if n := gapPrune(t, s, bankA, theirs); n != 0 {
		t.Errorf("PruneOrphans(bank A, other bank's orphan) deleted %d, want 0", n)
	}
	wantSet(t, "PendingMaintenance(other bank) after bank A's PruneOrphans of its id",
		gapPending(t, s, other, 10), []domain.EntityID{theirs})
	if n := gapPrune(t, s, bankA, mine); n != 1 {
		t.Errorf("PruneOrphans(bank A, own orphan) deleted %d, want 1", n)
	}
	wantSet(t, "PendingMaintenance(other bank) after bank A's PruneOrphans",
		gapPending(t, s, other, 10), []domain.EntityID{theirs})
	cs := candidates(t, s, other, "Zanzibar Expedition", 5)
	if len(cs) != 1 || cs[0].Entity.ID != theirs {
		t.Errorf("Candidates(other bank) after bank A's PruneOrphans = %+v, want its own entity %s", cs, theirs)
	}
	if cs := candidates(t, s, bankA, "Zanzibar Expedition", 5); len(cs) != 0 {
		t.Errorf("Candidates(bank A) after PruneOrphans = %+v, want none", cs)
	}
}

// testGapDirectives: the same directive id in two banks is two directives; Upsert and Delete in one
// tenant leave the other's alone, and Delete of an id only the other bank has is ErrNotFound.
func testGapDirectives(t *testing.T, s store.DB, other domain.BankRef) {
	gapBanks := []domain.BankRef{bankA, other}
	ensureBanks(t, s, gapBanks...)
	mine := domain.Directive{ID: "dir-1", Bank: bankA, Name: "mine", Rules: []string{"be brief"}, Active: true}
	theirs := domain.Directive{ID: "dir-1", Bank: other, Name: "theirs", Rules: []string{"x"}, Active: true}
	only := domain.Directive{ID: "dir-only-a", Bank: bankA, Name: "only", Rules: []string{"y"}, Active: true}
	for _, d := range []domain.Directive{mine, theirs, only} {
		if id := createDirective(t, s, d); id != d.ID {
			t.Errorf("Create(%s in %v) returned %s", d.ID, d.Bank, id)
		}
	}
	wantTheirs := func(step string) {
		t.Helper()
		got := listDirectives(t, s, other, false)
		wantSeq(t, step+": List(other bank)", directiveIDs(got), []domain.DirectiveID{theirs.ID})
		if len(got) == 1 {
			compareDirective(t, got[0], theirs)
		}
	}
	wantTheirs("after Create")
	wantSeq(t, "List(bank A)", directiveIDs(listDirectives(t, s, bankA, false)), []domain.DirectiveID{mine.ID, only.ID})
	mine.Name, mine.Active = "mine v2", false
	updateDirective(t, s, mine)
	wantTheirs("after bank A's Update")
	err := txErr(t, s, func(tx store.Tx) error { return tx.Directives().Delete(t.Context(), other, only.ID) })
	wantErrIs(t, "Delete(other bank, id only bank A has)", err, domain.ErrNotFound)
	wantSeq(t, "List(bank A) after the other bank's failed Delete",
		directiveIDs(listDirectives(t, s, bankA, false)), []domain.DirectiveID{mine.ID, only.ID})
	inTx(t, s, func(tx store.Tx) error { return tx.Directives().Delete(t.Context(), bankA, mine.ID) })
	wantTheirs("after bank A's Delete")
	wantSeq(t, "List(bank A, active) after Delete", directiveIDs(listDirectives(t, s, bankA, true)),
		[]domain.DirectiveID{only.ID})
}
