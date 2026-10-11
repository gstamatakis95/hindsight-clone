package postgres_test

import (
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// repoCases are RunStoreSuite's repository subtests: units, documents, chunks, entities, links, evidence,
// events, banks and mental models.
func repoCases() []storeCase {
	return []storeCase{
		{"UnitsUpsertObservation", testUpsertObservation},
		{"UnitsUnconsolidatedOldestFactsFirst", testUnconsolidatedOrder},
		{"UnitsUnconsolidatedScopes", testUnconsolidatedScopes},
		{"UnitsMarkConsolidatedAndFailed", testMarkConsolidated},
		{"UnitsDeleteByDocument", testDeleteByDocument},
		{"UnitsArchiveRestore", testArchiveRestore},
		{"DocumentsGetUnknown", testDocumentUnknown},
		{"DocumentsUpsertCompleteReupsert", testDocumentLifecycle},
		{"DocumentsSetTags", testDocumentSetTags},
		{"ChunksCommittedAndReplace", testChunks},
		{"EntitiesUpsertAssignsID", testEntityUpsert},
		{"EntitiesCandidates", testEntityCandidates},
		{"EntitiesLabelResolvesExisting", testEntityLabel},
		{"EntitiesAttachCooccurrence", testEntityCooccurrence},
		{"EntitiesAttachPrunedIsConflict", testEntityAttachPruned},
		{"LinksInsertNoDuplicates", testLinksIdempotent},
		{"EvidenceAddIdempotentAndCites", testEvidenceCites},
		{"EvidenceDeleteCiting", testEvidenceDeleteCiting},
		{"EventsAppendList", testEvents},
		{"EventsListNeverSplitsSeqGroup", testEventsSeqGroups},
		{"EventsTypeFilterAdvancesLastSeq", testEventsTypeFilter},
		{"BanksGetDeleteUnknown", testBankUnknown},
		{"BanksEnsure", testBankEnsure},
		{"BanksCreateDuplicate", testBankCreate},
		{"BanksSetConfig", testBankSetConfig},
		{"BanksMarkDeleting", testBankMarkDeleting},
		{"BanksDelete", testBankDelete},
		{"BanksTenantConfig", testTenantConfig},
		{"BanksTenantConfigLockSerializes", testTenantConfigLock},
		{"MentalModelsCreateGet", testModelCreateGet},
		{"MentalModelsCreateDuplicate", testModelDuplicate},
		{"MentalModelsUpdate", testModelUpdate},
		{"MentalModelsHistory", testModelHistory},
		{"MentalModelsDelete", testModelDelete},
	}
}

// ---------- UnitRepo ----------

// unconsolidated lists bank b's unconsolidated units in a read transaction.
func unconsolidated(t *testing.T, s store.DB, b domain.BankRef, scopes []domain.ScopeKey, limit int) []domain.Unit {
	t.Helper()
	var us []domain.Unit
	inTx(t, s, func(tx store.Tx) error {
		var err error
		us, err = tx.Units().Unconsolidated(t.Context(), b, scopes, limit)
		return err
	})
	return us
}

// upsertObservation upserts o stamped with a new transaction's Seq and records its history row with
// RecordObservationVersion in the same transaction; it returns the seq. RecordObservationVersion has no
// read port yet, so the history row is only checked to be accepted.
func upsertObservation(t *testing.T, s store.DB, o *domain.Unit) int64 {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if o.Seq, err = tx.Seq(t.Context(), o.Bank); err != nil {
			return err
		}
		if err := tx.Units().UpsertObservation(t.Context(), *o); err != nil {
			return err
		}
		v := domain.ObservationVersion{Version: o.Version, Text: o.Text, SourceIDs: o.SourceIDs, At: day(int(o.Version))}
		return tx.Units().RecordObservationVersion(t.Context(), o.Bank, o.ID, v)
	})
	return o.Seq
}

// testUpsertObservation: the first upsert inserts; the second replaces text, proof count, version and seq.
func testUpsertObservation(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	o := observation(bankA, "Alice likes tea", "team")
	seq1 := upsertObservation(t, s, &o)
	got := getUnits(t, s, bankA, o.ID)
	if len(got) != 1 {
		t.Fatalf("GetUnits after insert = %d units, want 1", len(got))
	}
	compareUnit(t, got[0], o)
	o2 := o
	o2.Text, o2.Version, o2.ProofCount, o2.Embedding = "Alice prefers green tea", 2, 3, angleVec(50)
	seq2 := upsertObservation(t, s, &o2)
	got = getUnits(t, s, bankA, o.ID)
	if len(got) != 1 {
		t.Fatalf("GetUnits after update = %d units, want 1", len(got))
	}
	compareUnit(t, got[0], o2)
	if seq2 <= seq1 {
		t.Errorf("second Seq %d not above first %d", seq2, seq1)
	}
}

// testUnconsolidatedOrder: facts only (no observations), oldest created_at first, limit honoured, bank scoped.
func testUnconsolidatedOrder(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	f1, f2, f3 := embeddedFact(bankA, "first"), embeddedFact(bankA, "second"), embeddedFact(bankA, "third")
	f1.CreatedAt, f2.CreatedAt, f3.CreatedAt = day(1), day(2), day(3)
	f2.Type = domain.FactTypeExperience
	obs := observation(bankA, "an observation")
	obs.CreatedAt = day(0)
	insertUnits(t, s, f3, obs, f1, f2)
	other := embeddedFact(bankB, "other bank")
	other.CreatedAt = day(0)
	insertUnits(t, s, other)

	wantSeq(t, "Unconsolidated(all)", unitIDs(unconsolidated(t, s, bankA, nil, 10)),
		[]domain.UnitID{f1.ID, f2.ID, f3.ID})
	wantSeq(t, "Unconsolidated(limit 2)", unitIDs(unconsolidated(t, s, bankA, nil, 2)), []domain.UnitID{f1.ID, f2.ID})
	got := unconsolidated(t, s, bankA, nil, 1)
	if len(got) != 1 {
		t.Fatalf("Unconsolidated(limit 1) = %v, want one unit", unitIDs(got))
	}
	if got[0].Text != "first" || !got[0].CreatedAt.Equal(day(1)) {
		t.Errorf("Unconsolidated returned %q created %v, want the stored unit", got[0].Text, got[0].CreatedAt)
	}
}

// testUnconsolidatedScopes: with scopes, only units whose tags contain one scope's tags are returned.
func testUnconsolidatedScopes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	tagSets := [][]string{{"alpha"}, {"alpha", "beta"}, {"beta"}, {}, {"gamma"}}
	us := make([]domain.Unit, len(tagSets))
	for i, tags := range tagSets {
		us[i] = embeddedFact(bankA, "scoped fact")
		us[i].Tags = domain.NormalizeTags(tags)
		us[i].CreatedAt = day(i + 1)
	}
	insertUnits(t, s, us...)
	scope := func(tags ...string) domain.ScopeKey { return domain.NewScopeKey(bankA, tags) }
	cases := []struct {
		name   string
		scopes []domain.ScopeKey
		want   []domain.UnitID
	}{
		{"{alpha}", []domain.ScopeKey{scope("alpha")}, []domain.UnitID{us[0].ID, us[1].ID}},
		{"{alpha} {gamma}", []domain.ScopeKey{scope("alpha"), scope("gamma")}, []domain.UnitID{us[0].ID, us[1].ID, us[4].ID}},
		{"{alpha,beta}", []domain.ScopeKey{scope("beta", "alpha")}, []domain.UnitID{us[1].ID}},
		{"{delta}", []domain.ScopeKey{scope("delta")}, nil},
	}
	for _, c := range cases {
		wantSeq(t, "Unconsolidated scopes "+c.name, unitIDs(unconsolidated(t, s, bankA, c.scopes, 10)), c.want)
	}
}

// testMarkConsolidated: consolidated and parked units leave Unconsolidated and keep their stamps.
func testMarkConsolidated(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	us := []domain.Unit{
		embeddedFact(bankA, "f1"), embeddedFact(bankA, "f2"), embeddedFact(bankA, "f3"), embeddedFact(bankA, "f4"),
	}
	for i := range us {
		us[i].CreatedAt = day(i + 1)
	}
	insertUnits(t, s, us...)
	at := day(30)
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{us[0].ID}, at); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), []domain.UnitID{us[1].ID}, at)
	})
	wantSeq(t, "Unconsolidated after marks", unitIDs(unconsolidated(t, s, bankA, nil, 10)),
		[]domain.UnitID{us[2].ID, us[3].ID})
	got := getUnits(t, s, bankA, us[0].ID, us[1].ID)
	if len(got) != 2 {
		t.Fatalf("GetUnits = %d units, want 2", len(got))
	}
	if !sameTimePtr(got[0].ConsolidatedAt, &at) || got[0].ConsolidationFailedAt != nil {
		t.Errorf("consolidated unit stamps = %v / %v, want %v / nil", got[0].ConsolidatedAt,
			got[0].ConsolidationFailedAt, at)
	}
	if !sameTimePtr(got[1].ConsolidationFailedAt, &at) || got[1].ConsolidatedAt != nil {
		t.Errorf("parked unit stamps = %v / %v, want nil / %v", got[1].ConsolidatedAt,
			got[1].ConsolidationFailedAt, at)
	}
}

// testDeleteByDocument: the document's units are deleted and returned; other documents' units stay.
func testDeleteByDocument(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	u1, u2, u3 := fact(bankA, "u1"), fact(bankA, "u2"), fact(bankA, "u3")
	u1.DocumentID, u2.DocumentID, u3.DocumentID = "doc-1", "doc-1", "doc-2"
	insertUnits(t, s, u1, u2, u3)
	insertLinks(t, s, link(bankA, u1.ID, u3.ID, domain.LinkSemantic, 0.5))
	var deleted []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		var err error
		deleted, err = tx.Units().DeleteByDocument(t.Context(), bankA, "doc-1")
		return err
	})
	wantSet(t, "DeleteByDocument ids", deleted, []domain.UnitID{u1.ID, u2.ID})
	wantSeq(t, "GetUnits after DeleteByDocument", unitIDs(getUnits(t, s, bankA, u1.ID, u2.ID, u3.ID)),
		[]domain.UnitID{u3.ID})
}

// archive archives unit id of bank A in a transaction that takes the bank's Seq.
func archive(t *testing.T, s store.DB, id domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), bankA, id, "wrong fact")
	})
}

// testArchiveRestore: an archived unit disappears from GetUnits and every search; Restore brings it back
// with its entities and causal links reattached.
func testArchiveRestore(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	u, keep, partner := fact(bankA, "The platypus lays eggs"), fact(bankA, "Neighbour"), fact(bankA, "Eggs hatch")
	u.Embedding, keep.Embedding = angleVec(0), angleVec(20)
	u.Tags = []string{"zoology"}
	insertUnits(t, s, u, keep, partner)
	insertLinks(t, s, link(bankA, keep.ID, u.ID, domain.LinkCausal, 0.9),
		link(bankA, u.ID, partner.ID, domain.LinkCausal, 0.8))
	ent := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Platypus", Kind: "animal"})
	inTx(t, s, func(tx store.Tx) error { return tx.Entities().Attach(t.Context(), u.ID, []domain.EntityID{ent}) })
	archive(t, s, u.ID)
	if got := getUnits(t, s, bankA, u.ID); len(got) != 0 {
		t.Errorf("GetUnits(archived) = %v, want none", unitIDs(got))
	}
	wantNoHit(t, "SearchVector archived", searchVector(t, s,
		store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10}), u.ID)
	wantNoHit(t, "SearchText archived", searchText(t, s, store.TextQuery{Bank: bankA, Text: "platypus", Limit: 10}), u.ID)
	wantNoHit(t, "ExpandGraph archived", expandGraph(t, s,
		store.GraphQuery{Bank: bankA, Seeds: []domain.UnitID{keep.ID}, MaxHops: 2, Limit: 10}), u.ID)
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		return tx.Units().Restore(t.Context(), bankA, u.ID)
	})
	got := getUnits(t, s, bankA, u.ID)
	if len(got) != 1 {
		t.Fatalf("GetUnits after Restore = %d units, want 1", len(got))
	}
	if got[0].Text != u.Text || got[0].Type != u.Type || !slices.Equal(got[0].Tags, u.Tags) {
		t.Errorf("restored unit = %q %q %v, want %q %q %v", got[0].Text, got[0].Type, got[0].Tags,
			u.Text, u.Type, u.Tags)
	}
	q := store.GraphQuery{
		Bank: bankA, Seeds: []domain.UnitID{u.ID}, MaxHops: 1, Kinds: []domain.LinkKind{domain.LinkCausal},
		Limit: 10,
	}
	wantSeq(t, "ExpandGraph(causal) from the restored unit", hitIDs(expandGraph(t, s, q)), []domain.UnitID{partner.ID})
	if n := pruneOrphans(t, s, ent); n != 0 {
		t.Errorf("PruneOrphans of the restored unit's entity deleted %d, want 0 (Restore reattaches it)", n)
	}
}

// ---------- DocumentRepo ----------

// testDocumentUnknown: Get of an unknown document is ErrNotFound.
func testDocumentUnknown(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	_, err := getDocument(t, s, bankA, "missing")
	wantErrIs(t, "Documents().Get(unknown)", err, domain.ErrNotFound)
}

// mustDocument reads a document and fails the test on error.
func mustDocument(t *testing.T, s store.DB, id domain.DocumentID) domain.Document {
	t.Helper()
	d, err := getDocument(t, s, bankA, id)
	if err != nil {
		t.Fatalf("Documents().Get(%s): %v", id, err)
	}
	return d
}

// compareDocument reports differing stored fields of a document.
func compareDocument(t *testing.T, step string, got, want domain.Document) {
	t.Helper()
	if got.ID != want.ID || got.Bank != want.Bank || got.BlobKey != want.BlobKey || !slices.Equal(got.Tags, want.Tags) {
		t.Errorf("%s: document = %s %v %q %v, want %s %v %q %v", step, got.ID, got.Bank, got.BlobKey, got.Tags,
			want.ID, want.Bank, want.BlobKey, want.Tags)
	}
	if got.ContentHash != want.ContentHash || got.OverridesHash != want.OverridesHash {
		t.Errorf("%s: hashes = %q/%q, want %q/%q", step, got.ContentHash, got.OverridesHash,
			want.ContentHash, want.OverridesHash)
	}
	if (got.ExtractedAt == nil) != (want.ExtractedAt == nil) {
		t.Errorf("%s: extracted_at = %v, want set=%v", step, got.ExtractedAt, want.ExtractedAt != nil)
	}
	wantJSON(t, step+": overrides", got.Overrides, want.Overrides)
}

// testDocumentLifecycle: Upsert leaves ExtractedAt nil; Complete stamps hashes and ExtractedAt; a second
// Upsert replaces tags, overrides and blob key and clears ExtractedAt but keeps the hashes.
func testDocumentLifecycle(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	d := domain.Document{
		ID: "doc-1", Bank: bankA, Tags: []string{"a", "b"}, BlobKey: blob.Key(bankA, "doc-1", "h1"),
		Overrides: &domain.BankConfig{RetainMission: "extract recipes", ExtractionMode: domain.ExtractionModeVerbose},
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Upsert(t.Context(), d) })
	compareDocument(t, "after Upsert", mustDocument(t, s, d.ID), d)

	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Complete(t.Context(), bankA, d.ID, "h1", "o1") })
	want := d
	want.ContentHash, want.OverridesHash, want.ExtractedAt = "h1", "o1", ptr(day(0))
	compareDocument(t, "after Complete", mustDocument(t, s, d.ID), want)

	d2 := domain.Document{ID: d.ID, Bank: bankA, Tags: []string{"c"}, BlobKey: blob.Key(bankA, d.ID, "h2")}
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Upsert(t.Context(), d2) })
	want = d2
	want.ContentHash, want.OverridesHash = "h1", "o1"
	compareDocument(t, "after second Upsert", mustDocument(t, s, d.ID), want)
}

// testDocumentSetTags: SetTags replaces the whole tag set.
func testDocumentSetTags(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	d := domain.Document{ID: "doc-1", Bank: bankA, Tags: []string{"a", "b"}}
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Upsert(t.Context(), d) })
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().SetTags(t.Context(), bankA, d.ID, []string{"z"}) })
	wantSeq(t, "tags after SetTags", mustDocument(t, s, d.ID).Tags, []string{"z"})
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().SetTags(t.Context(), bankA, d.ID, []string{}) })
	wantSeq(t, "tags after SetTags(empty)", mustDocument(t, s, d.ID).Tags, []string{})
}

// ---------- ChunkRepo ----------

// committed reads a document's committed chunks.
func committed(t *testing.T, s store.DB, doc domain.DocumentID) map[int]string {
	t.Helper()
	var m map[int]string
	inTx(t, s, func(tx store.Tx) error {
		var err error
		m, err = tx.Chunks().Committed(t.Context(), bankA, doc)
		return err
	})
	return m
}

// chunk builds a chunk of a bank-A document.
func chunk(doc domain.DocumentID, idx int, hash string) domain.Chunk {
	return domain.Chunk{
		ID: newID[domain.ChunkID](), Bank: bankA, DocumentID: doc, Index: idx, Text: "chunk " + hash,
		ContentHash: hash, TokenCount: 3,
	}
}

// testChunks: Committed is empty, then maps index to hash; a chunk at an existing index replaces it.
func testChunks(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	if m := committed(t, s, "doc-1"); len(m) != 0 {
		t.Errorf("Committed before Insert = %v, want empty", m)
	}
	inTx(t, s, func(tx store.Tx) error {
		return tx.Chunks().Insert(t.Context(), []domain.Chunk{
			chunk("doc-1", 0, "h0"), chunk("doc-1", 1, "h1"), chunk("doc-2", 0, "other"),
		})
	})
	wantMap(t, "Committed after Insert", committed(t, s, "doc-1"), map[int]string{0: "h0", 1: "h1"})
	inTx(t, s, func(tx store.Tx) error {
		return tx.Chunks().Insert(t.Context(), []domain.Chunk{chunk("doc-1", 1, "h1b")})
	})
	wantMap(t, "Committed after re-insert at index 1", committed(t, s, "doc-1"), map[int]string{0: "h0", 1: "h1b"})
	wantMap(t, "Committed of doc-2", committed(t, s, "doc-2"), map[int]string{0: "other"})
}

// wantMap fails the test unless got equals want.
func wantMap(t *testing.T, what string, got, want map[int]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

// ---------- EntityRepo ----------

// upsertEntity upserts e in its own transaction and returns the id.
func upsertEntity(t *testing.T, s store.DB, e domain.Entity) domain.EntityID {
	t.Helper()
	var id domain.EntityID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		id, err = tx.Entities().Upsert(t.Context(), e)
		return err
	})
	return id
}

// candidates returns entity candidates for name in bank b.
func candidates(t *testing.T, s store.DB, b domain.BankRef, name string, limit int) []domain.EntityCandidate {
	t.Helper()
	var cs []domain.EntityCandidate
	inTx(t, s, func(tx store.Tx) error {
		var err error
		cs, err = tx.Entities().Candidates(t.Context(), b, name, limit)
		return err
	})
	return cs
}

// testEntityUpsert: Upsert without an id assigns one; Upsert with that id updates the same entity.
func testEntityUpsert(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	id := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Alice Smith", Kind: "person"})
	if id == "" {
		t.Fatalf("Upsert returned an empty id")
	}
	id2 := upsertEntity(t, s, domain.Entity{ID: id, Bank: bankA, CanonicalName: "Alice B. Smith", Kind: "person"})
	if id2 != id {
		t.Errorf("Upsert with id %s returned %s", id, id2)
	}
	cs := candidates(t, s, bankA, "Alice B. Smith", 5)
	// The bank holds one entity, so exactly one candidate proves the update did not insert a second row.
	if len(cs) != 1 || cs[0].Entity.ID != id || cs[0].Entity.CanonicalName != "Alice B. Smith" {
		t.Errorf("Candidates after update = %+v, want only %s named Alice B. Smith", cs, id)
	}
	other := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Bob Jones", Kind: "person"})
	if other == "" || other == id {
		t.Errorf("second entity id = %q, want a new id", other)
	}
}

// testEntityCandidates: trigram candidates, most similar first, similarity in (0, 1], bank-isolated.
func testEntityCandidates(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	alice := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Alice Smith", Kind: "person"})
	upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Alice Smithson-Wright", Kind: "person"})
	bob := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Bob Jones", Kind: "person"})
	otherAlice := upsertEntity(t, s, domain.Entity{Bank: bankB, CanonicalName: "Alice Smith", Kind: "person"})
	cs := candidates(t, s, bankA, "alice smyth", 5)
	if len(cs) == 0 || cs[0].Entity.ID != alice {
		t.Fatalf("Candidates(alice smyth) = %+v, want %s (Alice Smith) first", cs, alice)
	}
	for i, c := range cs {
		if c.NameSimilarity <= 0 || c.NameSimilarity > 1 {
			t.Errorf("candidate %d similarity %v not in (0, 1]", i, c.NameSimilarity)
		}
		if i > 0 && c.NameSimilarity > cs[i-1].NameSimilarity {
			t.Errorf("candidate %d similarity %v above candidate %d's %v", i, c.NameSimilarity, i-1, cs[i-1].NameSimilarity)
		}
		if c.Entity.ID == otherAlice || c.Entity.ID == bob {
			t.Errorf("Candidates(alice smyth) returned %s (%q): other bank or dissimilar", c.Entity.ID,
				c.Entity.CanonicalName)
		}
	}
	if cs := candidates(t, s, bankA, "alice smyth", 1); len(cs) != 1 {
		t.Errorf("Candidates limit 1 returned %d", len(cs))
	}
	if cs := candidates(t, s, bankB, "alice smyth", 5); len(cs) != 1 || cs[0].Entity.ID != otherAlice {
		t.Errorf("Candidates in bank B = %+v, want only %s", cs, otherAlice)
	}
}

// testEntityLabel: a label entity with the same (kind, label value) resolves to the existing row.
func testEntityLabel(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	red := domain.Entity{Bank: bankA, CanonicalName: "color:red", Kind: "color", LabelValue: "red"}
	id1 := upsertEntity(t, s, red)
	id2 := upsertEntity(t, s, red)
	if id1 == "" || id1 != id2 {
		t.Errorf("label entity upserted twice: ids %q and %q, want the same", id1, id2)
	}
	blue := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "color:blue", Kind: "color", LabelValue: "blue"})
	size := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "size:red", Kind: "size", LabelValue: "red"})
	if blue == id1 || size == id1 || blue == size {
		t.Errorf("distinct labels share ids: red %s blue %s size:red %s", id1, blue, size)
	}
}

// cooccurrence counts how many of others co-occur with e.
func cooccurrence(t *testing.T, s store.DB, e domain.EntityID, others ...domain.EntityID) int {
	t.Helper()
	var n int
	inTx(t, s, func(tx store.Tx) error {
		var err error
		n, err = tx.Entities().Cooccurrence(t.Context(), bankA, e, others)
		return err
	})
	return n
}

// testEntityCooccurrence: Attach links units to entities; Cooccurrence counts others sharing a unit;
// re-attaching is a no-op.
func testEntityCooccurrence(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	u1, u2 := fact(bankA, "u1"), fact(bankA, "u2")
	insertUnits(t, s, u1, u2)
	var e [4]domain.EntityID
	for i, name := range []string{"Ann", "Ben", "Cid", "Dee"} {
		e[i] = upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: name, Kind: "person"})
	}
	attach := func(u domain.UnitID, ents ...domain.EntityID) {
		inTx(t, s, func(tx store.Tx) error { return tx.Entities().Attach(t.Context(), u, ents) })
	}
	attach(u1.ID, e[0], e[1])
	attach(u2.ID, e[0], e[2])
	check := func(step string) {
		t.Helper()
		if n := cooccurrence(t, s, e[0], e[1], e[2], e[3]); n != 2 {
			t.Errorf("%s: Cooccurrence(Ann; Ben, Cid, Dee) = %d, want 2", step, n)
		}
		if n := cooccurrence(t, s, e[1], e[0], e[2]); n != 1 {
			t.Errorf("%s: Cooccurrence(Ben; Ann, Cid) = %d, want 1", step, n)
		}
		if n := cooccurrence(t, s, e[1], e[2], e[3]); n != 0 {
			t.Errorf("%s: Cooccurrence(Ben; Cid, Dee) = %d, want 0", step, n)
		}
	}
	check("after Attach")
	attach(u1.ID, e[0], e[1])
	check("after re-Attach")
}

// testEntityAttachPruned: Attach of an entity that was pruned (resolved before graph maintenance deleted
// it) is domain.ErrConflict and attaches nothing, not even the entities that still exist (decision 2501).
func testEntityAttachPruned(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	u := fact(bankA, "Ann met Ben")
	insertUnits(t, s, u)
	ann := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Ann", Kind: "person"})
	ben := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Ben", Kind: "person"})
	if n := pruneOrphans(t, s, ben); n != 1 {
		t.Fatalf("PruneOrphans(Ben) = %d, want 1", n)
	}
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		return tx.Entities().Attach(t.Context(), u.ID, []domain.EntityID{ann, ben})
	})
	wantErrIs(t, "Attach with a pruned entity", err, domain.ErrConflict)
	if got := getUnits(t, s, bankA, u.ID); len(got) != 1 || len(got[0].Entities) != 0 {
		t.Errorf("unit after the failed Attach = %+v, want it with no entities", got)
	}
	for _, c := range candidates(t, s, bankA, "Ann", 5) {
		if c.Entity.ID == ann && c.Entity.MentionCount != 0 {
			t.Errorf("Ann's MentionCount after the failed Attach = %d, want 0", c.Entity.MentionCount)
		}
	}
}

// ---------- LinkRepo ----------

// testLinksIdempotent: inserted links are traversed; re-inserting an edge keeps one edge.
func testLinksIdempotent(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	seed, a, b := fact(bankA, "seed"), fact(bankA, "a"), fact(bankA, "b")
	insertUnits(t, s, seed, a, b)
	insertLinks(t, s, link(bankA, seed.ID, a.ID, domain.LinkSemantic, 0.5), link(bankA, a.ID, b.ID, domain.LinkCausal, 1))
	q := store.GraphQuery{Bank: bankA, Seeds: []domain.UnitID{seed.ID}, MaxHops: 2, Limit: 10}
	wantSet(t, "ExpandGraph after Insert", hitIDs(expandGraph(t, s, q)), []domain.UnitID{a.ID, b.ID})
	insertLinks(t, s, link(bankA, seed.ID, a.ID, domain.LinkSemantic, 0.9))
	q.MaxHops = 1
	wantSeq(t, "ExpandGraph after re-Insert", hitIDs(expandGraph(t, s, q)), []domain.UnitID{a.ID})
	q.Kinds = []domain.LinkKind{domain.LinkTemporal}
	wantSeq(t, "ExpandGraph Kinds=[temporal]", hitIDs(expandGraph(t, s, q)), nil)
}

// ---------- EvidenceRepo ----------

// cites reports Evidence().Cites(s, unit).
func cites(t *testing.T, s store.DB, scope domain.ScopeKey, unit domain.UnitID) bool {
	t.Helper()
	var ok bool
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ok, err = tx.Evidence().Cites(t.Context(), scope, unit)
		return err
	})
	return ok
}

// addEvidence records evidence of obs for units, quoting each.
func addEvidence(t *testing.T, s store.DB, obs domain.UnitID, units ...domain.UnitID) {
	t.Helper()
	ev := make([]domain.Evidence, len(units))
	for i, u := range units {
		ev[i] = domain.Evidence{Unit: u, Quote: "quote of " + string(u)}
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Evidence().Add(t.Context(), obs, ev) })
}

// deleteCiting runs DeleteCiting in its own transaction.
func deleteCiting(t *testing.T, s store.DB, ids ...domain.UnitID) (deleted, reset []domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		var err error
		deleted, reset, err = tx.Evidence().DeleteCiting(t.Context(), bankA, ids)
		return err
	})
	return deleted, reset
}

// testEvidenceCites: Add is idempotent; Cites is true only for an observation in exactly that scope.
func testEvidenceCites(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	f1, f2, f3 := fact(bankA, "f1"), fact(bankA, "f2"), fact(bankA, "f3")
	obs := observation(bankA, "observation in team", "team")
	insertUnits(t, s, f1, f2, f3, obs)
	addEvidence(t, s, obs.ID, f1.ID, f2.ID)
	addEvidence(t, s, obs.ID, f1.ID)
	team := domain.NewScopeKey(bankA, []string{"team"})
	checks := []struct {
		name  string
		scope domain.ScopeKey
		unit  domain.UnitID
		want  bool
	}{
		{"{team} cites f1", team, f1.ID, true},
		{"{team} cites f2", team, f2.ID, true},
		{"{team} cites f3", team, f3.ID, false},
		{"shared scope cites f1", domain.NewScopeKey(bankA, nil), f1.ID, false},
		{"{other,team} cites f1", domain.NewScopeKey(bankA, []string{"team", "other"}), f1.ID, false},
		{"bank B {team} cites f1", domain.NewScopeKey(bankB, []string{"team"}), f1.ID, false},
	}
	for _, c := range checks {
		if got := cites(t, s, c.scope, c.unit); got != c.want {
			t.Errorf("Cites %s = %v, want %v", c.name, got, c.want)
		}
	}
	deleted, _ := deleteCiting(t, s, f1.ID)
	wantSeq(t, "DeleteCiting deleted (evidence added twice)", deleted, []domain.UnitID{obs.ID})
}

// testEvidenceDeleteCiting: observations citing the ids are deleted, their other sources reset and back in
// Unconsolidated; observations not citing them stay.
func testEvidenceDeleteCiting(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	f1, f2, f3 := embeddedFact(bankA, "f1"), embeddedFact(bankA, "f2"), embeddedFact(bankA, "f3")
	o1, o2 := observation(bankA, "o1 rests on f1 and f2"), observation(bankA, "o2 rests on f3")
	insertUnits(t, s, f1, f2, f3, o1, o2)
	addEvidence(t, s, o1.ID, f1.ID, f2.ID)
	addEvidence(t, s, o2.ID, f3.ID)
	inTx(t, s, func(tx store.Tx) error {
		return tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{f1.ID, f2.ID, f3.ID}, day(5))
	})
	if got := unconsolidated(t, s, bankA, nil, 10); len(got) != 0 {
		t.Fatalf("Unconsolidated after MarkConsolidated = %v, want none", unitIDs(got))
	}
	deleted, reset := deleteCiting(t, s, f1.ID)
	wantSet(t, "DeleteCiting deleted", deleted, []domain.UnitID{o1.ID})
	wantSet(t, "DeleteCiting reset", reset, []domain.UnitID{f2.ID})
	wantSeq(t, "GetUnits(o1, o2) after DeleteCiting", unitIDs(getUnits(t, s, bankA, o1.ID, o2.ID)),
		[]domain.UnitID{o2.ID})
	uc := unitIDs(unconsolidated(t, s, bankA, nil, 10))
	if !slices.Contains(uc, f2.ID) || slices.Contains(uc, f3.ID) {
		t.Errorf("Unconsolidated after DeleteCiting = %v, want f2 %s back and f3 %s still consolidated", uc,
			f2.ID, f3.ID)
	}
	if cites(t, s, domain.NewScopeKey(bankA, nil), f2.ID) {
		t.Errorf("Cites(shared, f2) still true after its observation was deleted")
	}
}

// ---------- EventRepo ----------

// appendEvents appends events of bank b in one transaction stamped with its Seq, which it returns.
func appendEvents(t *testing.T, s store.DB, b domain.BankRef, subjects ...string) int64 {
	t.Helper()
	var seq int64
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if seq, err = tx.Seq(t.Context(), b); err != nil {
			return err
		}
		for _, sub := range subjects {
			ev := domain.LifecycleEvent{
				Seq: seq, Type: domain.EventTypeRetainCompleted, Bank: b, SubjectID: sub, At: nowMicro(),
			}
			if err := tx.Events().Append(t.Context(), ev); err != nil {
				return err
			}
		}
		return nil
	})
	return seq
}

// eventSubjects returns the subject ids of evs in order.
func eventSubjects(evs []domain.LifecycleEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.SubjectID
	}
	return out
}

// testEvents: List returns events after afterSeq in seq order, honours limit and is bank-scoped.
func testEvents(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	s1 := appendEvents(t, s, bankA, "e1", "e2")
	s2 := appendEvents(t, s, bankA, "e3")
	appendEvents(t, s, bankB, "other")
	all := listEvents(t, s, bankA, 0, 10)
	if len(all) != 3 {
		t.Fatalf("List(after 0) = %+v, want 3 events", all)
	}
	wantSet(t, "first tx's events", eventSubjects(all[:2]), []string{"e1", "e2"})
	if all[0].Seq != s1 || all[1].Seq != s1 || all[2].Seq != s2 || all[2].SubjectID != "e3" {
		t.Errorf("List seqs = %d %d %d (last %q), want %d %d %d (e3)", all[0].Seq, all[1].Seq, all[2].Seq,
			all[2].SubjectID, s1, s1, s2)
	}
	for _, e := range all {
		if e.Bank != bankA || e.Type != domain.EventTypeRetainCompleted {
			t.Errorf("event %q bank/type = %v %q, want %v %q", e.SubjectID, e.Bank, e.Type, bankA,
				domain.EventTypeRetainCompleted)
		}
	}
	wantSeq(t, "List(after s1)", eventSubjects(listEvents(t, s, bankA, s1, 10)), []string{"e3"})
	if evs := listEvents(t, s, bankA, 0, 2); len(evs) != 2 || evs[0].Seq != s1 || evs[1].Seq != s1 {
		t.Errorf("List(limit 2) = %+v, want the two seq-%d events", evs, s1)
	}
	wantSeq(t, "List(bank B)", eventSubjects(listEvents(t, s, bankB, 0, 10)), []string{"other"})
	if evs := listEvents(t, s, bankA, s2, 10); len(evs) != 0 {
		t.Errorf("List(after last seq) = %+v, want none", evs)
	}
}

// wantSeqGroup fails the test unless evs are exactly the events of one transaction: subjects (any order),
// all at seq.
func wantSeqGroup(t *testing.T, what string, evs []domain.LifecycleEvent, seq int64, subjects ...string) {
	t.Helper()
	wantSet(t, what, eventSubjects(evs), subjects)
	for _, e := range evs {
		if e.Seq != seq {
			t.Errorf("%s: event %q at seq %d, want %d", what, e.SubjectID, e.Seq, seq)
		}
	}
}

// testEventsSeqGroups: List's limit is soft and never splits one transaction's events; polling with the
// highest returned seq reads the next group.
func testEventsSeqGroups(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	s1 := appendEvents(t, s, bankA, "e1", "e2")
	s2 := appendEvents(t, s, bankA, "e3", "e4")
	wantSeqGroup(t, "List(after 0, limit 1)", listEvents(t, s, bankA, 0, 1), s1, "e1", "e2")
	wantSeqGroup(t, "List(after the first group, limit 1)", listEvents(t, s, bankA, s1, 1), s2, "e3", "e4")
	wantSeqGroup(t, "List(after the second group, limit 1)", listEvents(t, s, bankA, s2, 1), 0)
}

// testEventsTypeFilter: a type filter matching none of the scanned events returns no events, yet lastSeq is
// the highest seq scanned, so the next poll advances; lastSeq is never below afterSeq.
func testEventsTypeFilter(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	appendEvents(t, s, bankA, "e1", "e2")
	s2 := appendEvents(t, s, bankA, "e3")
	failed := []domain.EventType{domain.EventTypeOperationFailed}
	evs, last := listEventsOf(t, s, bankA, 0, 10, failed)
	if len(evs) != 0 || last != s2 {
		t.Errorf("List(after 0, OPERATION_FAILED only) = %d events, lastSeq %d; want none, %d", len(evs), last, s2)
	}
	if evs, last = listEventsOf(t, s, bankA, s2, 10, failed); len(evs) != 0 || last != s2 {
		t.Errorf("List(after %d, OPERATION_FAILED only) = %d events, lastSeq %d; want none, %d", s2, len(evs), last, s2)
	}
}
