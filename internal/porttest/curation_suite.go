package porttest

import (
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// curationCases are RunStoreSuite's subtests of curation and maintenance writes: unit edits and retags,
// directives, graph and entity maintenance queues, batched bank deletion and event pruning.
func curationCases() []storeCase {
	return []storeCase{
		{"UnitsUpdate", testUnitUpdate},
		{"UnitsSetDocumentTags", testSetDocumentTags},
		{"DirectivesUpsertListDelete", testDirectives},
		{"LinksCountDirtyClearDirty", testLinksDirty},
		{"EntitiesPendingMaintenancePruneOrphans", testEntityMaintenance},
		{"BanksDeleteBatch", testDeleteBatch},
		{"EventsPrune", testEventsPrune},
	}
}

// ---------- UnitRepo curation ----------

// testUnitUpdate: Update replaces a fact's curated fields; a nil Embedding takes it out of SearchVector but
// not GetUnits. AppendAudit is accepted in the same transaction. Unknown or archived ids are ErrNotFound.
func testUnitUpdate(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA)
	u, other := fact(bankA, "Alice lives in Paris"), fact(bankA, "Bob lives in Rome")
	u.Embedding, other.Embedding, u.Context = angleVec(0), angleVec(5), "chat"
	insertUnits(t, s, u, other)
	edited := u
	edited.Text, edited.Context, edited.Type = "Alice lives in Lisbon", "edited", domain.FactTypeExperience
	edited.OccurredStart, edited.OccurredEnd = ptr(day(3)), ptr(day(4))
	edited.EditedAt, edited.Embedding = ptr(day(5)), nil
	inTx(t, s, func(tx port.Tx) error {
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
	hs := searchVector(t, s, port.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10})
	wantSeq(t, "SearchVector after Update with a nil embedding", hitIDs(hs), []port.UnitID{other.ID})
	missing := fact(bankA, "never inserted")
	err := txErr(t, s, func(tx port.Tx) error { return tx.Units().Update(t.Context(), missing) })
	wantErrIs(t, "Update(unknown)", err, domain.ErrNotFound)
	archive(t, s, other.ID)
	err = txErr(t, s, func(tx port.Tx) error { return tx.Units().Update(t.Context(), other) })
	wantErrIs(t, "Update(archived)", err, domain.ErrNotFound)
}

// testSetDocumentTags: SetDocumentTags rewrites the tags and seq of the document's units only and returns
// their ids.
func testSetDocumentTags(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	u1, u2, u3 := fact(bankA, "u1"), fact(bankA, "u2"), fact(bankA, "u3")
	u1.DocumentID, u2.DocumentID, u3.DocumentID = "doc-1", "doc-1", "doc-2"
	u1.Tags, u2.Tags, u3.Tags = []string{"old"}, []string{"old"}, []string{"old"}
	oldSeq := insertUnits(t, s, u1, u2, u3)
	tags := domain.NormalizeTags([]string{"new", "x"})
	var (
		seq int64
		ids []port.UnitID
	)
	inTx(t, s, func(tx port.Tx) error {
		var err error
		if seq, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		ids, err = tx.Units().SetDocumentTags(t.Context(), bankA, "doc-1", tags, seq)
		return err
	})
	wantSet(t, "SetDocumentTags ids", ids, []port.UnitID{u1.ID, u2.ID})
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

// ---------- DirectiveRepo ----------

// upsertDirective upserts d in its own transaction and returns the id.
func upsertDirective(t *testing.T, s port.Store, d domain.Directive) port.DirectiveID {
	t.Helper()
	var id port.DirectiveID
	inTx(t, s, func(tx port.Tx) error {
		var err error
		id, err = tx.Directives().Upsert(t.Context(), d)
		return err
	})
	return id
}

// listDirectives lists bank b's directives.
func listDirectives(t *testing.T, s port.Store, b port.BankRef, activeOnly bool) []domain.Directive {
	t.Helper()
	var ds []domain.Directive
	inTx(t, s, func(tx port.Tx) error {
		var err error
		ds, err = tx.Directives().List(t.Context(), b, activeOnly)
		return err
	})
	return ds
}

// directiveIDs returns the ids of ds in order.
func directiveIDs(ds []domain.Directive) []port.DirectiveID {
	out := make([]port.DirectiveID, len(ds))
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

// testDirectives: Upsert assigns ids and replaces by id; List is oldest first and filters inactive ones;
// Delete removes, and deleting again is ErrNotFound; banks are isolated.
func testDirectives(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA, bankB)
	d1 := domain.Directive{Bank: bankA, Name: "tone", Rules: []string{"be brief", "cite sources"}, Active: true}
	d2 := domain.Directive{Bank: bankA, Name: "legal", Rules: []string{"no advice"}, Tags: []string{"legal"}}
	d1.ID = upsertDirective(t, s, d1)
	d2.ID = upsertDirective(t, s, d2)
	if d1.ID == "" || d2.ID == "" || d1.ID == d2.ID {
		t.Fatalf("Upsert ids = %q, %q; want two distinct ids", d1.ID, d2.ID)
	}
	all := listDirectives(t, s, bankA, false)
	wantSeq(t, "List(all)", directiveIDs(all), []port.DirectiveID{d1.ID, d2.ID})
	if len(all) == 2 {
		compareDirective(t, all[0], d1)
		compareDirective(t, all[1], d2)
	}
	wantSeq(t, "List(active)", directiveIDs(listDirectives(t, s, bankA, true)), []port.DirectiveID{d1.ID})
	d1.Name, d1.Active = "tone v2", false
	if id := upsertDirective(t, s, d1); id != d1.ID {
		t.Errorf("Upsert with id %s returned %s", d1.ID, id)
	}
	wantSeq(t, "List(active) after deactivating", directiveIDs(listDirectives(t, s, bankA, true)), nil)
	inTx(t, s, func(tx port.Tx) error { return tx.Directives().Delete(t.Context(), bankA, d2.ID) })
	all = listDirectives(t, s, bankA, false)
	wantSeq(t, "List(all) after Delete", directiveIDs(all), []port.DirectiveID{d1.ID})
	if len(all) == 1 {
		compareDirective(t, all[0], d1)
	}
	err := txErr(t, s, func(tx port.Tx) error { return tx.Directives().Delete(t.Context(), bankA, d2.ID) })
	wantErrIs(t, "Delete(deleted)", err, domain.ErrNotFound)
	wantSeq(t, "List(bank B)", directiveIDs(listDirectives(t, s, bankB, false)), nil)
}

// ---------- graph and entity maintenance queues ----------

// linkCount returns Links().Count(unit, kind).
func linkCount(t *testing.T, s port.Store, unit port.UnitID, kind domain.LinkKind) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx port.Tx) error {
		var err error
		n, err = tx.Links().Count(t.Context(), unit, kind)
		return err
	})
	return n
}

// dirty returns up to limit of bank A's units queued in graph_dirty.
func dirty(t *testing.T, s port.Store, limit int) []port.UnitID {
	t.Helper()
	var ids []port.UnitID
	inTx(t, s, func(tx port.Tx) error {
		var err error
		ids, err = tx.Links().Dirty(t.Context(), bankA, limit)
		return err
	})
	return ids
}

// clearDirty removes ids from bank A's graph_dirty.
func clearDirty(t *testing.T, s port.Store, ids ...port.UnitID) {
	t.Helper()
	inTx(t, s, func(tx port.Tx) error { return tx.Links().ClearDirty(t.Context(), bankA, ids) })
}

// deleteDocUnits runs DeleteByDocument on a bank-A document in a transaction that takes the bank's Seq.
func deleteDocUnits(t *testing.T, s port.Store, doc port.DocumentID) []port.UnitID {
	t.Helper()
	var ids []port.UnitID
	inTx(t, s, func(tx port.Tx) error {
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
func testLinksDirty(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	x, n1, n2, y, n3 := fact(bankA, "x"), fact(bankA, "n1"), fact(bankA, "n2"), fact(bankA, "y"), fact(bankA, "n3")
	x.DocumentID = "doc-1"
	insertUnits(t, s, x, n1, n2, y, n3)
	insertLinks(t, s,
		link(bankA, x.ID, n1.ID, domain.LinkSemantic, 0.9), link(bankA, n2.ID, x.ID, domain.LinkSemantic, 0.8),
		link(bankA, x.ID, n2.ID, domain.LinkTemporal, 0.7), link(bankA, y.ID, n3.ID, domain.LinkCausal, 0.6))
	counts := []struct {
		unit port.UnitID
		kind domain.LinkKind
		want int
	}{
		{x.ID, domain.LinkSemantic, 2},
		{x.ID, domain.LinkTemporal, 1},
		{x.ID, domain.LinkCausal, 0},
		{n1.ID, domain.LinkSemantic, 1},
	}
	for _, c := range counts {
		if got := linkCount(t, s, c.unit, c.kind); got != c.want {
			t.Errorf("Count(%s, %s) = %d, want %d", c.unit, c.kind, got, c.want)
		}
	}
	clearDirty(t, s, x.ID, n1.ID, n2.ID, y.ID, n3.ID) // a baseline whatever Insert queued
	wantSet(t, "Dirty after ClearDirty", dirty(t, s, 10), nil)
	wantSet(t, "DeleteByDocument ids", deleteDocUnits(t, s, "doc-1"), []port.UnitID{x.ID})
	wantSet(t, "Dirty after DeleteByDocument", dirty(t, s, 10), []port.UnitID{n1.ID, n2.ID})
	archive(t, s, y.ID)
	wantSet(t, "Dirty after Archive", dirty(t, s, 10), []port.UnitID{n1.ID, n2.ID, n3.ID})
	if got := dirty(t, s, 1); len(got) != 1 {
		t.Errorf("Dirty(limit 1) = %v, want one unit", got)
	}
	if got := linkCount(t, s, n1.ID, domain.LinkSemantic) + linkCount(t, s, n3.ID, domain.LinkCausal); got != 0 {
		t.Errorf("links left on n1 and n3 after their neighbours were removed = %d, want 0", got)
	}
	clearDirty(t, s, n1.ID, n2.ID, n3.ID)
	wantSet(t, "Dirty after the final ClearDirty", dirty(t, s, 10), nil)
}

// pendingMaintenance returns up to limit of bank A's entities queued in entity_maintenance.
func pendingMaintenance(t *testing.T, s port.Store, limit int) []port.EntityID {
	t.Helper()
	var ids []port.EntityID
	inTx(t, s, func(tx port.Tx) error {
		var err error
		ids, err = tx.Entities().PendingMaintenance(t.Context(), bankA, limit)
		return err
	})
	return ids
}

// pruneOrphans runs PruneOrphans on bank A's ids and returns how many entities it deleted.
func pruneOrphans(t *testing.T, s port.Store, ids ...port.EntityID) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx port.Tx) error {
		var err error
		n, err = tx.Entities().PruneOrphans(t.Context(), bankA, ids)
		return err
	})
	return n
}

// candidateIDs returns the ids of Candidates(name) in bank A.
func candidateIDs(t *testing.T, s port.Store, name string) []port.EntityID {
	t.Helper()
	var out []port.EntityID
	for _, c := range candidates(t, s, bankA, name, 5) {
		out = append(out, c.Entity.ID)
	}
	return out
}

// testEntityMaintenance: Archive and DeleteByDocument queue the removed units' entities; PendingMaintenance
// returns them up to limit; PruneOrphans deletes only the unreferenced ones and empties the queue.
func testEntityMaintenance(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	archived, kept, inDoc := fact(bankA, "archived"), fact(bankA, "kept"), fact(bankA, "in doc")
	inDoc.DocumentID = "doc-1"
	insertUnits(t, s, archived, kept, inDoc)
	only := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Zanzibar Expedition", Kind: "event"})
	shared := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Quentin Blake", Kind: "person"})
	docEnt := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Marmalade Factory", Kind: "place"})
	inTx(t, s, func(tx port.Tx) error {
		ents := tx.Entities()
		if err := ents.Attach(t.Context(), archived.ID, []port.EntityID{only, shared}); err != nil {
			return err
		}
		if err := ents.Attach(t.Context(), kept.ID, []port.EntityID{shared}); err != nil {
			return err
		}
		return ents.Attach(t.Context(), inDoc.ID, []port.EntityID{docEnt})
	})
	if n := pruneOrphans(t, s, only, shared, docEnt); n != 0 { // a baseline whatever Attach queued
		t.Errorf("PruneOrphans of referenced entities deleted %d, want 0", n)
	}
	wantSet(t, "PendingMaintenance after PruneOrphans", pendingMaintenance(t, s, 10), nil)
	archive(t, s, archived.ID)
	wantSet(t, "PendingMaintenance after Archive", pendingMaintenance(t, s, 10), []port.EntityID{only, shared})
	deleteDocUnits(t, s, "doc-1")
	pending := pendingMaintenance(t, s, 10)
	wantSet(t, "PendingMaintenance after DeleteByDocument", pending, []port.EntityID{only, shared, docEnt})
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

// ---------- BankRepo.DeleteBatch and EventRepo.Prune ----------

// fillBank writes one of each content row DeleteBatch removes into bank b: a document with a chunk, two
// facts with an entity and a link, an observation with evidence, a directive and a mental model, plus an
// event. It returns the unit ids.
func fillBank(t *testing.T, s port.Store, b port.BankRef) []port.UnitID {
	t.Helper()
	upsertDocs(t, s, b, "doc-1")
	f1, f2, obs := fact(b, "f1"), fact(b, "f2"), observation(b, "obs")
	f1.DocumentID, f2.DocumentID = "doc-1", "doc-1"
	insertUnits(t, s, f1, f2, obs)
	ent := upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Ann", Kind: "person"})
	inTx(t, s, func(tx port.Tx) error {
		c := domain.Chunk{ID: newID[port.ChunkID](), Bank: b, DocumentID: "doc-1", Text: "c", ContentHash: "c0"}
		if err := tx.Chunks().Insert(t.Context(), []domain.Chunk{c}); err != nil {
			return err
		}
		if err := tx.Entities().Attach(t.Context(), f1.ID, []port.EntityID{ent}); err != nil {
			return err
		}
		if err := tx.Links().Insert(t.Context(), []domain.Link{link(b, f1.ID, f2.ID, domain.LinkSemantic, 1)}); err != nil {
			return err
		}
		if err := tx.Evidence().Add(t.Context(), obs.ID, []domain.Evidence{{Unit: f1.ID, Quote: "f1"}}); err != nil {
			return err
		}
		if _, err := tx.Directives().Upsert(t.Context(), domain.Directive{Bank: b, Name: "d", Active: true}); err != nil {
			return err
		}
		return tx.MentalModels().Create(t.Context(), sampleModel(b, "model"))
	})
	appendEvents(t, s, b, "event")
	return []port.UnitID{f1.ID, f2.ID, obs.ID}
}

// deleteBatches calls DeleteBatch(limit) on bank A until it returns 0 and returns the total deleted. It
// fails the test if a call deletes more than limit or 0 never comes.
func deleteBatches(t *testing.T, s port.Store, limit int) int {
	t.Helper()
	total := 0
	for range 500 {
		var n int
		inTx(t, s, func(tx port.Tx) error {
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

// testDeleteBatch: DeleteBatch removes a bank's content in batches until it returns 0, leaving the bank row
// and its events; another bank's content stays.
func testDeleteBatch(t *testing.T, s port.Store) {
	ensureBanks(t, s, bankA, bankB)
	ids := fillBank(t, s, bankA)
	otherIDs := fillBank(t, s, bankB)
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

// pruneEvents runs EventRepo.Prune(before) and returns how many it deleted.
func pruneEvents(t *testing.T, s port.Store, before time.Time) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx port.Tx) error {
		var err error
		n, err = tx.Events().Prune(t.Context(), before)
		return err
	})
	return n
}

// testEventsPrune: Prune deletes the events written before the cutoff, across banks, and keeps later ones.
func testEventsPrune(t *testing.T, s port.Store) {
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
