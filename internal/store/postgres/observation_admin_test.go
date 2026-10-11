package postgres_test

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
	"time"
)

// oaAfterShared continues a scope listing after the shared scope, whose key "" cannot be a cursor ("" means
// "from the start"). Every other key starts with a tag length of at least 1, so "0" sorts after "" and
// before every other key.
const oaAfterShared = "0"

// TestObservationAdmin runs the contract tests of the observation administration methods:
// ListObservationScopes, UnitRepo.IDsByDocument, EvidenceRepo.CitingScopes, DeleteScopes and DeleteAllObservations,
// with the semantics the store interface comments state.
func TestObservationAdmin(t *testing.T) {
	t.Parallel()
	runStoreCases(t, []storeCase{
		{"list_scopes", testOaListScopes},
		{"list_scopes_unknown_or_tombstoned", testOaListScopesUnknown},
		{"ids_by_document", testOaIDsByDocument},
		{"citing_scopes", testOaCitingScopes},
		{"delete_scopes", testOaDeleteScopes},
		{"delete_all_observations", testOaDeleteAllObservations},
		{"tenant_isolation", testOaTenantIsolation},
	})
}

// ---------- fixtures ----------

// oaFact builds a world fact of bank b with the given tags and an embedding.
func oaFact(b domain.BankRef, text string, tags ...string) domain.Unit {
	u := fact(b, text)
	u.Tags, u.Embedding = domain.NormalizeTags(tags), angleVec(0)
	return u
}

// oaObserve upserts an observation of bank b in the scope given by tags (its Tags) citing srcs, in one
// transaction stamped with its Seq, and returns its id.
func oaObserve(t *testing.T, s store.DB, b domain.BankRef, srcs []domain.UnitID, tags ...string) domain.UnitID {
	t.Helper()
	o := observation(b, "observation", tags...)
	o.SourceIDs = srcs
	ev := make([]domain.Evidence, len(srcs))
	for i, u := range srcs {
		ev[i] = domain.Evidence{Unit: u, Quote: "quote of " + string(u)}
	}
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if o.Seq, err = tx.Seq(t.Context(), b); err != nil {
			return err
		}
		if err := tx.Units().UpsertObservation(t.Context(), o); err != nil {
			return err
		}
		return tx.Evidence().Add(t.Context(), o.ID, ev)
	})
	return o.ID
}

// oaMark stamps consolidated as consolidated and parked as consolidation-failed at the given times.
func oaMark(
	t *testing.T, s store.DB, consolidated []domain.UnitID, cAt time.Time, parked []domain.UnitID, pAt time.Time,
) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Units().MarkConsolidated(t.Context(), consolidated, cAt); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), parked, pAt)
	})
}

// oaArchive archives unit id of bank b in a transaction that takes b's Seq.
func oaArchive(t *testing.T, s store.DB, b domain.BankRef, id domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), b, id, "wrong")
	})
}

// oaScope is the scope of bank b with the given tags.
func oaScope(b domain.BankRef, tags ...string) domain.ScopeKey { return domain.NewScopeKey(b, tags) }

// oaListObservationScopes reads ListObservationScopes through Read() and returns its error unchanged.
func oaListObservationScopes(t *testing.T, s store.DB, b domain.BankRef, after string,
	limit int,
) ([]domain.ScopeInfo, error) {
	t.Helper()
	return s.Read().ListObservationScopes(t.Context(), b, after, limit)
}

// oaMustListScopes is oaListObservationScopes failing the test on error.
func oaMustListScopes(t *testing.T, s store.DB, b domain.BankRef, after string, limit int) []domain.ScopeInfo {
	t.Helper()
	infos, err := oaListObservationScopes(t, s, b, after, limit)
	if err != nil {
		t.Fatalf("ListObservationScopes(%v, %q, %d): %v", b, after, limit, err)
	}
	return infos
}

// oaKey is the scope key (ScopeKey.String) of a listed scope.
func oaKey(info domain.ScopeInfo) string { return domain.EncodeScopeTags(info.Scope.Tags) }

// oaKeys returns the scope keys of infos in order.
func oaKeys(infos []domain.ScopeInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = oaKey(info)
	}
	return out
}

// oaScopeKeys returns the String() of each scope in order.
func oaScopeKeys(ss []domain.ScopeKey) []string {
	out := make([]string, len(ss))
	for i, sk := range ss {
		out[i] = sk.String()
	}
	return out
}

// oaCounts maps a scope key to its listed {Observations, UnconsolidatedMemories}.
type oaCounts map[string][2]int64

// oaWantCounts fails the test unless infos list exactly the keys of want, each with its counts.
func oaWantCounts(t *testing.T, what string, infos []domain.ScopeInfo, want oaCounts) {
	t.Helper()
	got := oaCounts{}
	for _, info := range infos {
		got[oaKey(info)] = [2]int64{info.Observations, info.UnconsolidatedMemories}
	}
	if len(got) != len(want) {
		t.Errorf("%s: scopes %q, want %d scopes", what, oaKeys(infos), len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("%s: scope %q {observations, unconsolidated} = %v (listed %v), want %v", what, k, g, ok, w)
		}
	}
}

// oaPageAll lists every scope of b with pages of limit, continuing after each page's last key, and returns
// the keys in listing order.
func oaPageAll(t *testing.T, s store.DB, b domain.BankRef, limit int) []string {
	t.Helper()
	var keys []string
	after := ""
	for range 20 {
		page := oaMustListScopes(t, s, b, after, limit)
		if len(page) > limit {
			t.Errorf("ListObservationScopes(limit %d) returned %d scopes", limit, len(page))
		}
		if len(page) == 0 {
			return keys
		}
		keys = append(keys, oaKeys(page)...)
		if after = oaKey(page[len(page)-1]); after == "" {
			after = oaAfterShared
		}
	}
	t.Fatalf("ListObservationScopes(limit %d) never ran out of pages; keys so far %q", limit, keys)
	return nil
}

// oaIDsByDocument runs UnitRepo.IDsByDocument in a transaction.
func oaIDsByDocument(t *testing.T, s store.DB, b domain.BankRef, doc domain.DocumentID) []domain.UnitID {
	t.Helper()
	var ids []domain.UnitID
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ids, err = tx.Units().IDsByDocument(t.Context(), b, doc)
		return err
	})
	return ids
}

// oaCitingScopes runs EvidenceRepo.CitingScopes in a transaction.
func oaCitingScopes(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.UnitID) []domain.ScopeKey {
	t.Helper()
	var ss []domain.ScopeKey
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ss, err = tx.Evidence().CitingScopes(t.Context(), b, ids)
		return err
	})
	return ss
}

// oaDeleteScopes runs EvidenceRepo.DeleteScopes in a transaction that takes b's Seq.
func oaDeleteScopes(
	t *testing.T, s store.DB, b domain.BankRef, scopes []domain.ScopeKey,
) (deleted, reset []domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		var err error
		deleted, reset, err = tx.Evidence().DeleteScopes(t.Context(), b, scopes)
		return err
	})
	return deleted, reset
}

// oaDeleteAll runs EvidenceRepo.DeleteAllObservations in a transaction that takes b's Seq.
func oaDeleteAll(t *testing.T, s store.DB, b domain.BankRef) (deleted, reset []domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		var err error
		deleted, reset, err = tx.Evidence().DeleteAllObservations(t.Context(), b)
		return err
	})
	return deleted, reset
}

// oaWantSorted fails the test unless got is want sorted (want in any order).
func oaWantSorted(t *testing.T, what string, got, want []domain.UnitID) {
	t.Helper()
	wantSeq(t, what+" (sorted)", got, sorted(want))
}

// oaWantLive fails the test unless exactly want of ids are live in bank b.
func oaWantLive(t *testing.T, s store.DB, what string, b domain.BankRef, ids, want []domain.UnitID) {
	t.Helper()
	wantSet(t, what, unitIDs(getUnits(t, s, b, ids...)), want)
}

// oaStamps maps unit ids of bank b to their (consolidated_at, consolidation_failed_at) stamps.
func oaStamps(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.UnitID) map[domain.UnitID][2]*time.Time {
	t.Helper()
	out := map[domain.UnitID][2]*time.Time{}
	for _, u := range getUnits(t, s, b, ids...) {
		out[u.ID] = [2]*time.Time{u.ConsolidatedAt, u.ConsolidationFailedAt}
	}
	return out
}

// oaWantStamps fails the test unless unit id has the given consolidated_at and consolidation_failed_at.
func oaWantStamps(t *testing.T, what string, got map[domain.UnitID][2]*time.Time, id domain.UnitID, c, f *time.Time) {
	t.Helper()
	g, ok := got[id]
	if !ok {
		t.Errorf("%s: unit %s is not live", what, id)
		return
	}
	if !sameTimePtr(g[0], c) || !sameTimePtr(g[1], f) {
		t.Errorf("%s: unit %s stamps (consolidated, failed) = (%v, %v), want (%v, %v)", what, id, g[0], g[1], c, f)
	}
}

// ---------- ListObservationScopes ----------

// oaScopeFixture is the list_scopes data of bank A.
type oaScopeFixture struct {
	// fz is the consolidated {z} fact cited by oZ.
	fz domain.UnitID
	// oZ is the only observation of scope {z}.
	oZ domain.UnitID
}

// newOaScopeFixture seeds bank A with facts in every consolidation state and observations in four scopes
// ("", {x}, {x,y}, {z}), plus observations of bank B and of the other tenant's bank A that must not count.
func newOaScopeFixture(t *testing.T, s store.DB) oaScopeFixture {
	t.Helper()
	ensureBanks(t, s, bankA, bankB, bankOtherTenant)
	f1, f2, f3 := oaFact(bankA, "f1", "x"), oaFact(bankA, "f2", "x", "y"), oaFact(bankA, "f3", "y")
	f2.Embedding = nil // waiting for rederive still awaits consolidation
	f4, f5, f6, fz := oaFact(bankA, "f4", "x"), oaFact(bankA, "f5"), oaFact(bankA, "f6", "x"), oaFact(bankA, "fz", "z")
	insertUnits(t, s, f1, f2, f3, f4, f5, f6, fz)
	oaMark(t, s, []domain.UnitID{f3.ID, fz.ID}, day(5), []domain.UnitID{f4.ID}, day(6))
	oaArchive(t, s, bankA, f6.ID)
	oaObserve(t, s, bankA, []domain.UnitID{f3.ID})
	oaObserve(t, s, bankA, []domain.UnitID{f1.ID}, "x")
	oaObserve(t, s, bankA, []domain.UnitID{f3.ID}, "x")
	oaObserve(t, s, bankA, []domain.UnitID{f2.ID}, "y", "x")
	oZ := oaObserve(t, s, bankA, []domain.UnitID{fz.ID}, "z")
	for _, b := range []domain.BankRef{bankB, bankOtherTenant} {
		theirs := oaFact(b, "theirs", "x")
		insertUnits(t, s, theirs)
		oaObserve(t, s, b, []domain.UnitID{theirs.ID}, "x")
		oaObserve(t, s, b, []domain.UnitID{theirs.ID}, "w")
	}
	return oaScopeFixture{fz: fz.ID, oZ: oZ}
}

// testOaListScopes: keyset paging returns every scope once in byte order of its key; counts follow 1500
// (superset-tagged and unembedded unconsolidated facts count, consolidated, parked and archived ones don't,
// the shared scope counts every unconsolidated fact); a scope cleared by DeleteScopes still lists with 0
// observations; limit <= 0 is ErrInvalidArgument.
func testOaListScopes(t *testing.T, s store.DB) {
	f := newOaScopeFixture(t, s)
	keys := []string{"", "1:x", "1:x,1:y", "1:z"}
	all := oaMustListScopes(t, s, bankA, "", 10)
	wantSeq(t, "ListObservationScopes keys", oaKeys(all), keys)
	oaWantCounts(t, "ListObservationScopes", all, oaCounts{"": {1, 3}, "1:x": {2, 2}, "1:x,1:y": {1, 1}, "1:z": {1, 0}})
	for _, limit := range []int{1, 2} {
		wantSeq(t, fmt.Sprintf("ListObservationScopes paged by %d", limit), oaPageAll(t, s, bankA, limit), keys)
	}
	wantSeq(t, "ListObservationScopes after the shared scope", oaKeys(oaMustListScopes(t, s, bankA, oaAfterShared, 10)),
		keys[1:])
	wantSeq(t, "ListObservationScopes after {x}", oaKeys(oaMustListScopes(t, s, bankA, "1:x", 10)), keys[2:])
	for _, limit := range []int{0, -1} {
		_, err := oaListObservationScopes(t, s, bankA, "", limit)
		wantErrIs(t, "ListObservationScopes(limit <= 0)", err, domain.ErrInvalidArgument)
	}

	deleted, reset := oaDeleteScopes(t, s, bankA, []domain.ScopeKey{oaScope(bankA, "z")})
	wantSeq(t, "DeleteScopes({z}) deleted", deleted, []domain.UnitID{f.oZ})
	wantSeq(t, "DeleteScopes({z}) reset", reset, []domain.UnitID{f.fz})
	oaWantCounts(t, "ListObservationScopes after clearing {z}", oaMustListScopes(t, s, bankA, "", 10),
		oaCounts{"": {1, 4}, "1:x": {2, 2}, "1:x,1:y": {1, 1}, "1:z": {0, 1}})
}

// testOaListScopesUnknown: an unknown and a tombstoned bank are ErrNotFound; a live bank without scopes lists
// none.
func testOaListScopesUnknown(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	if got := oaMustListScopes(t, s, bankB, "", 10); len(got) != 0 {
		t.Errorf("ListObservationScopes(bank without observations) = %q, want none", oaKeys(got))
	}
	_, err := oaListObservationScopes(t, s, domain.BankRef{Tenant: "tenant-1", Bank: "unknown"}, "", 10)
	wantErrIs(t, "ListObservationScopes(unknown bank)", err, domain.ErrNotFound)
	_, err = oaListObservationScopes(t, s, bankOtherTenant, "", 10)
	wantErrIs(t, "ListObservationScopes(bank id known only under another tenant)", err, domain.ErrNotFound)
	f := oaFact(bankA, "f", "x")
	insertUnits(t, s, f)
	oaObserve(t, s, bankA, []domain.UnitID{f.ID}, "x")
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	_, err = oaListObservationScopes(t, s, bankA, "", 10)
	wantErrIs(t, "ListObservationScopes(tombstoned bank)", err, domain.ErrNotFound)
}

// ---------- UnitRepo.IDsByDocument ----------

// testOaIDsByDocument: the document's live units come back sorted, archived units and other documents,
// banks and tenants excluded; nothing is written (tags, seq and stamps unchanged).
func testOaIDsByDocument(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB, bankOtherTenant)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	upsertDocs(t, s, bankB, "doc-1")
	upsertDocs(t, s, bankOtherTenant, "doc-1")
	us := []domain.Unit{
		oaFact(bankA, "u1", "t1"), oaFact(bankA, "u2", "t2"), oaFact(bankA, "u3"), oaFact(bankA, "u4"),
		oaFact(bankA, "u5"),
	}
	for i, d := range []domain.DocumentID{"doc-1", "doc-1", "doc-1", "doc-1", "doc-2"} {
		us[i].DocumentID = d
	}
	insertUnits(t, s, us...)
	theirs, otherTenant := oaFact(bankB, "theirs"), oaFact(bankOtherTenant, "other tenant")
	theirs.DocumentID, otherTenant.DocumentID = "doc-1", "doc-1"
	insertUnits(t, s, theirs)
	insertUnits(t, s, otherTenant)
	oaMark(t, s, []domain.UnitID{us[0].ID}, day(3), []domain.UnitID{us[1].ID}, day(4))
	oaArchive(t, s, bankA, us[3].ID)

	live := []domain.UnitID{us[0].ID, us[1].ID, us[2].ID}
	before := getUnits(t, s, bankA, live...)
	seqBefore := bankSeq(t, s, bankA)
	oaWantSorted(t, "IDsByDocument(doc-1)", oaIDsByDocument(t, s, bankA, "doc-1"), live)
	after := getUnits(t, s, bankA, live...)
	if len(after) != len(before) {
		t.Fatalf("GetUnits after IDsByDocument = %d units, want %d", len(after), len(before))
	}
	for i := range before {
		b, a := before[i], after[i]
		if !domain.SameTagSet(a.Tags, b.Tags) || a.Seq != b.Seq || !sameTimePtr(a.ConsolidatedAt, b.ConsolidatedAt) ||
			!sameTimePtr(a.ConsolidationFailedAt, b.ConsolidationFailedAt) {
			t.Errorf("IDsByDocument changed unit %s: tags %v -> %v, seq %d -> %d, stamps (%v, %v) -> (%v, %v)",
				b.ID, b.Tags, a.Tags, b.Seq, a.Seq, b.ConsolidatedAt, b.ConsolidationFailedAt, a.ConsolidatedAt,
				a.ConsolidationFailedAt)
		}
	}
	if got := bankSeq(t, s, bankA); got != seqBefore {
		t.Errorf("BankSeq after IDsByDocument = %d, want %d", got, seqBefore)
	}
	wantSeq(t, "IDsByDocument(doc-2)", oaIDsByDocument(t, s, bankA, "doc-2"), []domain.UnitID{us[4].ID})
	wantSeq(t, "IDsByDocument(bank B doc-1)", oaIDsByDocument(t, s, bankB, "doc-1"), []domain.UnitID{theirs.ID})
	if got := oaIDsByDocument(t, s, bankA, "unknown"); len(got) != 0 {
		t.Errorf("IDsByDocument(unknown document) = %v, want none", got)
	}
}

// ---------- EvidenceRepo.CitingScopes ----------

// testOaCitingScopes: the distinct scopes of b's live observations citing any of ids, ordered by String(),
// each with BankRef b; unknown, malformed and uncited ids give none.
func testOaCitingScopes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	f1, f2, f3 := oaFact(bankA, "f1"), oaFact(bankA, "f2"), oaFact(bankA, "f3")
	insertUnits(t, s, f1, f2, f3)
	oaObserve(t, s, bankA, []domain.UnitID{f1.ID}, "y")
	oaObserve(t, s, bankA, []domain.UnitID{f1.ID}, "x")
	oaObserve(t, s, bankA, []domain.UnitID{f2.ID, f1.ID}, "x")
	oaObserve(t, s, bankA, []domain.UnitID{f2.ID})
	gone := oaObserve(t, s, bankA, []domain.UnitID{f1.ID}, "q")
	if deleted, _ := oaDeleteScopes(t, s, bankA, []domain.ScopeKey{oaScope(bankA, "q")}); len(deleted) != 1 ||
		deleted[0] != gone {
		t.Fatalf("DeleteScopes({q}) deleted %v, want [%s]", deleted, gone)
	}
	theirs := oaFact(bankB, "theirs")
	insertUnits(t, s, theirs)
	oaObserve(t, s, bankB, []domain.UnitID{theirs.ID}, "w")

	got := oaCitingScopes(t, s, bankA, f1.ID, f2.ID)
	wantSeq(t, "CitingScopes(f1, f2)", oaScopeKeys(got), []string{"", "1:x", "1:y"})
	for _, sk := range got {
		if sk.BankRef != bankA {
			t.Errorf("CitingScopes scope %q has BankRef %v, want %v", sk.String(), sk.BankRef, bankA)
		}
	}
	wantSeq(t, "CitingScopes(f2)", oaScopeKeys(oaCitingScopes(t, s, bankA, f2.ID)), []string{"", "1:x"})
	for what, ids := range map[string][]domain.UnitID{
		"uncited f3":            {f3.ID},
		"unknown and malformed": {newID[domain.UnitID](), "not-a-uuid"},
		"none":                  nil,
	} {
		if got := oaCitingScopes(t, s, bankA, ids...); len(got) != 0 {
			t.Errorf("CitingScopes(%s) = %q, want none", what, oaScopeKeys(got))
		}
	}
	if got := oaCitingScopes(t, s, bankB, f1.ID); len(got) != 0 {
		t.Errorf("CitingScopes(bank B, bank A's f1) = %q, want none", oaScopeKeys(got))
	}
}

// ---------- DeleteScopes and DeleteAllObservations ----------

// oaDeleteFixture is the delete_scopes data of bank A.
type oaDeleteFixture struct {
	f1, f2, f3, f4, f5 domain.UnitID
	oX, oXY, oY, oAll  domain.UnitID
	// theirs are the {x} observations of bank B and of the other tenant's bank A.
	theirs map[domain.BankRef]domain.UnitID
}

// newOaDeleteFixture seeds bank A with consolidated facts f1..f4 and parked f5, observations oX {x} (f1, f5),
// oXY {x,y} (f2, f1), oY {y} (f3) and oAll (shared, f4), and an {x} observation in bank B and in the other
// tenant's bank A.
func newOaDeleteFixture(t *testing.T, s store.DB) oaDeleteFixture {
	t.Helper()
	ensureBanks(t, s, bankA, bankB, bankOtherTenant)
	f1, f2, f3 := oaFact(bankA, "f1", "x"), oaFact(bankA, "f2", "x", "y"), oaFact(bankA, "f3", "y")
	f4, f5 := oaFact(bankA, "f4"), oaFact(bankA, "f5", "x")
	insertUnits(t, s, f1, f2, f3, f4, f5)
	oaMark(t, s, []domain.UnitID{f1.ID, f2.ID, f3.ID, f4.ID}, day(5), []domain.UnitID{f5.ID}, day(6))
	f := oaDeleteFixture{f1: f1.ID, f2: f2.ID, f3: f3.ID, f4: f4.ID, f5: f5.ID, theirs: map[domain.BankRef]domain.UnitID{}}
	f.oX = oaObserve(t, s, bankA, []domain.UnitID{f1.ID, f5.ID}, "x")
	f.oXY = oaObserve(t, s, bankA, []domain.UnitID{f2.ID, f1.ID}, "x", "y")
	f.oY = oaObserve(t, s, bankA, []domain.UnitID{f3.ID}, "y")
	f.oAll = oaObserve(t, s, bankA, []domain.UnitID{f4.ID})
	for _, b := range []domain.BankRef{bankB, bankOtherTenant} {
		theirs := oaFact(b, "theirs", "x")
		insertUnits(t, s, theirs)
		oaMark(t, s, []domain.UnitID{theirs.ID}, day(5), nil, day(6))
		f.theirs[b] = oaObserve(t, s, b, []domain.UnitID{theirs.ID}, "x")
	}
	return f
}

// obs returns the ids of bank A's observations.
func (f oaDeleteFixture) obs() []domain.UnitID { return []domain.UnitID{f.oX, f.oXY, f.oY, f.oAll} }

// facts returns the ids of bank A's facts.
func (f oaDeleteFixture) facts() []domain.UnitID {
	return []domain.UnitID{f.f1, f.f2, f.f3, f.f4, f.f5}
}

// wantTheirs fails the test unless the other banks' observations are still live.
func (f oaDeleteFixture) wantTheirs(t *testing.T, s store.DB, what string) {
	t.Helper()
	for b, id := range f.theirs {
		oaWantLive(t, s, what+" "+b.Tenant+"/"+b.Bank+" observation", b, []domain.UnitID{id}, []domain.UnitID{id})
	}
}

// testOaDeleteScopes: empty and nil scopes do nothing; only the named scopes' observations are deleted (a
// scope with another BankRef is matched under b); deleted and reset come back sorted; the reset sources lose
// consolidated_at but keep consolidation_failed_at; other observations, banks and tenants are untouched.
func testOaDeleteScopes(t *testing.T, s store.DB) {
	f := newOaDeleteFixture(t, s)
	for what, scopes := range map[string][]domain.ScopeKey{"empty": {}, "nil": nil} {
		deleted, reset := oaDeleteScopes(t, s, bankA, scopes)
		if len(deleted) != 0 || len(reset) != 0 {
			t.Errorf("DeleteScopes(%s) = %v, %v, want nothing", what, deleted, reset)
		}
	}
	oaWantLive(t, s, "observations after empty DeleteScopes", bankA, f.obs(), f.obs())
	st := oaStamps(t, s, bankA, f.facts()...)
	for _, id := range []domain.UnitID{f.f1, f.f2, f.f3, f.f4} {
		oaWantStamps(t, "after empty DeleteScopes", st, id, ptr(day(5)), nil)
	}

	scopes := []domain.ScopeKey{oaScope(bankOtherTenant, "x"), oaScope(bankA, "y", "x")}
	deleted, reset := oaDeleteScopes(t, s, bankA, scopes)
	oaWantSorted(t, "DeleteScopes({x}, {x,y}) deleted", deleted, []domain.UnitID{f.oX, f.oXY})
	oaWantSorted(t, "DeleteScopes({x}, {x,y}) reset", reset, []domain.UnitID{f.f1, f.f2, f.f5})
	oaWantLive(t, s, "bank A observations after DeleteScopes", bankA, f.obs(), []domain.UnitID{f.oY, f.oAll})
	st = oaStamps(t, s, bankA, f.facts()...)
	oaWantStamps(t, "reset f1", st, f.f1, nil, nil)
	oaWantStamps(t, "reset f2", st, f.f2, nil, nil)
	oaWantStamps(t, "reset parked f5", st, f.f5, nil, ptr(day(6)))
	oaWantStamps(t, "untouched f3", st, f.f3, ptr(day(5)), nil)
	oaWantStamps(t, "untouched f4", st, f.f4, ptr(day(5)), nil)
	f.wantTheirs(t, s, "after DeleteScopes")
}

// testOaDeleteAllObservations: every observation of the bank is deleted and its sources reset, while bank B's
// and the other tenant's bank A's observations stay; a second call finds nothing.
func testOaDeleteAllObservations(t *testing.T, s store.DB) {
	f := newOaDeleteFixture(t, s)
	deleted, reset := oaDeleteAll(t, s, bankA)
	oaWantSorted(t, "DeleteAllObservations deleted", deleted, f.obs())
	oaWantSorted(t, "DeleteAllObservations reset", reset, f.facts())
	oaWantLive(t, s, "bank A observations after DeleteAllObservations", bankA, f.obs(), nil)
	st := oaStamps(t, s, bankA, f.facts()...)
	for _, id := range []domain.UnitID{f.f1, f.f2, f.f3, f.f4} {
		oaWantStamps(t, "after DeleteAllObservations", st, id, nil, nil)
	}
	oaWantStamps(t, "parked f5 after DeleteAllObservations", st, f.f5, nil, ptr(day(6)))
	f.wantTheirs(t, s, "after DeleteAllObservations")
	for b := range f.theirs {
		if got := oaMustListScopes(t, s, b, "", 10); len(got) != 1 || got[0].Observations != 1 {
			t.Errorf("ListObservationScopes(%v) after bank A's DeleteAllObservations = %q, want {x} with 1 observation", b,
				oaKeys(got))
		}
	}
	deleted, reset = oaDeleteAll(t, s, bankA)
	if len(deleted) != 0 || len(reset) != 0 {
		t.Errorf("second DeleteAllObservations = %v, %v, want nothing", deleted, reset)
	}
}

// testOaTenantIsolation: with the same bank id under two tenants, ListObservationScopes, CitingScopes, IDsByDocument
// and
// DeleteScopes see and touch only the given tenant's bank.
func testOaTenantIsolation(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	units := map[domain.BankRef][]domain.UnitID{}
	obs := map[domain.BankRef][]domain.UnitID{}
	for n, b := range []domain.BankRef{bankA, bankOtherTenant} {
		upsertDocs(t, s, b, "doc-1")
		var us []domain.Unit
		for i := 0; i <= n; i++ { // bank A gets one unit, the other tenant two, so counts differ
			u := oaFact(b, "unit", "x")
			u.DocumentID = "doc-1"
			us = append(us, u)
		}
		insertUnits(t, s, us...)
		units[b] = unitIDs(us)
		obs[b] = []domain.UnitID{oaObserve(t, s, b, units[b], "x")}
	}
	oaWantCounts(t, "ListObservationScopes(bank A)", oaMustListScopes(t, s, bankA, "", 10), oaCounts{"1:x": {1, 1}})
	oaWantCounts(t, "ListObservationScopes(other tenant)", oaMustListScopes(t, s, bankOtherTenant, "", 10),
		oaCounts{"1:x": {1, 2}})
	oaWantSorted(t, "IDsByDocument(bank A)", oaIDsByDocument(t, s, bankA, "doc-1"), units[bankA])
	oaWantSorted(t, "IDsByDocument(other tenant)", oaIDsByDocument(t, s, bankOtherTenant, "doc-1"),
		units[bankOtherTenant])
	if got := oaCitingScopes(t, s, bankOtherTenant, units[bankA]...); len(got) != 0 {
		t.Errorf("CitingScopes(other tenant, bank A's units) = %q, want none", oaScopeKeys(got))
	}
	got := oaCitingScopes(t, s, bankA, units[bankA]...)
	if len(got) != 1 || got[0].BankRef != bankA || got[0].String() != "1:x" {
		t.Errorf("CitingScopes(bank A) = %+v, want [{x}] of bank A", got)
	}
	deleted, reset := oaDeleteScopes(t, s, bankOtherTenant, []domain.ScopeKey{oaScope(bankA, "x")})
	wantSeq(t, "DeleteScopes(other tenant, {x}) deleted", deleted, obs[bankOtherTenant])
	oaWantSorted(t, "DeleteScopes(other tenant, {x}) reset", reset, units[bankOtherTenant])
	oaWantLive(t, s, "bank A observation after the other tenant's DeleteScopes", bankA, obs[bankA], obs[bankA])
	oaWantCounts(t, "ListObservationScopes(bank A) after the other tenant's DeleteScopes",
		oaMustListScopes(t, s, bankA, "", 10), oaCounts{"1:x": {1, 1}})
}
