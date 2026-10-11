package postgres_test

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
	"time"
)

// curationCases are RunStoreSuite's subtests of curation and maintenance writes: unit edits and retags,
// directives, graph and entity maintenance queues, batched bank deletion and event pruning.
func curationCases() []storeCase {
	return []storeCase{
		{"UnitsUpdate", testUnitUpdate},
		{"UnitsSetDocumentTags", testSetDocumentTags},
		{"UnitsSetDocumentTagsKeepsExtraTags", testSetDocumentTagsKeepsExtraTags},
		{"UpdateAndRetagResetConsolidation", testResetConsolidation},
		{"DirectivesCreateUpdateListDelete", testDirectives},
		{"LinksCountDirtyClearDirty", testLinksDirty},
		{"EntitiesPendingMaintenancePruneOrphans", testEntityMaintenance},
		{"RederiveDetachAndDeleteLinks", testRederiveDetach},
		{"BanksDeleteBatch", testDeleteBatch},
		{"DeleteRefusesLiveBank", testDeleteRefusesLive},
		{"DeleteBatchCoversAllTables", testDeleteBatchAllTables},
		{"EventsPrune", testEventsPrune},
	}
}

// ---------- UnitRepo curation ----------

// testUnitUpdate: Update replaces a fact's curated fields; a nil Embedding takes it out of SearchVector but
// not GetUnits. AppendAudit is accepted in the same transaction. Unknown or archived ids are ErrNotFound.
func testUnitUpdate(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	u, other := fact(bankA, "Alice lives in Paris"), fact(bankA, "Bob lives in Rome")
	u.Embedding, other.Embedding, u.Context = angleVec(0), angleVec(5), "chat"
	insertUnits(t, s, u, other)
	edited := u
	edited.Text, edited.Context, edited.Type = "Alice lives in Lisbon", "edited", domain.FactTypeExperience
	edited.OccurredStart, edited.OccurredEnd = ptr(day(3)), ptr(day(4))
	edited.EditedAt, edited.Embedding = ptr(day(5)), nil
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if edited.Seq, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		if err := tx.Units().Update(t.Context(), edited); err != nil {
			return err
		}
		return tx.Units().AppendAudit(t.Context(), domain.AuditEntry{
			Bank: bankA, UnitID: u.ID, Action: domain.CurationEdit, Reason: "moved",
			Before: json.RawMessage(`{"text":"Alice lives in Paris"}`),
			After:  json.RawMessage(`{"text":"Alice lives in Lisbon"}`), At: day(5),
		})
	})
	got := getUnits(t, s, bankA, u.ID)
	if len(got) != 1 {
		t.Fatalf("GetUnits after Update = %d units, want 1", len(got))
	}
	compareUnit(t, got[0], edited)
	if !sameTimePtr(got[0].EditedAt, edited.EditedAt) {
		t.Errorf("edited_at after Update = %v, want %v", got[0].EditedAt, edited.EditedAt)
	}
	hs := searchVector(t, s, store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10})
	wantSeq(t, "SearchVector after Update with a nil embedding", hitIDs(hs), []domain.UnitID{other.ID})
	missing := fact(bankA, "never inserted")
	err := txErr(t, s, func(tx store.Tx) error { return tx.Units().Update(t.Context(), missing) })
	wantErrIs(t, "Update(unknown)", err, domain.ErrNotFound)
	archive(t, s, other.ID)
	err = txErr(t, s, func(tx store.Tx) error { return tx.Units().Update(t.Context(), other) })
	wantErrIs(t, "Update(archived)", err, domain.ErrNotFound)
}

// testSetDocumentTags: SetDocumentTags rewrites the tags and seq of the document's units only and returns
// their ids.
func testSetDocumentTags(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	u1, u2, u3 := fact(bankA, "u1"), fact(bankA, "u2"), fact(bankA, "u3")
	u1.DocumentID, u2.DocumentID, u3.DocumentID = "doc-1", "doc-1", "doc-2"
	u1.Tags, u2.Tags, u3.Tags = []string{"old"}, []string{"old"}, []string{"old"}
	oldSeq := insertUnits(t, s, u1, u2, u3)
	tags := domain.NormalizeTags([]string{"new", "x"})
	var (
		seq int64
		ids []domain.UnitID
	)
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if seq, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		ids, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", []string{"old"}, tags, seq)
		return err
	})
	wantSet(t, "SetDocumentTags ids", ids, []domain.UnitID{u1.ID, u2.ID})
	got := getUnits(t, s, bankA, u1.ID, u2.ID, u3.ID)
	if len(got) != 3 {
		t.Fatalf("GetUnits after SetDocumentTags = %d units, want 3", len(got))
	}
	for i, want := range []struct {
		tags []string
		seq  int64
	}{{tags, seq}, {tags, seq}, {[]string{"old"}, oldSeq}} {
		if !slices.Equal(got[i].Tags, want.tags) || got[i].Seq != want.seq {
			t.Errorf("unit %d after SetDocumentTags = tags %v seq %d, want %v %d", i, got[i].Tags, got[i].Seq,
				want.tags, want.seq)
		}
	}
}

// testSetDocumentTagsKeepsExtraTags: SetDocumentTags replaces only the document-derived tags.
// A unit with doc tags [a] plus label tags retagged [a]→[b] ends with [b] plus the same label tags, live and
// archived, in domain.NormalizeTags (byte) order; the live unit's seq is stamped and both consolidation
// stamps are cleared; the other tenant's doc-1 is untouched; unnormalized old tags are INVALID_ARGUMENT.
func testSetDocumentTagsKeepsExtraTags(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	upsertDocs(t, s, bankA, "doc-1")
	upsertDocs(t, s, bankOtherTenant, "doc-1")
	labels := []string{"topic:x", "topic-y", "topic_z", "a.b"} // punctuation sorts differently outside "C"
	before := domain.NormalizeTags(append([]string{"a"}, labels...))
	live, arch, theirs := embeddedFact(bankA, "live"), embeddedFact(bankA, "archived"), fact(bankOtherTenant, "o")
	for _, u := range []*domain.Unit{&live, &arch, &theirs} {
		u.DocumentID, u.Tags = "doc-1", before
	}
	insertUnits(t, s, live, arch)
	theirSeq := insertUnits(t, s, theirs)
	archive(t, s, arch.ID)
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{live.ID}, day(3)); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), []domain.UnitID{live.ID}, day(3))
	})
	var (
		seq int64
		ids []domain.UnitID
	)
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if seq, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		ids, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", []string{"a"}, []string{"b"}, seq)
		return err
	})
	wantSet(t, "SetDocumentTags ids (live only)", ids, []domain.UnitID{live.ID})
	want := domain.NormalizeTags(append([]string{"b"}, labels...))
	if got := getUnits(t, s, bankA, live.ID); len(got) != 1 || !slices.Equal(got[0].Tags, want) || got[0].Seq != seq {
		t.Errorf("live unit after retag [a]→[b] = %+v, want tags %v seq %d", got, want, seq)
	}
	wantSeq(t, "Unconsolidated after the retag", unitIDs(unconsolidated(t, s, bankA, nil, 10)),
		[]domain.UnitID{live.ID})
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		return tx.Units().Restore(t.Context(), bankA, arch.ID)
	})
	if got := getUnits(t, s, bankA, arch.ID); len(got) != 1 || !slices.Equal(got[0].Tags, want) {
		t.Errorf("archived unit after retag [a]→[b] and Restore = %+v, want tags %v", got, want)
	}
	if got := getUnits(t, s, bankOtherTenant, theirs.ID); len(got) != 1 || !slices.Equal(got[0].Tags, before) ||
		got[0].Seq != theirSeq {
		t.Errorf("other tenant's unit after bank A's retag = %+v, want tags %v seq %d", got, before, theirSeq)
	}
	err := txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", []string{"B", "a"}, []string{"b"}, seq)
		return err
	})
	wantErrIs(t, "SetDocumentTags(unnormalized old tags)", err, domain.ErrInvalidArgument)
}

// testResetConsolidation: Update and SetDocumentTags clear consolidated_at and consolidation_failed_at, so the
// edited and the retagged fact are back in Unconsolidated.
func testResetConsolidation(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	u, v := embeddedFact(bankA, "edited"), embeddedFact(bankA, "retagged")
	u.CreatedAt, v.CreatedAt, v.DocumentID = day(1), day(2), "doc-1"
	insertUnits(t, s, u, v)
	inTx(t, s, func(tx store.Tx) error { // both stamps on both units, so each reset must clear both
		if err := tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{u.ID, v.ID}, day(3)); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), []domain.UnitID{u.ID, v.ID}, day(3))
	})
	wantSeq(t, "Unconsolidated after the marks", unitIDs(unconsolidated(t, s, bankA, nil, 10)), nil)
	u.Text = "edited again"
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if u.Seq, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		return tx.Units().Update(t.Context(), u)
	})
	wantSeq(t, "Unconsolidated after Update", unitIDs(unconsolidated(t, s, bankA, nil, 10)), []domain.UnitID{u.ID})
	inTx(t, s, func(tx store.Tx) error {
		seq, err := tx.Seq(t.Context(), bankA)
		if err != nil {
			return err
		}
		_, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", nil, []string{"x"}, seq)
		return err
	})
	wantSeq(t, "Unconsolidated after SetDocumentTags", unitIDs(unconsolidated(t, s, bankA, nil, 10)),
		[]domain.UnitID{u.ID, v.ID})
}

// ---------- DirectiveRepo ----------

// createDirective creates d in its own transaction and returns the id.
func createDirective(t *testing.T, s store.DB, d domain.Directive) domain.DirectiveID {
	t.Helper()
	var id domain.DirectiveID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		id, err = tx.Directives().Create(t.Context(), d)
		return err
	})
	return id
}

// updateDirective replaces d (by id) in its own transaction.
func updateDirective(t *testing.T, s store.DB, d domain.Directive) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Directives().Update(t.Context(), d) })
}

// listDirectives lists bank b's directives.
func listDirectives(t *testing.T, s store.DB, b domain.BankRef, activeOnly bool) []domain.Directive {
	t.Helper()
	var ds []domain.Directive
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ds, err = tx.Directives().List(t.Context(), b, activeOnly)
		return err
	})
	return ds
}

// directiveIDs returns the ids of ds in order.
func directiveIDs(ds []domain.Directive) []domain.DirectiveID {
	out := make([]domain.DirectiveID, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

// compareDirective reports differing stored fields of a directive.
func compareDirective(t *testing.T, got, want domain.Directive) {
	t.Helper()
	if got.Bank != want.Bank || got.Name != want.Name || got.Active != want.Active ||
		!slices.Equal(got.Rules, want.Rules) || !slices.Equal(got.Tags, want.Tags) {
		t.Errorf("directive %s = %v %q %v %q %v, want %v %q %v %q %v", want.ID, got.Bank, got.Name, got.Active,
			got.Rules, got.Tags, want.Bank, want.Name, want.Active, want.Rules, want.Tags)
	}
}

// testDirectives: Create assigns ids and refuses a taken name with no SQL text; Update replaces by id, refuses a
// taken name and an unknown id; List is oldest first and filters inactive ones; Delete removes, and deleting
// again is ErrNotFound; banks are isolated.
func testDirectives(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	d1 := domain.Directive{Bank: bankA, Name: "tone", Rules: []string{"be brief", "cite sources"}, Active: true}
	d2 := domain.Directive{Bank: bankA, Name: "legal", Rules: []string{"no advice"}, Tags: []string{"legal"}}
	d1.ID = createDirective(t, s, d1)
	d2.ID = createDirective(t, s, d2)
	if d1.ID == "" || d2.ID == "" || d1.ID == d2.ID {
		t.Fatalf("Create ids = %q, %q; want two distinct ids", d1.ID, d2.ID)
	}
	err := txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Directives().Create(t.Context(), domain.Directive{Bank: bankA, Name: "tone"})
		return err
	})
	wantErrIs(t, "Create(taken name)", err, domain.ErrAlreadyExists)
	if err != nil && strings.Contains(err.Error(), "SQLSTATE") {
		t.Errorf("Create(taken name) leaks driver text: %v", err)
	}
	createDirective(t, s, domain.Directive{Bank: bankB, Name: "tone"}) // names are per bank
	all := listDirectives(t, s, bankA, false)
	wantSeq(t, "List(all)", directiveIDs(all), []domain.DirectiveID{d1.ID, d2.ID})
	if len(all) == 2 {
		compareDirective(t, all[0], d1)
		compareDirective(t, all[1], d2)
	}
	wantSeq(t, "List(active)", directiveIDs(listDirectives(t, s, bankA, true)), []domain.DirectiveID{d1.ID})
	d1.Name, d1.Active = "tone v2", false
	updateDirective(t, s, d1)
	wantSeq(t, "List(active) after deactivating", directiveIDs(listDirectives(t, s, bankA, true)), nil)
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Directives().Update(t.Context(), domain.Directive{ID: d1.ID, Bank: bankA, Name: "legal"})
	})
	wantErrIs(t, "Update(taken name)", err, domain.ErrAlreadyExists)
	err = txErr(t, s, func(tx store.Tx) error {
		return tx.Directives().Update(t.Context(), domain.Directive{ID: "missing", Bank: bankA, Name: "x"})
	})
	wantErrIs(t, "Update(unknown)", err, domain.ErrNotFound)
	inTx(t, s, func(tx store.Tx) error { return tx.Directives().Delete(t.Context(), bankA, d2.ID) })
	all = listDirectives(t, s, bankA, false)
	wantSeq(t, "List(all) after Delete", directiveIDs(all), []domain.DirectiveID{d1.ID})
	if len(all) == 1 {
		compareDirective(t, all[0], d1)
	}
	err = txErr(t, s, func(tx store.Tx) error { return tx.Directives().Delete(t.Context(), bankA, d2.ID) })
	wantErrIs(t, "Delete(deleted)", err, domain.ErrNotFound)
	if n := len(listDirectives(t, s, bankB, false)); n != 1 {
		t.Errorf("List(bank B) = %d directives, want 1", n)
	}
}

// ---------- graph and entity maintenance queues ----------

// linkCount returns Graph().Degree(unit, kind).
func linkCount(t *testing.T, s store.DB, unit domain.UnitID, kind graph.Kind) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		n, err = tx.Graph().Degree(t.Context(), unit, kind)
		return err
	})
	return n
}

// dirty returns up to limit of bank A's units queued in graph_dirty.
func dirty(t *testing.T, s store.DB, limit int) []domain.UnitID {
	t.Helper()
	var ids []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ids, err = tx.Units().DirtyLinks(t.Context(), bankA, limit)
		return err
	})
	return ids
}

// clearDirty removes ids from bank A's graph_dirty.
func clearDirty(t *testing.T, s store.DB, ids ...domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Units().ClearDirtyLinks(t.Context(), bankA, ids) })
}

// deleteDocUnits runs DeleteByDocument on a bank-A document in a transaction that takes the bank's Seq.
func deleteDocUnits(t *testing.T, s store.DB, doc domain.DocumentID) []domain.UnitID {
	t.Helper()
	var ids []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		var err error
		ids, err = tx.Units().DeleteByDocument(t.Context(), bankA, doc)
		return err
	})
	return ids
}

// testLinksDirty: Count counts a unit's links of one kind in either direction; DeleteByDocument and Archive
// queue the removed units' live link neighbours in graph_dirty; Dirty returns them up to limit; ClearDirty
// empties the queue.
func testLinksDirty(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	x, n1, n2, y, n3 := fact(bankA, "x"), fact(bankA, "n1"), fact(bankA, "n2"), fact(bankA, "y"), fact(bankA, "n3")
	x.DocumentID = "doc-1"
	insertUnits(t, s, x, n1, n2, y, n3)
	insertLinks(t, s,
		link(bankA, x.ID, n1.ID, graph.KindSemantic, 0.9), link(bankA, n2.ID, x.ID, graph.KindSemantic, 0.8),
		link(bankA, x.ID, n2.ID, graph.KindTemporal, 0.7), link(bankA, y.ID, n3.ID, graph.KindCausal, 0.6))
	counts := []struct {
		unit domain.UnitID
		kind graph.Kind
		want int
	}{
		{x.ID, graph.KindSemantic, 2},
		{x.ID, graph.KindTemporal, 1},
		{x.ID, graph.KindCausal, 0},
		{n1.ID, graph.KindSemantic, 1},
	}
	for _, c := range counts {
		if got := linkCount(t, s, c.unit, c.kind); got != c.want {
			t.Errorf("Count(%s, %s) = %d, want %d", c.unit, c.kind, got, c.want)
		}
	}
	clearDirty(t, s, x.ID, n1.ID, n2.ID, y.ID, n3.ID) // a baseline whatever Insert queued
	wantSet(t, "Dirty after ClearDirty", dirty(t, s, 10), nil)
	wantSet(t, "DeleteByDocument ids", deleteDocUnits(t, s, "doc-1"), []domain.UnitID{x.ID})
	wantSet(t, "Dirty after DeleteByDocument", dirty(t, s, 10), []domain.UnitID{n1.ID, n2.ID})
	archive(t, s, y.ID)
	wantSet(t, "Dirty after Archive", dirty(t, s, 10), []domain.UnitID{n1.ID, n2.ID, n3.ID})
	if got := dirty(t, s, 1); len(got) != 1 {
		t.Errorf("Dirty(limit 1) = %v, want one unit", got)
	}
	if got := linkCount(t, s, n1.ID, graph.KindSemantic) + linkCount(t, s, n3.ID, graph.KindCausal); got != 0 {
		t.Errorf("links left on n1 and n3 after their neighbours were removed = %d, want 0", got)
	}
	clearDirty(t, s, n1.ID, n2.ID, n3.ID)
	wantSet(t, "Dirty after the final ClearDirty", dirty(t, s, 10), nil)
}

// pendingMaintenance returns up to limit of bank A's entities queued in entity_maintenance.
func pendingMaintenance(t *testing.T, s store.DB, limit int) []domain.EntityID {
	t.Helper()
	var ids []domain.EntityID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ids, err = tx.Entities().PendingMaintenance(t.Context(), bankA, limit)
		return err
	})
	return ids
}

// pruneOrphans runs PruneOrphans on bank A's ids and returns how many entities it deleted.
func pruneOrphans(t *testing.T, s store.DB, ids ...domain.EntityID) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		n, err = tx.Entities().PruneOrphans(t.Context(), bankA, ids)
		return err
	})
	return n
}

// candidateIDs returns the ids of Candidates(name) in bank A.
func candidateIDs(t *testing.T, s store.DB, name string) []domain.EntityID {
	t.Helper()
	var out []domain.EntityID
	for _, c := range candidates(t, s, bankA, name, 5) {
		out = append(out, c.Entity.ID)
	}
	return out
}

// testEntityMaintenance: Archive and DeleteByDocument queue the removed units' entities; PendingMaintenance
// returns them up to limit; PruneOrphans deletes only the unreferenced ones and empties the queue.
func testEntityMaintenance(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	archived, kept, inDoc := fact(bankA, "archived"), fact(bankA, "kept"), fact(bankA, "in doc")
	inDoc.DocumentID = "doc-1"
	insertUnits(t, s, archived, kept, inDoc)
	only := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Zanzibar Expedition", Kind: "event"})
	shared := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Quentin Blake", Kind: "person"})
	docEnt := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Marmalade Factory", Kind: "place"})
	inTx(t, s, func(tx store.Tx) error {
		ents := tx.Entities()
		if err := ents.Attach(t.Context(), archived.ID, []domain.EntityID{only, shared}); err != nil {
			return err
		}
		if err := ents.Attach(t.Context(), kept.ID, []domain.EntityID{shared}); err != nil {
			return err
		}
		return ents.Attach(t.Context(), inDoc.ID, []domain.EntityID{docEnt})
	})
	if n := pruneOrphans(t, s, only, shared, docEnt); n != 0 { // a baseline whatever Attach queued
		t.Errorf("PruneOrphans of referenced entities deleted %d, want 0", n)
	}
	wantSet(t, "PendingMaintenance after PruneOrphans", pendingMaintenance(t, s, 10), nil)
	archive(t, s, archived.ID)
	wantSet(t, "PendingMaintenance after Archive", pendingMaintenance(t, s, 10), []domain.EntityID{only, shared})
	deleteDocUnits(t, s, "doc-1")
	pending := pendingMaintenance(t, s, 10)
	wantSet(t, "PendingMaintenance after DeleteByDocument", pending, []domain.EntityID{only, shared, docEnt})
	if got := pendingMaintenance(t, s, 1); len(got) != 1 {
		t.Errorf("PendingMaintenance(limit 1) = %v, want one entity", got)
	}
	if n := pruneOrphans(t, s, pending...); n != 2 {
		t.Errorf("PruneOrphans = %d, want 2 (the archived unit's and the document's only entities)", n)
	}
	wantSet(t, "PendingMaintenance after PruneOrphans", pendingMaintenance(t, s, 10), nil)
	if slices.Contains(candidateIDs(t, s, "Zanzibar Expedition"), only) {
		t.Errorf("Candidates still lists the pruned entity %s", only)
	}
	if ids := candidateIDs(t, s, "Quentin Blake"); len(ids) == 0 || ids[0] != shared {
		t.Errorf("Candidates(Quentin Blake) = %v, want the still referenced %s first", ids, shared)
	}
}

// testRederiveDetach: Detach removes a unit's entity rows and queues its entities, so PruneOrphans deletes the
// one only it referenced; DeleteForUnit removes the unit's links of the given kinds in either direction and
// keeps the others.
func testRederiveDetach(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	u, other, partner := fact(bankA, "u"), fact(bankA, "other"), fact(bankA, "partner")
	insertUnits(t, s, u, other, partner)
	only := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Zanzibar Expedition", Kind: "event"})
	shared := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Quentin Blake", Kind: "person"})
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Entities().Attach(t.Context(), u.ID, []domain.EntityID{only, shared}); err != nil {
			return err
		}
		return tx.Entities().Attach(t.Context(), other.ID, []domain.EntityID{shared})
	})
	pruneOrphans(t, s, only, shared) // a baseline whatever Attach queued
	insertLinks(t, s, link(bankA, u.ID, partner.ID, graph.KindSemantic, 0.9),
		link(bankA, partner.ID, u.ID, graph.KindSemantic, 0.8), link(bankA, u.ID, partner.ID, graph.KindTemporal, 0.7))
	var n int
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Entities().Detach(t.Context(), u.ID); err != nil {
			return err
		}
		var err error
		n, err = tx.Graph().DeleteUnitLinks(t.Context(), u.ID, []graph.Kind{graph.KindSemantic})
		return err
	})
	if n != 2 {
		t.Errorf("DeleteForUnit(semantic) = %d, want 2 (both directions)", n)
	}
	wantSet(t, "PendingMaintenance after Detach", pendingMaintenance(t, s, 10), []domain.EntityID{only, shared})
	if n := pruneOrphans(t, s, only, shared); n != 1 {
		t.Errorf("PruneOrphans after Detach = %d, want 1 (the entity only the detached unit referenced)", n)
	}
	if sem, tmp := linkCount(t, s, u.ID, graph.KindSemantic), linkCount(t, s, u.ID, graph.KindTemporal); sem != 0 ||
		tmp != 1 {
		t.Errorf("links of u after DeleteForUnit(semantic) = %d semantic, %d temporal; want 0, 1", sem, tmp)
	}
}

// ---------- BankRepo.DeleteBatch and EventRepo.Prune ----------

// fillBank writes one of each content row DeleteBatch removes into bank b: a document with a chunk, two
// facts with an entity and a link, an observation with evidence, a directive and a mental model, plus an
// event. It returns the unit ids.
func fillBank(t *testing.T, s store.DB, b domain.BankRef) []domain.UnitID {
	t.Helper()
	upsertDocs(t, s, b, "doc-1")
	f1, f2, obs := fact(b, "f1"), fact(b, "f2"), observation(b, "obs")
	f1.DocumentID, f2.DocumentID = "doc-1", "doc-1"
	insertUnits(t, s, f1, f2, obs)
	ent := upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Ann", Kind: "person"})
	inTx(t, s, func(tx store.Tx) error {
		c := domain.Chunk{ID: newID[domain.ChunkID](), Bank: b, DocumentID: "doc-1", Text: "c", ContentHash: "c0"}
		if err := tx.Chunks().Insert(t.Context(), []domain.Chunk{c}); err != nil {
			return err
		}
		if err := tx.Entities().Attach(t.Context(), f1.ID, []domain.EntityID{ent}); err != nil {
			return err
		}
		if err := tx.Graph().AddLinks(t.Context(), []graph.Link{link(b, f1.ID, f2.ID, graph.KindSemantic, 1)}); err != nil {
			return err
		}
		if err := tx.Evidence().Add(t.Context(), obs.ID, []domain.Evidence{{Unit: f1.ID, Quote: "f1"}}); err != nil {
			return err
		}
		if _, err := tx.Directives().Create(t.Context(), domain.Directive{Bank: b, Name: "d", Active: true}); err != nil {
			return err
		}
		return tx.MentalModels().Create(t.Context(), sampleModel(b, "model"))
	})
	appendEvents(t, s, b, "event")
	return []domain.UnitID{f1.ID, f2.ID, obs.ID}
}

// deleteBatches calls DeleteBatch(limit) on bank A until it returns 0 and returns the total deleted. It
// fails the test if a call deletes more than limit or 0 never comes.
func deleteBatches(t *testing.T, s store.DB, limit int) int {
	t.Helper()
	total := 0
	for range 500 {
		var n int
		inTx(t, s, func(tx store.Tx) error {
			var err error
			n, err = tx.Banks().DeleteBatch(t.Context(), bankA, limit)
			return err
		})
		if n > limit {
			t.Errorf("DeleteBatch(limit %d) deleted %d", limit, n)
		}
		if n == 0 {
			return total
		}
		total += n
	}
	t.Fatalf("DeleteBatch(limit %d) never returned 0 after 500 calls", limit)
	return total
}

// testDeleteBatch: DeleteBatch removes a tombstoned bank's content in batches until it returns 0, leaving the
// bank row and its events; another bank's content stays.
func testDeleteBatch(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	ids := fillBank(t, s, bankA)
	otherIDs := fillBank(t, s, bankB)
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	if total := deleteBatches(t, s, 3); total < len(ids) {
		t.Errorf("DeleteBatch deleted %d rows in total, want at least the %d units", total, len(ids))
	}
	if b := mustBank(t, s, bankA); b.Ref != bankA {
		t.Errorf("bank row after DeleteBatch = %+v", b)
	}
	if got := getUnits(t, s, bankA, ids...); len(got) != 0 {
		t.Errorf("GetUnits after DeleteBatch = %v, want none", unitIDs(got))
	}
	_, err := getDocument(t, s, bankA, "doc-1")
	wantErrIs(t, "Documents().Get after DeleteBatch", err, domain.ErrNotFound)
	_, err = getModel(t, s, "model")
	wantErrIs(t, "MentalModels().Get after DeleteBatch", err, domain.ErrNotFound)
	wantSeq(t, "Directives after DeleteBatch", directiveIDs(listDirectives(t, s, bankA, false)), nil)
	if m := committed(t, s, "doc-1"); len(m) != 0 {
		t.Errorf("Chunks after DeleteBatch = %v, want none", m)
	}
	wantSeq(t, "Events after DeleteBatch (kept)", eventSubjects(listEvents(t, s, bankA, 0, 10)), []string{"event"})
	wantSeq(t, "bank B units after bank A's DeleteBatch", unitIDs(getUnits(t, s, bankB, otherIDs...)), otherIDs)
}

// testDeleteRefusesLive: DeleteBatch and Delete of a bank that is not tombstoned are ErrFailedPrecondition
// and delete nothing.
func testDeleteRefusesLive(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	ids := fillBank(t, s, bankA)
	err := txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Banks().DeleteBatch(t.Context(), bankA, 100)
		return err
	})
	wantErrIs(t, "DeleteBatch(live bank)", err, domain.ErrFailedPrecondition)
	err = txErr(t, s, func(tx store.Tx) error { return tx.Banks().Delete(t.Context(), bankA) })
	wantErrIs(t, "Delete(live bank)", err, domain.ErrFailedPrecondition)
	mustBank(t, s, bankA)
	wantSeq(t, "units after the refused deletes", unitIDs(getUnits(t, s, bankA, ids...)), ids)
	if _, err := getDocument(t, s, bankA, "doc-1"); err != nil {
		t.Errorf("Documents().Get after the refused deletes: %v", err)
	}
}

// filledBank is what fillAllTables wrote: the live units fillBank wrote, the archived unit, and two entities
// that co-occur on the first unit.
type filledBank struct {
	units    []domain.UnitID
	archived domain.UnitID
	ents     [2]domain.EntityID
}

// fillAllTables writes fillBank's rows into bank b plus an archived unit, a deleted document's unit (queueing
// graph_dirty and entity_maintenance rows), co-occurring entities, an observation version, an audit row and
// a mental-model history entry, so every bank-keyed table a port can reach holds a row.
func fillAllTables(t *testing.T, s store.DB, b domain.BankRef) filledBank {
	t.Helper()
	f := filledBank{units: fillBank(t, s, b)}
	upsertDocs(t, s, b, "doc-2")
	gone, arch := fact(b, "gone"), fact(b, "archived")
	gone.DocumentID, f.archived = "doc-2", arch.ID
	insertUnits(t, s, gone, arch)
	f.ents[0] = upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Beatrix Potter", Kind: "person"})
	f.ents[1] = upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Cyrus Vance", Kind: "person"})
	inTx(t, s, func(tx store.Tx) error {
		ctx, ents, units := t.Context(), tx.Entities(), tx.Units()
		if err := ents.Attach(ctx, f.units[0], f.ents[:]); err != nil {
			return err
		}
		if err := ents.Attach(ctx, gone.ID, f.ents[:1]); err != nil {
			return err
		}
		if err := ents.Attach(ctx, arch.ID, f.ents[1:]); err != nil {
			return err
		}
		ls := []graph.Link{link(b, gone.ID, f.units[1], graph.KindSemantic, 1), link(b, f.units[1], arch.ID,
			graph.KindCausal, 1)}
		if err := tx.Graph().AddLinks(ctx, ls); err != nil {
			return err
		}
		v := domain.ObservationVersion{Version: 1, Text: "obs", SourceFactIDs: f.units[:1], CreatedAt: day(1)}
		if err := units.RecordObservationVersion(ctx, b, f.units[2], v); err != nil {
			return err
		}
		a := domain.AuditEntry{Bank: b, UnitID: f.units[0], Action: domain.CurationEdit, Reason: "r", At: day(1)}
		if err := units.AppendAudit(ctx, a); err != nil {
			return err
		}
		return tx.MentalModels().AppendHistory(ctx, b, "model", historyEntry(domain.HistoryKindVersion, 1), 0)
	})
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		if _, err := tx.Units().DeleteByDocument(t.Context(), b, "doc-2"); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), b, arch.ID, "wrong fact")
	})
	return f
}

// notFoundZero maps a domain.ErrNotFound read to a count of 0.
func notFoundZero(n int, err error) (int, error) {
	if errors.Is(err, domain.ErrNotFound) {
		return 0, nil
	}
	return n, err
}

// boolCount is 1 for true and 0 for false.
func boolCount(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

// bankCounts reads, table by table, how many of fillAllTables' rows bank b still has (as far as the read
// ports can see them; the archive, observation versions and audit rows have no read port).
func bankCounts(t *testing.T, s store.DB, b domain.BankRef, f filledBank) map[string]int {
	t.Helper()
	ctx := t.Context()
	reads := map[string]func(tx store.Tx) (int, error){
		"units": func(store.Tx) (int, error) { us, err := s.Read().GetUnits(ctx, b, f.units); return len(us), err },
		"documents": func(tx store.Tx) (int, error) {
			_, err := tx.Documents().Get(ctx, b, "doc-1")
			return notFoundZero(1, err)
		},
		"chunks": func(tx store.Tx) (int, error) { m, err := tx.Chunks().Committed(ctx, b, "doc-1"); return len(m), err },
		"entities": func(tx store.Tx) (int, error) {
			cs, err := tx.Entities().Candidates(ctx, b, "Beatrix Potter", 5)
			return len(cs), err
		},
		"entity_cooccurrences": func(tx store.Tx) (int, error) {
			return tx.Entities().Cooccurrence(ctx, b, f.ents[0], f.ents[1:])
		},
		"memory_links": func(tx store.Tx) (int, error) { return tx.Graph().Degree(ctx, f.units[0], graph.KindSemantic) },
		"observation_evidence": func(tx store.Tx) (int, error) {
			ok, err := tx.Evidence().Cites(ctx, domain.NewScopeKey(b, nil), f.units[0])
			return boolCount(ok), err
		},
		"graph_dirty": func(tx store.Tx) (int, error) { ids, err := tx.Units().DirtyLinks(ctx, b, 10); return len(ids), err },
		"entity_maintenance": func(tx store.Tx) (int, error) {
			ids, err := tx.Entities().PendingMaintenance(ctx, b, 10)
			return len(ids), err
		},
		"mental_models": func(tx store.Tx) (int, error) {
			_, err := tx.MentalModels().Get(ctx, b, "model")
			return notFoundZero(1, err)
		},
		"mental_model_history": func(tx store.Tx) (int, error) {
			es, err := tx.MentalModels().History(ctx, b, "model", domain.HistoryKindUnspecified, 10)
			return notFoundZero(len(es), err)
		},
		"directives": func(tx store.Tx) (int, error) { ds, err := tx.Directives().List(ctx, b, false); return len(ds), err },
	}
	out := map[string]int{}
	for name, read := range reads {
		inTx(t, s, func(tx store.Tx) error {
			var err error
			out[name], err = read(tx)
			return err
		})
	}
	return out
}

// testDeleteBatchAllTables: DeleteBatch empties every bank-keyed table of the tombstoned bank (as far as the
// read ports see), keeping the bank row and its events, while the same bank id under another tenant (not
// tombstoned) keeps every row, its archive included.
func testDeleteBatchAllTables(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	mine := fillAllTables(t, s, bankA)
	theirs := fillAllTables(t, s, bankOtherTenant)
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	deleteBatches(t, s, 5)
	for table, n := range bankCounts(t, s, bankA, mine) {
		if n != 0 {
			t.Errorf("%s: %d rows left in the tombstoned bank after DeleteBatch, want 0", table, n)
		}
	}
	for table, n := range bankCounts(t, s, bankOtherTenant, theirs) {
		if n == 0 {
			t.Errorf("%s: the other tenant's rows are gone after bank A's DeleteBatch", table)
		}
	}
	mustBank(t, s, bankA)
	wantSeq(t, "events after DeleteBatch (kept)", eventSubjects(listEvents(t, s, bankA, 0, 10)), []string{"event"})
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankOtherTenant); err != nil {
			return err
		}
		return tx.Units().Restore(t.Context(), bankOtherTenant, theirs.archived)
	})
	wantSeq(t, "the other tenant's archived unit after Restore",
		unitIDs(getUnits(t, s, bankOtherTenant, theirs.archived)), []domain.UnitID{theirs.archived})
}

// pruneEvents runs EventRepo.Prune(before) and returns how many it deleted.
func pruneEvents(t *testing.T, s store.DB, before time.Time) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		n, err = tx.Events().Prune(t.Context(), before)
		return err
	})
	return n
}

// testEventsPrune: Prune deletes the events written before the cutoff, across banks, and keeps later ones.
func testEventsPrune(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	appendEvents(t, s, bankA, "e1", "e2")
	appendEvents(t, s, bankB, "b1")
	if n := pruneEvents(t, s, past()); n != 0 {
		t.Errorf("Prune(an hour ago) = %d, want 0", n)
	}
	wantSet(t, "events after Prune(an hour ago)", eventSubjects(listEvents(t, s, bankA, 0, 10)), []string{"e1", "e2"})
	if n := pruneEvents(t, s, future()); n != 3 {
		t.Errorf("Prune(in an hour) = %d, want 3", n)
	}
	wantSet(t, "bank A events after Prune(in an hour)", eventSubjects(listEvents(t, s, bankA, 0, 10)), nil)
	wantSet(t, "bank B events after Prune(in an hour)", eventSubjects(listEvents(t, s, bankB, 0, 10)), nil)
}
