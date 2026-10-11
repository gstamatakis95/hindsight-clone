package postgres_test

import (
	"cmp"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// TestReads runs the contract tests of the id-keyed Reader lookups and small reads:
// GetChunks, EntityStates, StaleScopes, TagCounts, CountObservations, NewestSeqInScope, ModelsCiting, the
// temporal relevance filter and Document.ObservationScopeModes. It asserts the semantics pinned in
// docs/decisions/1500-wp15-read-semantics.md, including tenant isolation for every method.
func TestReads(t *testing.T) {
	t.Parallel()
	runStoreCases(t, []storeCase{
		{"get_chunks", testRdGetChunks},
		{"entity_states", testRdEntityStates},
		{"entity_states_id_order", testRdEntityStatesIDOrder},
		{"stale_scopes", testRdStaleScopes},
		{"tag_counts", testRdTagCounts},
		{"count_observations", testRdCountObservations},
		{"newest_seq_in_scope", testRdNewestSeqInScope},
		{"models_citing", testRdModelsCiting},
		{"temporal_relevance_filter", testRdTemporalRelevance},
		{"document_observation_scopes", testRdDocumentObservationScopes},
		{"error_regimes", testRdErrorRegimes},
	})
}

// rdBadID is a malformed (non-UUID) id; id-keyed lookups skip it.
const rdBadID = "not-a-uuid"

// ---------- shared helpers ----------

// rdMust fails the test when err is set and returns v otherwise.
func rdMust[T any](t *testing.T, what string, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return v
}

// rdFact builds a world fact of bank b created on day n with the given tags (normalized).
func rdFact(b domain.BankRef, text string, n int, tags ...string) domain.Unit {
	u := fact(b, text)
	u.CreatedAt, u.Tags = day(n), domain.NormalizeTags(tags)
	return u
}

// rdObs builds an observation of bank b created on day n in the scope of tags.
func rdObs(b domain.BankRef, text string, n int, tags ...string) domain.Unit {
	o := observation(b, text, tags...)
	o.CreatedAt = day(n)
	return o
}

// rdArchive archives unit id of bank b in a transaction that takes the bank's Seq.
func rdArchive(t *testing.T, s store.DB, b domain.BankRef, id domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), b, id, "wrong")
	})
}

// rdScope is the scope of bank b with the given tags.
func rdScope(b domain.BankRef, tags ...string) domain.ScopeKey { return domain.NewScopeKey(b, tags) }

// rdTombstone marks bank b deleting.
func rdTombstone(t *testing.T, s store.DB, b domain.BankRef) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), b, day(400)) })
}

// ---------- GetChunks ----------

// rdChunk builds chunk idx of document doc of bank b.
func rdChunk(b domain.BankRef, doc domain.DocumentID, idx int) domain.Chunk {
	return domain.Chunk{
		ID: newID[domain.ChunkID](), Bank: b, DocumentID: doc, Index: idx, Text: fmt.Sprintf("chunk %d", idx),
		ContentHash: fmt.Sprintf("h%d", idx), TokenCount: idx + 3, CreatedAt: day(1),
	}
}

// rdInsertChunks inserts cs in one transaction.
func rdInsertChunks(t *testing.T, s store.DB, cs ...domain.Chunk) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Chunks().Insert(t.Context(), cs) })
}

// rdChunkIDs returns the ids of cs in order.
func rdChunkIDs(cs []domain.Chunk) []domain.ChunkID {
	out := make([]domain.ChunkID, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// rdGetChunks runs GetChunks through Read() and fails the test on error.
func rdGetChunks(t *testing.T, s store.DB, b domain.BankRef, ids []domain.ChunkID) []domain.Chunk {
	t.Helper()
	cs, err := s.Read().GetChunks(t.Context(), b, ids)
	return rdMust(t, fmt.Sprintf("GetChunks(%v)", b), cs, err)
}

// rdCompareChunk fails the test unless got carries want's stored fields.
func rdCompareChunk(t *testing.T, got, want domain.Chunk) {
	t.Helper()
	if got.Bank != want.Bank || got.DocumentID != want.DocumentID || got.Index != want.Index ||
		got.Text != want.Text || got.ContentHash != want.ContentHash || got.TokenCount != want.TokenCount {
		t.Errorf("GetChunks chunk = %+v, want %+v (ignoring CreatedAt)", got, want)
	}
}

// testRdGetChunks: input order is kept; unknown, malformed, other-bank and other-tenant ids are skipped; a
// repeated id comes back once.
func testRdGetChunks(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB, bankOtherTenant)
	for _, b := range []domain.BankRef{bankA, bankB, bankOtherTenant} {
		upsertDocs(t, s, b, "doc-1")
	}
	c0, c1, c2 := rdChunk(bankA, "doc-1", 0), rdChunk(bankA, "doc-1", 1), rdChunk(bankA, "doc-1", 2)
	cb, co := rdChunk(bankB, "doc-1", 0), rdChunk(bankOtherTenant, "doc-1", 0)
	rdInsertChunks(t, s, c0, c1, c2)
	rdInsertChunks(t, s, cb)
	rdInsertChunks(t, s, co)

	ids := []domain.ChunkID{c2.ID, newID[domain.ChunkID](), c0.ID, rdBadID, cb.ID, co.ID, c2.ID, c1.ID}
	got := rdGetChunks(t, s, bankA, ids)
	wantSeq(t, "GetChunks(bank A)", rdChunkIDs(got), []domain.ChunkID{c2.ID, c0.ID, c1.ID})
	if len(got) > 0 && got[0].ID == c2.ID {
		rdCompareChunk(t, got[0], c2)
	}
	wantSeq(t, "GetChunks(bank B)", rdChunkIDs(rdGetChunks(t, s, bankB, ids)), []domain.ChunkID{cb.ID})
	wantSeq(t, "GetChunks(other tenant)", rdChunkIDs(rdGetChunks(t, s, bankOtherTenant, ids)),
		[]domain.ChunkID{co.ID})
	if got := rdGetChunks(t, s, bankA, nil); len(got) != 0 {
		t.Errorf("GetChunks(nil ids) = %v, want empty", rdChunkIDs(got))
	}
}

// ---------- EntityStates ----------

// rdEntityFixture is bank A's EntityStates data plus the other tenant's.
type rdEntityFixture struct {
	f1, f2, f3                  domain.Unit
	ann, bob, cy                domain.EntityID
	obsCite, obsDirect, obsBoth domain.Unit
	obsArch                     domain.Unit
	otherFact, otherObs         domain.Unit
	otherAnn                    domain.EntityID
}

// rdAttach attaches unit to ents in its own transaction.
func rdAttach(t *testing.T, s store.DB, unit domain.UnitID, ents ...domain.EntityID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Entities().Attach(t.Context(), unit, ents) })
}

// rdPerson upserts a person entity of bank b and returns its id.
func rdPerson(t *testing.T, s store.DB, b domain.BankRef, name string) domain.EntityID {
	t.Helper()
	return upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: name, Kind: "person"})
}

// rdSeedEntities seeds bank A with facts f1 and f2 (the inputs) and f3 (not an input):
//   - ann is attached to f1 and f3, bob to f1 and f2, cy only to f3;
//   - obsCite cites only f3, so it is listed for ann although f3 is not an input;
//   - obsDirect is attached directly to bob and cites nothing;
//   - obsBoth cites f1 and is attached to ann, so ann lists it once and bob lists it through f1;
//   - obsArch cites f2 and is archived, so it is never listed.
//
// The other tenant gets a fact attached to its own "Ann" and an observation citing it.
func rdSeedEntities(t *testing.T, s store.DB) rdEntityFixture {
	t.Helper()
	ensureBanks(t, s, bankA, bankOtherTenant)
	var f rdEntityFixture
	f.f1, f.f2, f.f3 = rdFact(bankA, "f1", 1), rdFact(bankA, "f2", 2), rdFact(bankA, "f3", 3)
	f.obsCite, f.obsDirect = rdObs(bankA, "cites f3", 4), rdObs(bankA, "attached to bob", 5)
	f.obsBoth, f.obsArch = rdObs(bankA, "cites f1, attached to ann", 6), rdObs(bankA, "archived", 7)
	insertUnits(t, s, f.f1, f.f2, f.f3, f.obsCite, f.obsDirect, f.obsBoth, f.obsArch)
	f.ann, f.bob, f.cy = rdPerson(t, s, bankA, "Ann"), rdPerson(t, s, bankA, "Bob"), rdPerson(t, s, bankA, "Cy")
	rdAttach(t, s, f.f1.ID, f.ann, f.bob)
	rdAttach(t, s, f.f2.ID, f.bob)
	rdAttach(t, s, f.f3.ID, f.ann, f.cy)
	rdAttach(t, s, f.obsDirect.ID, f.bob)
	rdAttach(t, s, f.obsBoth.ID, f.ann)
	addEvidence(t, s, f.obsCite.ID, f.f3.ID)
	addEvidence(t, s, f.obsBoth.ID, f.f1.ID)
	addEvidence(t, s, f.obsArch.ID, f.f2.ID)
	rdArchive(t, s, bankA, f.obsArch.ID)

	f.otherFact, f.otherObs = rdFact(bankOtherTenant, "other f", 1), rdObs(bankOtherTenant, "other obs", 2)
	insertUnits(t, s, f.otherFact, f.otherObs)
	f.otherAnn = rdPerson(t, s, bankOtherTenant, "Ann")
	rdAttach(t, s, f.otherFact.ID, f.otherAnn)
	addEvidence(t, s, f.otherObs.ID, f.otherFact.ID)
	return f
}

// rdEntityStates runs EntityStates through Read() and fails the test on error.
func rdEntityStates(t *testing.T, s store.DB, b domain.BankRef, units ...domain.UnitID) []domain.EntityState {
	t.Helper()
	es, err := s.Read().EntityStates(t.Context(), b, units)
	return rdMust(t, fmt.Sprintf("EntityStates(%v)", b), es, err)
}

// rdWantEntityStates fails the test unless got equals want in order (ObservationIDs compared in order).
func rdWantEntityStates(t *testing.T, what string, got, want []domain.EntityState) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %d entities %+v, want %d %+v", what, len(got), got, len(want), want)
		return
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.EntityID != w.EntityID || g.CanonicalName != w.CanonicalName {
			t.Errorf("%s[%d]: got %s %q, want %s %q", what, i, g.EntityID, g.CanonicalName, w.EntityID,
				w.CanonicalName)
			continue
		}
		wantSeq(t, fmt.Sprintf("%s[%d] %s ObservationIDs", what, i, w.CanonicalName), g.ObservationIDs,
			w.ObservationIDs)
	}
}

// rdState builds an expected EntityState with its observation ids sorted.
func rdState(id domain.EntityID, name string, obs ...domain.UnitID) domain.EntityState {
	return domain.EntityState{EntityID: id, CanonicalName: name, ObservationIDs: sorted(obs)}
}

// testRdEntityStates: entities attached to the input units come back ordered by id; an observation is
// listed when it is attached to the entity or cites a live fact attached to it (an input or not); archived
// observations, unknown and malformed units and other tenants never show up. A Tx read agrees.
func testRdEntityStates(t *testing.T, s store.DB) {
	f := rdSeedEntities(t, s)
	annState := rdState(f.ann, "Ann", f.obsCite.ID, f.obsBoth.ID)
	bobState := rdState(f.bob, "Bob", f.obsDirect.ID, f.obsBoth.ID)
	want := []domain.EntityState{annState, bobState}
	slices.SortFunc(want, func(a, b domain.EntityState) int { return cmp.Compare(a.EntityID, b.EntityID) })

	in := []domain.UnitID{f.f2.ID, f.f1.ID, newID[domain.UnitID](), rdBadID, f.f2.ID, f.otherFact.ID}
	rdWantEntityStates(t, "EntityStates(bank A)", rdEntityStates(t, s, bankA, in...), want)
	rdWantEntityStates(t, "EntityStates(bank A, f2)", rdEntityStates(t, s, bankA, f.f2.ID),
		[]domain.EntityState{bobState})
	rdWantEntityStates(t, "EntityStates(other tenant)", rdEntityStates(t, s, bankOtherTenant, in...),
		[]domain.EntityState{rdState(f.otherAnn, "Ann", f.otherObs.ID)})
	if got := rdEntityStates(t, s, bankA, f.otherFact.ID, f.obsArch.ID); len(got) != 0 {
		t.Errorf("EntityStates(bank A, other tenant's and archived units) = %+v, want empty", got)
	}
	var inTxStates []domain.EntityState
	inTx(t, s, func(tx store.Tx) error {
		var err error
		inTxStates, err = tx.EntityStates(t.Context(), bankA, in)
		return err
	})
	rdWantEntityStates(t, "Tx.EntityStates(bank A)", inTxStates, want)
}

// testRdEntityStatesIDOrder: five entities with explicit ids, created out of order and named so that name
// order is the reverse of id order, come back in id order (neither by name nor by insertion).
func testRdEntityStatesIDOrder(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	f := rdFact(bankA, "f", 1)
	insertUnits(t, s, f)
	names := map[int]string{1: "Eve", 2: "Dan", 3: "Cy", 4: "Bob", 5: "Ann"}
	var want []domain.EntityState
	for _, n := range []int{3, 1, 5, 2, 4} {
		id := domain.EntityID(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
		got := upsertEntity(t, s, domain.Entity{ID: id, Bank: bankA, CanonicalName: names[n], Kind: "person"})
		if got != id {
			t.Fatalf("Upsert(%s) = %s, want the given id", id, got)
		}
		rdAttach(t, s, f.ID, id)
	}
	for n := 1; n <= 5; n++ {
		want = append(want, rdState(domain.EntityID(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)), names[n]))
	}
	rdWantEntityStates(t, "EntityStates(id order)", rdEntityStates(t, s, bankA, f.ID), want)
}

// ---------- StaleScopes ----------

// rdScopeStrings renders scopes as "tenant/bank|key" so they compare in order (ScopeKey holds a slice).
func rdScopeStrings(ks []domain.ScopeKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.Tenant + "/" + k.Bank + "|" + k.String()
	}
	return out
}

// rdStale runs StaleScopes through Read() and fails the test on error.
func rdStale(t *testing.T, s store.DB, b domain.BankRef, scopes []domain.ScopeKey) []domain.ScopeKey {
	t.Helper()
	ks, err := s.Read().StaleScopes(t.Context(), b, scopes)
	return rdMust(t, "StaleScopes", ks, err)
}

// rdWantStale fails the test unless StaleScopes(b, in) returns want, in order.
func rdWantStale(t *testing.T, s store.DB, what string, b domain.BankRef, in, want []domain.ScopeKey) {
	t.Helper()
	wantSeq(t, what, rdScopeStrings(rdStale(t, s, b, in)), rdScopeStrings(want))
}

// rdMark stamps ids consolidated (failed = false) or parked (failed = true) in its own transaction.
func rdMark(t *testing.T, s store.DB, failed bool, ids ...domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if failed {
			return tx.Units().MarkConsolidationFailed(t.Context(), ids, day(50))
		}
		return tx.Units().MarkConsolidated(t.Context(), ids, day(50))
	})
}

// testRdStaleScopes: a scope is stale while an unconsolidated fact of the bank carries all its tags (the
// shared scope while any fact is pending); MarkConsolidated and MarkConsolidationFailed clear it; pending
// observations, consolidated or parked facts and other tenants' facts never count; input order and
// duplicates are kept; a Tx read sees its own uncommitted fact.
func testRdStaleScopes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	f1, f2 := rdFact(bankA, "f1", 1, "x", "y"), rdFact(bankA, "f2", 2, "z")
	done, parked := rdFact(bankA, "done", 3, "w"), rdFact(bankA, "parked", 3, "v")
	done.ConsolidatedAt, parked.ConsolidationFailedAt = ptr(day(4)), ptr(day(4))
	insertUnits(t, s, f1, f2, done, parked, rdObs(bankA, "pending-looking observation", 5, "w"))
	insertUnits(t, s, rdFact(bankOtherTenant, "other pending", 1, "v", "w", "x", "y", "z"))

	x, xy, xz := rdScope(bankA, "x"), rdScope(bankA, "y", "x"), rdScope(bankA, "x", "z")
	w, v, shared := rdScope(bankA, "w"), rdScope(bankA, "v"), rdScope(bankA)
	in := []domain.ScopeKey{xz, x, shared, w, xy, v, x}
	rdWantStale(t, s, "StaleScopes initially", bankA, in, []domain.ScopeKey{x, shared, xy, x})
	rdWantStale(t, s, "StaleScopes(other tenant) initially", bankOtherTenant,
		[]domain.ScopeKey{rdScope(bankOtherTenant, "w"), rdScope(bankOtherTenant, "x", "z")},
		[]domain.ScopeKey{rdScope(bankOtherTenant, "w"), rdScope(bankOtherTenant, "x", "z")})

	rdMark(t, s, false, f1.ID)
	rdWantStale(t, s, "StaleScopes after MarkConsolidated(f1)", bankA, in, []domain.ScopeKey{shared})
	z := rdScope(bankA, "z")
	rdWantStale(t, s, "StaleScopes(z) while f2 is pending", bankA, []domain.ScopeKey{z, x}, []domain.ScopeKey{z})
	rdMark(t, s, true, f2.ID)
	rdWantStale(t, s, "StaleScopes after MarkConsolidationFailed(f2)", bankA, in, nil)
	rdWantStale(t, s, "StaleScopes(z) after MarkConsolidationFailed(f2)", bankA, []domain.ScopeKey{z}, nil)
	rdWantStale(t, s, "StaleScopes(bank A, other tenant's scope key)", bankA,
		[]domain.ScopeKey{rdScope(bankOtherTenant, "x")}, nil)

	var inTxStale []domain.ScopeKey
	err := txErr(t, s, func(tx store.Tx) error {
		if _, err := stampAndInsert(t, tx, []domain.Unit{rdFact(bankA, "uncommitted", 6, "w")}); err != nil {
			return err
		}
		var err error
		if inTxStale, err = tx.StaleScopes(t.Context(), bankA, in); err != nil {
			return err
		}
		return errRollback
	})
	wantErrIs(t, "rolled-back tx", err, errRollback)
	wantSeq(t, "Tx.StaleScopes sees its own fact", rdScopeStrings(inTxStale),
		rdScopeStrings([]domain.ScopeKey{shared, w}))
	rdWantStale(t, s, "StaleScopes after rollback", bankA, in, nil)
}

// ---------- TagCounts ----------

// rdTagCounts runs TagCounts through Read() and returns "tag:count" strings, failing the test on error.
func rdTagCounts(
	t *testing.T, s store.DB, b domain.BankRef, src domain.TagSource, prefix, after string, limit int,
) []string {
	t.Helper()
	tcs, err := s.Read().TagCounts(t.Context(), b, src, prefix, after, limit)
	tcs = rdMust(t, fmt.Sprintf("TagCounts(%v, %q, %q, %q, %d)", b, src, prefix, after, limit), tcs, err)
	out := make([]string, len(tcs))
	for i, tc := range tcs {
		out[i] = fmt.Sprintf("%s:%d", tc.Tag, tc.Count)
	}
	return out
}

// rdUpsertTaggedDoc upserts document id of bank b with the given tags.
func rdUpsertTaggedDoc(t *testing.T, s store.DB, b domain.BankRef, id domain.DocumentID, tags ...string) {
	t.Helper()
	d := domain.Document{
		ID: id, Bank: b, Tags: domain.NormalizeTags(tags), BlobKey: blob.Key(b, id, "h0"),
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Upsert(t.Context(), d) })
}

// rdCreateModel creates mental model id of bank b with the given tags and source ids.
func rdCreateModel(t *testing.T, s store.DB, b domain.BankRef, id domain.MentalModelID, tags []string,
	sources ...domain.UnitID,
) {
	t.Helper()
	m := sampleModel(b, id)
	m.Spec.Tags, m.SourceIDs = domain.NormalizeTags(tags), sources
	inTx(t, s, func(tx store.Tx) error { return tx.MentalModels().Create(t.Context(), m) })
}

// rdTagMemories are bank A's memory tag counts in byte order. "_x" < "a" and "z" < "é" hold in byte
// order but not under a locale collation (which ignores "_" and sorts "é" next to "e"); tags are lowercased
// on write, so "B" < "a" cannot be stored. "%", "_" and "\" are the LIKE metacharacters a prefix must
// match literally.
var rdTagMemories = []string{
	"_x:1", "a:4", "a%b:1", `a\b:1`, "a_b:1", "axb:1", "ayb:1", "b:1", "z:1", "é:1",
}

// rdSeedTags seeds bank A's memories (an observation and an archived unit included), mental models and
// documents, and the other tenant's with tags that would show up in bank A's counts if they leaked.
func rdSeedTags(t *testing.T, s store.DB) {
	t.Helper()
	ensureBanks(t, s, bankA, bankOtherTenant)
	gone := rdFact(bankA, "archived", 6, "a", "gone")
	insertUnits(t, s,
		rdFact(bankA, "u1", 1, "_x", "a", "é"), rdFact(bankA, "u2", 2, "a", "z"),
		rdFact(bankA, "u3", 3, "a", "a_b", "a%b"), rdFact(bankA, "u4", 4, "axb", "ayb", `a\b`),
		rdObs(bankA, "obs", 5, "a", "b"), gone)
	rdArchive(t, s, bankA, gone.ID)
	rdCreateModel(t, s, bankA, "m1", []string{"eng", "team"})
	rdCreateModel(t, s, bankA, "m2", []string{"eng"})
	rdUpsertTaggedDoc(t, s, bankA, "doc-1", "doc-x")
	rdUpsertTaggedDoc(t, s, bankA, "doc-2", "doc-x", "doc-y")

	insertUnits(t, s, rdFact(bankOtherTenant, "o1", 1, "a", "leak"), rdFact(bankOtherTenant, "o2", 2, "a", "z"))
	rdCreateModel(t, s, bankOtherTenant, "m1", []string{"eng", "leak"})
	rdUpsertTaggedDoc(t, s, bankOtherTenant, "doc-1", "doc-x", "leak")
}

// testRdTagCounts: all three sources are counted (memories include observations, not archived units;
// unspecified = memories) in byte order; prefix (literal), after (exclusive) and limit (<= 0 = all) apply;
// an unknown or tombstoned bank is ErrNotFound; the other tenant's tags never leak.
func testRdTagCounts(t *testing.T, s store.DB) {
	rdSeedTags(t, s)
	mem, unspec := domain.TagSourceMemories, domain.TagSourceUnspecified
	wantSeq(t, "TagCounts(memories)", rdTagCounts(t, s, bankA, mem, "", "", 0), rdTagMemories)
	wantSeq(t, "TagCounts(unspecified)", rdTagCounts(t, s, bankA, unspec, "", "", 0), rdTagMemories)
	wantSeq(t, "TagCounts(limit -1)", rdTagCounts(t, s, bankA, mem, "", "", -1), rdTagMemories)
	wantSeq(t, "TagCounts(limit 3)", rdTagCounts(t, s, bankA, mem, "", "", 3), rdTagMemories[:3])
	wantSeq(t, "TagCounts(mental models)", rdTagCounts(t, s, bankA, domain.TagSourceMentalModels, "", "", 0),
		[]string{"eng:2", "team:1"})
	wantSeq(t, "TagCounts(documents)", rdTagCounts(t, s, bankA, domain.TagSourceDocuments, "", "", 0),
		[]string{"doc-x:2", "doc-y:1"})
	rdCheckTagFilters(t, s)
	wantSeq(t, "TagCounts(other tenant, memories)", rdTagCounts(t, s, bankOtherTenant, mem, "", "", 0),
		[]string{"a:2", "leak:1", "z:1"})

	_, err := s.Read().TagCounts(t.Context(), bankB, mem, "", "", 0)
	wantErrIs(t, "TagCounts(unknown bank)", err, domain.ErrNotFound)
	rdTombstone(t, s, bankA)
	for _, src := range []domain.TagSource{unspec, mem, domain.TagSourceMentalModels, domain.TagSourceDocuments} {
		_, err := s.Read().TagCounts(t.Context(), bankA, src, "", "", 0)
		wantErrIs(t, fmt.Sprintf("TagCounts(tombstoned bank, %q)", src), err, domain.ErrNotFound)
	}
	wantSeq(t, "TagCounts(other tenant) after bank A's tombstone",
		rdTagCounts(t, s, bankOtherTenant, domain.TagSourceDocuments, "", "", 0), []string{"doc-x:1", "leak:1"})
}

// rdCheckTagFilters checks prefix, after and limit on bank A's memory tags.
func rdCheckTagFilters(t *testing.T, s store.DB) {
	t.Helper()
	mem := domain.TagSourceMemories
	cases := []struct {
		prefix, after string
		limit         int
		want          []string
	}{
		{"a_", "", 0, []string{"a_b:1"}},
		{"a%", "", 0, []string{"a%b:1"}},
		{`a\`, "", 0, []string{`a\b:1`}},
		{"a", "", 0, []string{"a:4", "a%b:1", `a\b:1`, "a_b:1", "axb:1", "ayb:1"}},
		{"a", "a", 2, []string{"a%b:1", `a\b:1`}},
		{"a", "a_b", 0, []string{"axb:1", "ayb:1"}},
		{"", "z", 0, []string{"é:1"}},
		{"", "_x", 1, []string{"a:4"}},
		{"é", "", 0, []string{"é:1"}},
		{"q", "", 0, nil},
		{"", "é", 0, nil},
	}
	for _, c := range cases {
		wantSeq(t, fmt.Sprintf("TagCounts(prefix %q, after %q, limit %d)", c.prefix, c.after, c.limit),
			rdTagCounts(t, s, bankA, mem, c.prefix, c.after, c.limit), c.want)
	}
	wantSeq(t, "TagCounts(mental models, prefix t)",
		rdTagCounts(t, s, bankA, domain.TagSourceMentalModels, "t", "", 0), []string{"team:1"})
	wantSeq(t, "TagCounts(documents, after doc-x)",
		rdTagCounts(t, s, bankA, domain.TagSourceDocuments, "", "doc-x", 0), []string{"doc-y:1"})
}

// ---------- CountObservations ----------

// rdCountObs runs CountObservations through Read() and fails the test on error.
func rdCountObs(t *testing.T, s store.DB, k domain.ScopeKey) int {
	t.Helper()
	n, err := s.Read().CountObservations(t.Context(), k)
	return rdMust(t, "CountObservations", n, err)
}

// testRdCountObservations: only live observations whose tag set equals the scope's count (not superset
// scopes, facts or archived observations); unknown scopes and banks count 0; the other tenant's
// observations never count; a Tx read sees its own uncommitted observation.
func testRdCountObservations(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	arch := rdObs(bankA, "archived a", 6, "a")
	insertUnits(t, s, rdObs(bankA, "a1", 1, "a"), rdObs(bankA, "a2", 2, "a"), rdObs(bankA, "ab", 3, "a", "b"),
		rdObs(bankA, "shared", 4), rdFact(bankA, "fact a", 5, "a"), arch)
	rdArchive(t, s, bankA, arch.ID)
	insertUnits(t, s, rdObs(bankOtherTenant, "o1", 1, "a"), rdObs(bankOtherTenant, "o2", 2, "a"),
		rdObs(bankOtherTenant, "o3", 3, "a"), rdObs(bankOtherTenant, "o4", 4, "b"))

	cases := []struct {
		what string
		key  domain.ScopeKey
		want int
	}{
		{"scope a", rdScope(bankA, "a"), 2},
		{"scope a,b (unsorted key)", domain.ScopeKey{BankRef: bankA, Tags: []string{"b", "a"}}, 1},
		{"shared scope", rdScope(bankA), 1},
		{"scope b (only a superset scope holds b)", rdScope(bankA, "b"), 0},
		{"unknown scope", rdScope(bankA, "c"), 0},
		{"unknown bank", rdScope(bankB, "a"), 0},
		{"other tenant scope a", rdScope(bankOtherTenant, "a"), 3},
		{"other tenant scope b", rdScope(bankOtherTenant, "b"), 1},
	}
	for _, c := range cases {
		if got := rdCountObs(t, s, c.key); got != c.want {
			t.Errorf("CountObservations(%s) = %d, want %d", c.what, got, c.want)
		}
	}

	inTxCount := -1
	err := txErr(t, s, func(tx store.Tx) error {
		if _, err := stampAndInsert(t, tx, []domain.Unit{rdObs(bankA, "uncommitted", 7, "a")}); err != nil {
			return err
		}
		var err error
		if inTxCount, err = tx.CountObservations(t.Context(), rdScope(bankA, "a")); err != nil {
			return err
		}
		return errRollback
	})
	wantErrIs(t, "rolled-back tx", err, errRollback)
	if inTxCount != 3 {
		t.Errorf("Tx.CountObservations(scope a) after an uncommitted insert = %d, want 3", inTxCount)
	}
	if got := rdCountObs(t, s, rdScope(bankA, "a")); got != 2 {
		t.Errorf("CountObservations(scope a) after rollback = %d, want 2", got)
	}
}

// ---------- NewestSeqInScope ----------

// rdNewest runs NewestSeqInScope through Read() and fails the test on error.
func rdNewest(t *testing.T, s store.DB, q store.ScopeQuery) int64 {
	t.Helper()
	n, err := s.Read().NewestSeqInScope(t.Context(), q)
	return rdMust(t, "NewestSeqInScope", n, err)
}

// rdTagsAnyStrict is the predicate "carries one of tags" that excludes untagged units.
func rdTagsAnyStrict(tags ...string) *domain.TagPredicate {
	return &domain.TagPredicate{Op: domain.PredicateTags, Tags: tags, Match: domain.TagsMatchAnyStrict}
}

// rdSeqFixture holds the seqs of bank A's units, each written in its own transaction.
type rdSeqFixture struct {
	worldA, obsA, expB, untagged int64
}

// rdSeedSeqs writes the other tenant's units first (so their seqs run higher than bank A's), then bank A's:
// a world fact tagged a, an observation tagged a, an experience fact tagged b, an untagged world fact and
// a world fact tagged a that is archived (so it never counts).
func rdSeedSeqs(t *testing.T, s store.DB) rdSeqFixture {
	t.Helper()
	ensureBanks(t, s, bankA, bankOtherTenant)
	for i := range 8 {
		insertUnits(t, s, rdFact(bankOtherTenant, "other", i+1, "a", "b"))
	}
	var f rdSeqFixture
	f.worldA = insertUnits(t, s, rdFact(bankA, "world a", 1, "a"))
	f.obsA = insertUnits(t, s, rdObs(bankA, "obs a", 2, "a"))
	exp := rdFact(bankA, "experience b", 3, "b")
	exp.Type = domain.FactTypeExperience
	f.expB = insertUnits(t, s, exp)
	f.untagged = insertUnits(t, s, rdFact(bankA, "untagged", 4))
	arch := rdFact(bankA, "archived a", 5, "a")
	insertUnits(t, s, arch)
	rdArchive(t, s, bankA, arch.ID)
	return f
}

// testRdNewestSeqInScope: the highest seq of a live fact or observation matching the types, tag predicate
// and seq range (After exclusive, UpTo inclusive, UpTo 0 matches nothing); 0 when nothing matches or the
// bank is unknown; the other tenant's higher seqs never leak.
func testRdNewestSeqInScope(t *testing.T, s store.DB) {
	f := rdSeedSeqs(t, s)
	obs, world := domain.FactTypeObservation, domain.FactTypeWorld
	cases := []struct {
		what string
		q    store.ScopeQuery
		want int64
	}{
		{"everything", store.ScopeQuery{}, f.untagged},
		{"observations", store.ScopeQuery{Types: []domain.FactType{obs}}, f.obsA},
		{"experience", store.ScopeQuery{Types: []domain.FactType{domain.FactTypeExperience}}, f.expB},
		{"world and observation", store.ScopeQuery{Types: []domain.FactType{world, obs}}, f.untagged},
		{"tags a", store.ScopeQuery{Tags: rdTagsAnyStrict("a")}, f.obsA},
		{"tags a, world", store.ScopeQuery{Tags: rdTagsAnyStrict("a"), Types: []domain.FactType{world}}, f.worldA},
		{"tags b", store.ScopeQuery{Tags: rdTagsAnyStrict("b")}, f.expB},
		{"tags none match", store.ScopeQuery{Tags: rdTagsAnyStrict("zzz")}, 0},
		{"after expB", store.ScopeQuery{Seq: domain.SeqRange{After: f.expB}}, f.untagged},
		{"after untagged", store.ScopeQuery{Seq: domain.SeqRange{After: f.untagged}}, 0},
		{"up to expB", store.ScopeQuery{Seq: domain.SeqRange{UpTo: ptr(f.expB)}}, f.expB},
		{"(worldA, obsA]", store.ScopeQuery{Seq: domain.SeqUpTo(f.worldA, f.obsA)}, f.obsA},
		{"(obsA, obsA]", store.ScopeQuery{Seq: domain.SeqUpTo(f.obsA, f.obsA)}, 0},
		{"up to 0", store.ScopeQuery{Seq: domain.SeqUpTo(0, 0)}, 0},
		{"tags b, up to obsA", store.ScopeQuery{Tags: rdTagsAnyStrict("b"), Seq: domain.SeqUpTo(0, f.obsA)}, 0},
	}
	for _, c := range cases {
		c.q.Bank = bankA
		if got := rdNewest(t, s, c.q); got != c.want {
			t.Errorf("NewestSeqInScope(%s) = %d, want %d", c.what, got, c.want)
		}
	}
	if got := rdNewest(t, s, store.ScopeQuery{Bank: bankB}); got != 0 {
		t.Errorf("NewestSeqInScope(unknown bank) = %d, want 0", got)
	}
	if got := rdNewest(t, s, store.ScopeQuery{Bank: bankOtherTenant}); got <= f.untagged {
		t.Errorf("NewestSeqInScope(other tenant) = %d, want > bank A's %d (fixture assumption)", got, f.untagged)
	}
}

// ---------- ModelsCiting ----------

// rdModelsCiting runs ModelsCiting through Read() and fails the test on error.
func rdModelsCiting(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.UnitID) []domain.MentalModelID {
	t.Helper()
	ms, err := s.Read().ModelsCiting(t.Context(), b, ids)
	return rdMust(t, fmt.Sprintf("ModelsCiting(%v)", b), ms, err)
}

// testRdModelsCiting: the ids of the bank's models citing any input id, ordered by id and deduplicated;
// unknown and malformed ids and unknown banks give nothing; the other tenant's models (one with the same
// id) never leak.
func testRdModelsCiting(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	u1, u2, u3 := rdFact(bankA, "u1", 1), rdFact(bankA, "u2", 2), rdFact(bankA, "u3", 3)
	insertUnits(t, s, u1, u2, u3)
	rdCreateModel(t, s, bankA, "m-c", nil, u3.ID)
	rdCreateModel(t, s, bankA, "m-b", nil, u1.ID, u2.ID)
	rdCreateModel(t, s, bankA, "m-a", nil, u2.ID)
	rdCreateModel(t, s, bankA, "m-d", nil, newID[domain.UnitID]())
	rdCreateModel(t, s, bankOtherTenant, "m-a", nil, u3.ID)
	rdCreateModel(t, s, bankOtherTenant, "m-z", nil, u1.ID)

	wantSeq(t, "ModelsCiting(u2, u1)", rdModelsCiting(t, s, bankA, u2.ID, u1.ID), []domain.MentalModelID{"m-a", "m-b"})
	wantSeq(t, "ModelsCiting(u3, unknown, malformed)",
		rdModelsCiting(t, s, bankA, u3.ID, newID[domain.UnitID](), rdBadID), []domain.MentalModelID{"m-c"})
	wantSeq(t, "ModelsCiting(u1, u2, u3)", rdModelsCiting(t, s, bankA, u1.ID, u2.ID, u3.ID),
		[]domain.MentalModelID{"m-a", "m-b", "m-c"})
	if got := rdModelsCiting(t, s, bankA); len(got) != 0 {
		t.Errorf("ModelsCiting(no ids) = %v, want empty", got)
	}
	if got := rdModelsCiting(t, s, bankB, u1.ID, u2.ID, u3.ID); len(got) != 0 {
		t.Errorf("ModelsCiting(unknown bank) = %v, want empty", got)
	}
	wantSeq(t, "ModelsCiting(other tenant)", rdModelsCiting(t, s, bankOtherTenant, u1.ID, u2.ID, u3.ID),
		[]domain.MentalModelID{"m-a", "m-z"})
	var inTxModels []domain.MentalModelID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		inTxModels, err = tx.ModelsCiting(t.Context(), bankA, []domain.UnitID{u3.ID})
		return err
	})
	wantSeq(t, "Tx.ModelsCiting(u3)", inTxModels, []domain.MentalModelID{"m-c"})
}

// ---------- temporal relevance filter ----------

// testRdTemporalRelevance: with Embedding set and MinSimilarity > 0, in-window units below the floor and
// units without an embedding are dropped before Limit applies; MinSimilarity 0 or an empty Embedding
// turns the filter off; the other tenant's units never show up.
func testRdTemporalRelevance(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	rel, irr := dated(bankA, "relevant", day(70), day(71)), dated(bankA, "irrelevant", day(72), day(73))
	noEmb, outside := dated(bankA, "no embedding", day(74), day(75)), dated(bankA, "outside", day(10), day(11))
	rel.Embedding, irr.Embedding, outside.Embedding = angleVec(10), angleVec(80), angleVec(0)
	other := dated(bankOtherTenant, "other tenant", day(70), day(71))
	other.Embedding = angleVec(0)
	insertUnits(t, s, rel, irr, noEmb, outside)
	insertUnits(t, s, other)

	window := domain.TimeWindow{Start: day(60), End: day(90)}
	minSim := (cosDeg(10) + cosDeg(80)) / 2
	q := store.TemporalQuery{Bank: bankA, Window: window, Embedding: angleVec(0), MinSimilarity: minSim, Limit: 10}
	wantSet(t, "SearchTemporal(filter on)", hitIDs(searchTemporal(t, s, q)), []domain.UnitID{rel.ID})
	q.Limit = 1
	wantSeq(t, "SearchTemporal(filter on, Limit 1)", hitIDs(searchTemporal(t, s, q)), []domain.UnitID{rel.ID})
	all := []domain.UnitID{rel.ID, irr.ID, noEmb.ID}
	q.Limit, q.MinSimilarity = 10, 0
	wantSet(t, "SearchTemporal(MinSimilarity 0)", hitIDs(searchTemporal(t, s, q)), all)
	q.MinSimilarity, q.Embedding = minSim, nil
	wantSet(t, "SearchTemporal(nil Embedding)", hitIDs(searchTemporal(t, s, q)), all)
	q.Embedding = []float32{}
	wantSet(t, "SearchTemporal(empty Embedding)", hitIDs(searchTemporal(t, s, q)), all)
	q = store.TemporalQuery{Bank: bankOtherTenant, Window: window, Embedding: angleVec(0), MinSimilarity: minSim}
	q.Limit = 10
	wantSeq(t, "SearchTemporal(other tenant, filter on)", hitIDs(searchTemporal(t, s, q)), []domain.UnitID{other.ID})
}

// ---------- Document.ObservationScopeModes ----------

// rdUpsertScopedDoc upserts document id of bank b with the given observation scopes.
func rdUpsertScopedDoc(t *testing.T, s store.DB, b domain.BankRef, id domain.DocumentID,
	sc *domain.ObservationScopeModes,
) {
	t.Helper()
	d := domain.Document{
		ID: id, Bank: b, Tags: []string{}, BlobKey: blob.Key(b, id, "h0"), ObservationScopeModes: sc,
	}
	inTx(t, s, func(tx store.Tx) error { return tx.Documents().Upsert(t.Context(), d) })
}

// rdWantDocScopes fails the test unless document id of bank b carries want (nil = none).
func rdWantDocScopes(t *testing.T, s store.DB, what string, b domain.BankRef, id domain.DocumentID,
	want *domain.ObservationScopeModes,
) {
	t.Helper()
	d, err := getDocument(t, s, b, id)
	d = rdMust(t, what, d, err)
	if want == nil || d.ObservationScopeModes == nil {
		if want != d.ObservationScopeModes {
			t.Errorf("%s: ObservationScopeModes = %+v, want %+v", what, d.ObservationScopeModes, want)
		}
		return
	}
	wantJSON(t, what, d.ObservationScopeModes, want)
}

// testRdDocumentObservationScopes: Upsert stores the scopes and Get returns them; a second Upsert with nil
// clears them; a document that never had scopes stays nil; the same document id under the other tenant
// keeps its own scopes.
func testRdDocumentObservationScopes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	custom := &domain.ObservationScopeModes{
		Mode: domain.ScopeModeCustom,
		Custom: []domain.TagSet{
			{Tags: domain.NormalizeTags([]string{"a", "b"})}, {Tags: []string{"c"}},
		},
		MaxCombinations: ptr(3),
	}
	perTag := &domain.ObservationScopeModes{Mode: domain.ScopeModePerTag, Custom: []domain.TagSet{}}
	rdUpsertScopedDoc(t, s, bankA, "doc-1", custom)
	rdUpsertScopedDoc(t, s, bankA, "doc-2", nil)
	rdUpsertScopedDoc(t, s, bankOtherTenant, "doc-1", perTag)
	rdWantDocScopes(t, s, "doc-1 after Upsert(custom)", bankA, "doc-1", custom)
	rdWantDocScopes(t, s, "doc-2 after Upsert(nil)", bankA, "doc-2", nil)
	rdWantDocScopes(t, s, "other tenant doc-1", bankOtherTenant, "doc-1", perTag)

	rdUpsertScopedDoc(t, s, bankA, "doc-1", nil)
	rdUpsertScopedDoc(t, s, bankA, "doc-2", nil)
	rdWantDocScopes(t, s, "doc-1 after Upsert(nil)", bankA, "doc-1", nil)
	rdWantDocScopes(t, s, "doc-2 after a second Upsert(nil)", bankA, "doc-2", nil)
	rdWantDocScopes(t, s, "other tenant doc-1 after bank A's Upsert(nil)", bankOtherTenant, "doc-1", perTag)
}

// ---------- error regimes ----------

// rdWantIDKeyedEmpty checks that every id-keyed lookup on bank b returns empty with no error.
func rdWantIDKeyedEmpty(t *testing.T, s store.DB, what string, b domain.BankRef) {
	t.Helper()
	r := s.Read()
	ctx := t.Context()
	cs, err := r.GetChunks(ctx, b, []domain.ChunkID{newID[domain.ChunkID]()})
	rdWantEmpty(t, what+": GetChunks", len(cs), err)
	es, err := r.EntityStates(ctx, b, []domain.UnitID{newID[domain.UnitID]()})
	rdWantEmpty(t, what+": EntityStates", len(es), err)
	ks, err := r.StaleScopes(ctx, b, []domain.ScopeKey{rdScope(b), rdScope(b, "a")})
	rdWantEmpty(t, what+": StaleScopes", len(ks), err)
	n, err := r.CountObservations(ctx, rdScope(b))
	rdWantEmpty(t, what+": CountObservations", n, err)
	seq, err := r.NewestSeqInScope(ctx, store.ScopeQuery{Bank: b})
	rdWantEmpty(t, what+": NewestSeqInScope", int(seq), err)
	ms, err := r.ModelsCiting(ctx, b, []domain.UnitID{newID[domain.UnitID]()})
	rdWantEmpty(t, what+": ModelsCiting", len(ms), err)
}

// rdWantRepoLookupsEmpty checks the id-keyed repository lookups on bank b in a transaction that takes no
// Seq: UnitRepo.IDsByDocument and EvidenceRepo.CitingScopes return empty with no error.
func rdWantRepoLookupsEmpty(t *testing.T, s store.DB, what string, b domain.BankRef) {
	t.Helper()
	var ids []domain.UnitID
	var scopes []domain.ScopeKey
	var idsErr, scopesErr error
	inTx(t, s, func(tx store.Tx) error {
		ids, idsErr = tx.Units().IDsByDocument(t.Context(), b, "doc-1")
		scopes, scopesErr = tx.Evidence().CitingScopes(t.Context(), b, []domain.UnitID{newID[domain.UnitID](), rdBadID})
		return nil
	})
	rdWantEmpty(t, what+": IDsByDocument", len(ids), idsErr)
	rdWantEmpty(t, what+": CitingScopes", len(scopes), scopesErr)
}

// rdWantEmpty fails the test unless err is nil and n (a length or count) is 0.
func rdWantEmpty(t *testing.T, what string, n int, err error) {
	t.Helper()
	if err != nil || n != 0 {
		t.Errorf("%s = %d results, error %v; want 0 and no error", what, n, err)
	}
}

// rdWantTagCountsNotFound checks that TagCounts of every source on bank b is ErrNotFound.
func rdWantTagCountsNotFound(t *testing.T, s store.DB, what string, b domain.BankRef) {
	t.Helper()
	for _, src := range []domain.TagSource{
		domain.TagSourceUnspecified, domain.TagSourceMemories, domain.TagSourceMentalModels,
		domain.TagSourceDocuments,
	} {
		_, err := s.Read().TagCounts(t.Context(), b, src, "", "", 0)
		wantErrIs(t, fmt.Sprintf("%s: TagCounts(%q)", what, src), err, domain.ErrNotFound)
	}
}

// rdSeededLookups calls every id-keyed lookup on bank b with the ids of its seeded rows and fails the test
// on any error. 1500 says id-keyed lookups never check the bank, so a tombstoned bank's rows may come back
// or not; only the absence of an error is asserted.
func rdSeededLookups(t *testing.T, s store.DB, b domain.BankRef, ids []domain.UnitID) {
	t.Helper()
	r, ctx := s.Read(), t.Context()
	_, err1 := r.GetChunks(ctx, b, []domain.ChunkID{newID[domain.ChunkID]()})
	_, err2 := r.EntityStates(ctx, b, ids)
	_, err3 := r.StaleScopes(ctx, b, []domain.ScopeKey{rdScope(b), rdScope(b, "a")})
	_, err4 := r.CountObservations(ctx, rdScope(b, "a"))
	_, err5 := r.NewestSeqInScope(ctx, store.ScopeQuery{Bank: b})
	_, err6 := r.ModelsCiting(ctx, b, ids)
	if err := errors.Join(err1, err2, err3, err4, err5, err6); err != nil {
		t.Errorf("id-keyed lookups on tombstoned bank %v with rows: %v", b, err)
	}
}

// testRdErrorRegimes: on an unknown bank and on a tombstoned empty bank, the id-keyed lookups (GetChunks,
// EntityStates, StaleScopes, CountObservations, NewestSeqInScope, ModelsCiting, and the repositories'
// IDsByDocument and CitingScopes) return empty with no error, and TagCounts is ErrNotFound. On a tombstoned
// bank that still holds rows, the Reader's id-keyed lookups give no error (decision 1500: whether they return
// the rows is unspecified), and TagCounts is ErrNotFound.
func testRdErrorRegimes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	f, o := rdFact(bankOtherTenant, "pending", 1, "a"), rdObs(bankOtherTenant, "obs", 2, "a")
	insertUnits(t, s, f, o)
	rdCreateModel(t, s, bankOtherTenant, "m1", nil, f.ID)
	rdTombstone(t, s, bankA)
	rdTombstone(t, s, bankOtherTenant)

	rdWantIDKeyedEmpty(t, s, "unknown bank", bankB)
	rdWantIDKeyedEmpty(t, s, "tombstoned empty bank", bankA)
	rdWantRepoLookupsEmpty(t, s, "unknown bank", bankB)
	rdWantRepoLookupsEmpty(t, s, "tombstoned empty bank", bankA)
	rdSeededLookups(t, s, bankOtherTenant, []domain.UnitID{f.ID, o.ID})
	rdWantTagCountsNotFound(t, s, "unknown bank", bankB)
	rdWantTagCountsNotFound(t, s, "tombstoned empty bank", bankA)
	rdWantTagCountsNotFound(t, s, "tombstoned bank with rows", bankOtherTenant)
}
