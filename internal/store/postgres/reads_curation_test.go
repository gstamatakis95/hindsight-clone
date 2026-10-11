package postgres_test

import (
	"cmp"
	"fmt"
	"maps"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
	"time"
)

// TestCurationReads runs the contract tests of the curation listings: GetUnit, ListUnits,
// ObservationHistory, BankStats, ListDocuments and ListMentalModels. The pinned semantics are
// the internal/store interface comments.
func TestCurationReads(t *testing.T) {
	t.Parallel()
	runStoreCases(t, []storeCase{
		{"get_unit", testCrGetUnit},
		{"list_units", testCrListUnits},
		{"observation_history", testCrObservationHistory},
		{"bank_stats", testCrBankStats},
		{"list_documents", testCrListDocuments},
		{"list_mental_models", testCrListMentalModels},
		{"error_regimes", testCrErrorRegimes},
		{"tenant_isolation", testCrTenantIsolation},
	})
}

// crUnknownBank is a bank no subtest creates.
var crUnknownBank = domain.BankRef{Tenant: "tenant-1", Bank: "bank-unknown"}

// ---------- shared helpers ----------

// crArchive archives unit id of bank b in a transaction that takes the bank's Seq.
func crArchive(t *testing.T, s store.DB, b domain.BankRef, id domain.UnitID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), b); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), b, id, "wrong fact")
	})
}

// crAttach attaches the entities to unit in its own transaction.
func crAttach(t *testing.T, s store.DB, unit domain.UnitID, ents ...domain.EntityID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Entities().Attach(t.Context(), unit, ents) })
}

// crTombstone marks bank b deleting.
func crTombstone(t *testing.T, s store.DB, b domain.BankRef) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), b, day(30)) })
}

// crEnqueue enqueues jobs in one transaction.
func crEnqueue(t *testing.T, s store.DB, jobs ...store.Job) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		for _, j := range jobs {
			if _, _, err := tx.Queue().Enqueue(t.Context(), j); err != nil {
				return err
			}
		}
		return nil
	})
}

// crGetUnit reads one unit through Read() and fails the test on error.
func crGetUnit(t *testing.T, s store.DB, b domain.BankRef, id domain.UnitID) domain.Unit {
	t.Helper()
	u, err := s.Read().GetUnit(t.Context(), b, id)
	if err != nil {
		t.Fatalf("GetUnit(%s): %v", id, err)
	}
	return u
}

// crListUnits runs ListUnits through Read() and fails the test on error.
func crListUnits(t *testing.T, s store.DB, q store.UnitQuery) []domain.Unit {
	t.Helper()
	us, err := s.Read().ListUnits(t.Context(), q)
	if err != nil {
		t.Fatalf("ListUnits(%+v): %v", q, err)
	}
	return us
}

// crBankStats runs BankStats through Read() and fails the test on error.
func crBankStats(t *testing.T, s store.DB, b domain.BankRef) domain.BankStats {
	t.Helper()
	st, err := s.Read().BankStats(t.Context(), b)
	if err != nil {
		t.Fatalf("BankStats(%v): %v", b, err)
	}
	return st
}

// crListDocs runs ListDocuments through Read() and fails the test on error.
func crListDocs(t *testing.T, s store.DB, q store.DocumentQuery) []domain.Document {
	t.Helper()
	ds, err := s.Read().ListDocuments(t.Context(), q)
	if err != nil {
		t.Fatalf("ListDocuments(%+v): %v", q, err)
	}
	return ds
}

// crListModels runs ListMentalModels through Read() and fails the test on error.
func crListModels(t *testing.T, s store.DB, q store.ModelQuery) ([]domain.MentalModel, string) {
	t.Helper()
	ms, next, err := s.Read().ListMentalModels(t.Context(), q)
	if err != nil {
		t.Fatalf("ListMentalModels(%+v): %v", q, err)
	}
	return ms, next
}

// crNewestFirst returns the ids of us in listing order: created_at, then id, both descending.
func crNewestFirst(us ...domain.Unit) []domain.UnitID {
	out := slices.Clone(us)
	slices.SortFunc(out, func(a, b domain.Unit) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return unitIDs(out)
}

// crExpect is how a reader must fill a unit's read-time fields.
type crExpect struct {
	state    domain.MemoryState
	entities []string
	sources  []domain.UnitID
	stale    bool
}

// crWantFill fails the test unless got's stored fields equal want's and its read-time fields equal e.
func crWantFill(t *testing.T, what string, got, want domain.Unit, e crExpect) {
	t.Helper()
	if got.ID != want.ID || got.Bank != want.Bank || got.Type != want.Type || got.Text != want.Text ||
		got.DocumentID != want.DocumentID || !slices.Equal(got.Tags, want.Tags) || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("%s: unit = %s %v %s %q %q %v %v, want %s %v %s %q %q %v %v", what, got.ID, got.Bank, got.Type,
			got.Text, got.DocumentID, got.Tags, got.CreatedAt, want.ID, want.Bank, want.Type, want.Text,
			want.DocumentID, want.Tags, want.CreatedAt)
	}
	if !sameTimePtr(got.MentionedAt, want.MentionedAt) || !sameTimePtr(got.OccurredStart, want.OccurredStart) ||
		!sameTimePtr(got.OccurredEnd, want.OccurredEnd) {
		t.Errorf("%s: mentioned/occurred = %v %v %v, want %v %v %v", what, got.MentionedAt, got.OccurredStart,
			got.OccurredEnd, want.MentionedAt, want.OccurredStart, want.OccurredEnd)
	}
	if got.State != e.state || got.Stale != e.stale {
		t.Errorf("%s: state/stale = %q %v, want %q %v", what, got.State, got.Stale, e.state, e.stale)
	}
	wantSet(t, what+": entities", got.Entities, e.entities)
	wantSet(t, what+": source ids", got.SourceIDs, e.sources)
}

// ---------- GetUnit ----------

// testCrGetUnit: GetUnit fills a live fact (State valid, Entities, no SourceIDs), a live observation
// (SourceIDs from evidence, Stale while an unconsolidated in-scope fact exists) and an archived unit (State
// invalidated, snapshot entity names after the entity was pruned). GetUnits leaves Stale false. Unknown or
// malformed ids and unknown, other or tombstoned banks are ErrNotFound.
func testCrGetUnit(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	f, gone := fact(bankA, "Ann Lee runs the lab"), fact(bankA, "Zed Quill visited")
	f.Tags, f.CreatedAt, f.MentionedAt = []string{"lab", "people"}, day(1), ptr(day(5))
	gone.Tags, gone.CreatedAt = []string{"other"}, day(3)
	insertUnits(t, s, f, gone)
	o := observation(bankA, "Ann runs things", "lab")
	o.CreatedAt = day(2)
	upsertObservation(t, s, &o)
	ann := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Ann Lee", Kind: "person"})
	zed := upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Zed Quill", Kind: "person"})
	crAttach(t, s, f.ID, ann)
	crAttach(t, s, gone.ID, zed)
	addEvidence(t, s, o.ID, f.ID)
	valid := domain.MemoryStateValid
	crWantFill(t, "GetUnit(live fact)", crGetUnit(t, s, bankA, f.ID), f,
		crExpect{state: valid, entities: []string{"Ann Lee"}})
	crWantFill(t, "GetUnit(observation, fact pending)", crGetUnit(t, s, bankA, o.ID), o,
		crExpect{state: valid, sources: []domain.UnitID{f.ID}, stale: true})
	if us := getUnits(t, s, bankA, o.ID); len(us) != 1 || us[0].Stale {
		t.Errorf("GetUnits(observation) = %+v, want one unit with Stale false", us)
	}
	inTx(t, s, func(tx store.Tx) error {
		return tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{f.ID}, day(6))
	})
	crWantFill(t, "GetUnit(observation, fact consolidated)", crGetUnit(t, s, bankA, o.ID), o,
		crExpect{state: valid, sources: []domain.UnitID{f.ID}})
	crArchive(t, s, bankA, gone.ID)
	if n := pruneOrphans(t, s, zed); n != 1 {
		t.Errorf("PruneOrphans(entity of the archived unit) = %d, want 1", n)
	}
	crWantFill(t, "GetUnit(archived)", crGetUnit(t, s, bankA, gone.ID), gone,
		crExpect{state: domain.MemoryStateInvalidated, entities: []string{"Zed Quill"}})
	crGetUnitInTx(t, s)
	crGetUnitMisses(t, s, f.ID)
}

// crGetUnitInTx: inside a transaction, GetUnit sees the transaction's own uncommitted unit.
func crGetUnitInTx(t *testing.T, s store.DB) {
	t.Helper()
	u := fact(bankA, "uncommitted")
	u.CreatedAt = day(7)
	var got domain.Unit
	err := txErr(t, s, func(tx store.Tx) error {
		if _, err := stampAndInsert(t, tx, []domain.Unit{u}); err != nil {
			return err
		}
		var err error
		if got, err = tx.GetUnit(t.Context(), bankA, u.ID); err != nil {
			return err
		}
		return errRollback
	})
	wantErrIs(t, "GetUnit inside the inserting tx", err, errRollback)
	if got.ID != u.ID {
		t.Errorf("GetUnit inside the inserting tx = %q, want %s", got.ID, u.ID)
	}
}

// crGetUnitMisses: unknown and malformed ids, an unknown bank, another bank and finally the tombstoned bank
// A are all ErrNotFound for GetUnit. live is a live unit of bank A.
func crGetUnitMisses(t *testing.T, s store.DB, live domain.UnitID) {
	t.Helper()
	misses := []struct {
		what string
		b    domain.BankRef
		id   domain.UnitID
	}{
		{"unknown id", bankA, newID[domain.UnitID]()},
		{"malformed id", bankA, "not-a-uuid"},
		{"unknown bank", crUnknownBank, live},
		{"another bank of the tenant", bankB, live},
	}
	for _, m := range misses {
		_, err := s.Read().GetUnit(t.Context(), m.b, m.id)
		wantErrIs(t, "GetUnit("+m.what+")", err, domain.ErrNotFound)
	}
	crTombstone(t, s, bankA)
	_, err := s.Read().GetUnit(t.Context(), bankA, live)
	wantErrIs(t, "GetUnit(tombstoned bank)", err, domain.ErrNotFound)
}

// ---------- ListUnits ----------

// crUnitFixture is list_units' bank A: a and c (archived) are in doc-1 and attached to ent; obs is an
// observation scoped to "red", stale because of a; p and q share a created_at.
type crUnitFixture struct {
	a, b, c, obs, p, q domain.Unit
	ent                domain.EntityID
	entName            string
}

// all returns every unit of the fixture, archived c included.
func (f crUnitFixture) all() []domain.Unit { return []domain.Unit{f.a, f.b, f.c, f.obs, f.p, f.q} }

// live returns the fixture's live units.
func (f crUnitFixture) live() []domain.Unit { return []domain.Unit{f.a, f.b, f.obs, f.p, f.q} }

// crSeedListUnits writes list_units' fixture into bank A.
func crSeedListUnits(t *testing.T, s store.DB) crUnitFixture {
	t.Helper()
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	f := crUnitFixture{entName: "Alice Moreau"}
	f.a = fact(bankA, "Alice likes 100% Coffee")
	f.a.DocumentID, f.a.Tags, f.a.CreatedAt = "doc-1", []string{"red"}, day(1)
	f.a.MentionedAt, f.a.OccurredStart = ptr(day(10)), ptr(day(20))
	f.b = fact(bankA, "Bob drinks tea")
	f.b.Type, f.b.Tags, f.b.CreatedAt = domain.FactTypeExperience, []string{"blue"}, day(2)
	f.b.OccurredStart, f.b.OccurredEnd = ptr(day(21)), ptr(day(22))
	f.c = fact(bankA, "coffee beans from Alice")
	f.c.DocumentID, f.c.Tags, f.c.CreatedAt, f.c.MentionedAt = "doc-1", []string{"blue", "red"}, day(3), ptr(day(12))
	f.obs = observation(bankA, "Coffee drinkers", "red")
	f.obs.CreatedAt = day(4)
	f.p, f.q = fact(bankA, "plain one"), fact(bankA, "plain two")
	f.p.CreatedAt, f.q.CreatedAt = day(5), day(5)
	insertUnits(t, s, f.all()...)
	f.ent = upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: f.entName, Kind: "person"})
	crAttach(t, s, f.a.ID, f.ent)
	crAttach(t, s, f.c.ID, f.ent)
	addEvidence(t, s, f.obs.ID, f.a.ID)
	crArchive(t, s, bankA, f.c.ID)
	return f
}

// crUnitCase is one ListUnits filter and the units it must return (in any order; the test sorts them).
type crUnitCase struct {
	name string
	q    store.UnitQuery
	want []domain.Unit
}

// crUnitFilterCases are list_units' filter cases over the fixture.
func crUnitFilterCases(f crUnitFixture) []crUnitCase {
	tq := func(field domain.TimeField, start, end *time.Time) store.UnitQuery {
		return store.UnitQuery{TimeField: field, Start: start, End: end}
	}
	tags := func(match domain.TagsMatch, ts ...string) store.UnitQuery {
		return store.UnitQuery{Tags: ts, TagsMatch: match}
	}
	u := func(us ...domain.Unit) []domain.Unit { return us }
	return []crUnitCase{
		{"State valid", store.UnitQuery{State: domain.MemoryStateValid}, f.live()},
		{"State invalidated", store.UnitQuery{State: domain.MemoryStateInvalidated}, u(f.c)},
		{"Types experience", store.UnitQuery{Types: []domain.FactType{domain.FactTypeExperience}}, u(f.b)},
		{"Types observation", store.UnitQuery{Types: []domain.FactType{domain.FactTypeObservation}}, u(f.obs)},
		{"Types world+experience", store.UnitQuery{
			Types: []domain.FactType{domain.FactTypeWorld, domain.FactTypeExperience},
		}, u(f.a, f.b, f.c, f.p, f.q)},
		{"Q COFFEE (case-insensitive)", store.UnitQuery{TextContains: "COFFEE"}, u(f.a, f.c, f.obs)},
		{"Q % (literal)", store.UnitQuery{TextContains: "%"}, u(f.a)},
		{"Q _ (literal)", store.UnitQuery{TextContains: "_"}, nil},
		{"Q coffee, State valid", store.UnitQuery{TextContains: "coffee", State: domain.MemoryStateValid}, u(f.a, f.obs)},
		{"DocumentID", store.UnitQuery{DocumentID: "doc-1"}, u(f.a, f.c)},
		{"EntityID (live units only)", store.UnitQuery{EntityID: f.ent}, u(f.a)},
		{"Tags red any_strict", tags(domain.TagsMatchAnyStrict, "red"), u(f.a, f.c, f.obs)},
		{"Tags blue+red all_strict", tags(domain.TagsMatchAllStrict, "blue", "red"), u(f.c)},
		{"Tags blue any (untagged pass)", tags(domain.TagsMatchAny, "blue"), u(f.b, f.c, f.p, f.q)},
		{"created_at [2, 4)", tq(domain.TimeFieldCreatedAt, ptr(day(2)), ptr(day(4))), u(f.b, f.c)},
		{"unspecified field [2, 4)", tq(domain.TimeFieldUnspecified, ptr(day(2)), ptr(day(4))), u(f.b, f.c)},
		{"created_at >= 5", tq(domain.TimeFieldCreatedAt, ptr(day(5)), nil), u(f.p, f.q)},
		{"mentioned_at [10, 12)", tq(domain.TimeFieldMentionedAt, ptr(day(10)), ptr(day(12))), u(f.a)},
		{"mentioned_at >= 11", tq(domain.TimeFieldMentionedAt, ptr(day(11)), nil), u(f.c)},
		{"mentioned_at < 100", tq(domain.TimeFieldMentionedAt, nil, ptr(day(100))), u(f.a, f.c)},
		{"occurred_start >= 20", tq(domain.TimeFieldOccurredStart, ptr(day(20)), nil), u(f.a, f.b)},
		{"occurred_end < 23", tq(domain.TimeFieldOccurredEnd, nil, ptr(day(23))), u(f.b)},
		{"updated_at >= epoch", tq(domain.TimeFieldUpdatedAt, ptr(day(0)), nil), f.all()},
		{"updated_at < epoch", tq(domain.TimeFieldUpdatedAt, nil, ptr(day(0))), nil},
	}
}

// crCheckListedFill checks every listed unit against the fixture: stored fields, State, Entities (the
// archived unit's snapshot) and Stale (set on the observation only).
func crCheckListedFill(t *testing.T, got []domain.Unit, f crUnitFixture) {
	t.Helper()
	byID := map[domain.UnitID]domain.Unit{}
	for _, u := range f.all() {
		byID[u.ID] = u
	}
	for _, u := range got {
		e := crExpect{state: domain.MemoryStateValid}
		switch u.ID {
		case f.a.ID:
			e.entities = []string{f.entName}
		case f.c.ID:
			e.state, e.entities = domain.MemoryStateInvalidated, []string{f.entName}
		case f.obs.ID:
			e.stale, e.sources = true, []domain.UnitID{f.a.ID}
		}
		crWantFill(t, "ListUnits "+u.Text, u, byID[u.ID], e)
	}
}

// crPageUnits lists q page by page (Limit limit, After the last unit's cursor) until an empty page and
// returns every id in order.
func crPageUnits(t *testing.T, s store.DB, q store.UnitQuery, limit int) []domain.UnitID {
	t.Helper()
	var out []domain.UnitID
	q.Limit = limit
	for range 100 {
		page := crListUnits(t, s, q)
		if len(page) > limit {
			t.Errorf("ListUnits(Limit %d) returned %d units", limit, len(page))
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, unitIDs(page)...)
		last := page[len(page)-1]
		q.After = &store.UnitCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	t.Fatalf("ListUnits paging did not end after 100 pages")
	return out
}

// testCrListUnits: ListUnits reads both tables in one created_at DESC, id DESC order, applies every filter
// (State, Types, Q, DocumentID, EntityID, Tags, time bounds with null fields never matching), fills like
// GetUnit, pages with After exactly once, and rejects a limit <= 0.
func testCrListUnits(t *testing.T, s store.DB) {
	f := crSeedListUnits(t, s)
	all := crListUnits(t, s, store.UnitQuery{Bank: bankA, Limit: 50})
	wantSeq(t, "ListUnits(all)", unitIDs(all), crNewestFirst(f.all()...))
	crCheckListedFill(t, all, f)
	for _, c := range crUnitFilterCases(f) {
		c.q.Bank, c.q.Limit = bankA, 50
		wantSeq(t, "ListUnits("+c.name+")", unitIDs(crListUnits(t, s, c.q)), crNewestFirst(c.want...))
	}
	for _, limit := range []int{1, 2, 4} {
		wantSeq(t, fmt.Sprintf("ListUnits paged by %d", limit), crPageUnits(t, s, store.UnitQuery{Bank: bankA}, limit),
			crNewestFirst(f.all()...))
	}
	valid := store.UnitQuery{Bank: bankA, State: domain.MemoryStateValid}
	wantSeq(t, "ListUnits(valid) paged by 2", crPageUnits(t, s, valid, 2), crNewestFirst(f.live()...))
	for _, limit := range []int{0, -1} {
		_, err := s.Read().ListUnits(t.Context(), store.UnitQuery{Bank: bankA, Limit: limit})
		wantErrIs(t, fmt.Sprintf("ListUnits(Limit %d)", limit), err, domain.ErrInvalidArgument)
	}
}

// ---------- ObservationHistory ----------

// testCrObservationHistory: ObservationHistory returns the versions newest first whatever the write order and
// pages by version; an unknown, malformed or version-less id is ErrNotFound.
func testCrObservationHistory(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	f, o := fact(bankA, "source"), observation(bankA, "obs")
	f.CreatedAt, o.CreatedAt = day(1), day(2)
	insertUnits(t, s, f, o)
	versions := map[int64]domain.ObservationVersion{}
	inTx(t, s, func(tx store.Tx) error {
		for _, n := range []int64{2, 1, 3} {
			v := domain.ObservationVersion{
				Version: n, Text: fmt.Sprintf("obs v%d", n), SourceFactIDs: []domain.UnitID{f.ID}, CreatedAt: day(int(n)),
			}
			versions[n] = v
			if err := tx.Units().RecordObservationVersion(t.Context(), bankA, o.ID, v); err != nil {
				return err
			}
		}
		return nil
	})
	got, err := s.Read().ObservationHistory(t.Context(), bankA, o.ID, 0, 100)
	if err != nil {
		t.Fatalf("ObservationHistory: %v", err)
	}
	var nums []int64
	for _, v := range got {
		nums = append(nums, v.Version)
		want := versions[v.Version]
		if v.Text != want.Text || !v.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("version %d = %q %v, want %q %v", v.Version, v.Text, v.CreatedAt, want.Text, want.CreatedAt)
		}
		wantSet(t, fmt.Sprintf("version %d source ids", v.Version), v.SourceFactIDs, want.SourceFactIDs)
	}
	wantSeq(t, "ObservationHistory versions", nums, []int64{3, 2, 1})
	// The keyset: below version 3, one row; below the oldest version, an empty result rather than NotFound.
	if got, err := s.Read().ObservationHistory(t.Context(), bankA, o.ID, 3, 1); err != nil || len(got) != 1 ||
		got[0].Version != 2 {
		t.Errorf("ObservationHistory(before 3, limit 1) = %v, %v; want version 2", got, err)
	}
	if got, err := s.Read().ObservationHistory(t.Context(), bankA, o.ID, 1, 100); err != nil || len(got) != 0 {
		t.Errorf("ObservationHistory(before 1) = %v, %v; want empty", got, err)
	}
	for what, id := range map[string]domain.UnitID{
		"unknown id": newID[domain.UnitID](), "malformed id": "not-a-uuid", "a fact without versions": f.ID,
	} {
		_, err := s.Read().ObservationHistory(t.Context(), bankA, id, 0, 100)
		wantErrIs(t, "ObservationHistory("+what+")", err, domain.ErrNotFound)
	}
}

// ---------- BankStats ----------

// crWantStats fails the test unless got equals want field by field (nil and empty maps are equal).
func crWantStats(t *testing.T, what string, got, want domain.BankStats) {
	t.Helper()
	if !maps.Equal(got.MemoriesByFactType, want.MemoriesByFactType) {
		t.Errorf("%s: MemoriesByFactType = %v, want %v", what, got.MemoriesByFactType, want.MemoriesByFactType)
	}
	if !maps.Equal(got.PendingOperationsByKind, want.PendingOperationsByKind) {
		t.Errorf("%s: PendingOperationsByKind = %v, want %v", what, got.PendingOperationsByKind,
			want.PendingOperationsByKind)
	}
	counts := func(st domain.BankStats) []int64 {
		return []int64{
			st.InvalidatedMemories, st.Documents, st.Entities, st.MentalModels, st.UnconsolidatedMemories,
			st.ChangeSeq,
		}
	}
	gotN, wantN := counts(got), counts(want)
	if !slices.Equal(gotN, wantN) {
		t.Errorf("%s: invalidated/documents/entities/models/unconsolidated/change_seq = %v, want %v", what, gotN,
			wantN)
	}
	if !sameTimePtr(got.LastMemoryWriteAt, want.LastMemoryWriteAt) {
		t.Errorf("%s: LastMemoryWriteAt = %v, want %v", what, got.LastMemoryWriteAt, want.LastMemoryWriteAt)
	}
}

// crWithBankRow sets want's LastMemoryWriteAt from Banks().Get and ChangeSeq from BankSeq.
func crWithBankRow(t *testing.T, s store.DB, b domain.BankRef, want domain.BankStats) domain.BankStats {
	t.Helper()
	want.LastMemoryWriteAt, want.ChangeSeq = mustBank(t, s, b).LastMemoryWriteAt, bankSeq(t, s, b)
	return want
}

// testCrBankStats: every BankStats field matches the seeded data; an empty bank has zero counts.
func testCrBankStats(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	upsertDocs(t, s, bankA, "doc-1", "doc-2")
	w1, w2, w3, e1 := fact(bankA, "w1"), fact(bankA, "w2"), fact(bankA, "w3"), fact(bankA, "e1")
	o := observation(bankA, "o")
	for i, u := range []*domain.Unit{&w1, &w2, &w3, &e1, &o} {
		u.CreatedAt = day(i + 1)
	}
	e1.Type = domain.FactTypeExperience
	insertUnits(t, s, w1, w2, w3, e1, o)
	crArchive(t, s, bankA, w3.ID)
	inTx(t, s, func(tx store.Tx) error {
		if err := tx.Units().MarkConsolidated(t.Context(), []domain.UnitID{w1.ID}, day(1)); err != nil {
			return err
		}
		return tx.Units().MarkConsolidationFailed(t.Context(), []domain.UnitID{e1.ID}, day(1))
	})
	upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Ann Lee", Kind: "person"})
	upsertEntity(t, s, domain.Entity{Bank: bankA, CanonicalName: "Bob Ray", Kind: "person"})
	crCreateModels(t, s, bankA, crModelSeeds()[:2])
	crEnqueue(t, s, doc("doc-1"), doc("doc-2"), job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`))
	want := crWithBankRow(t, s, bankA, domain.BankStats{
		MemoriesByFactType: map[domain.FactType]int64{
			domain.FactTypeWorld: 2, domain.FactTypeExperience: 1, domain.FactTypeObservation: 1,
		},
		InvalidatedMemories: 1, Documents: 2, Entities: 2, MentalModels: 2,
		PendingOperationsByKind: map[domain.OperationKind]int64{
			domain.OperationKindRetainDocument: 2, domain.OperationKindConsolidate: 1,
		},
		UnconsolidatedMemories: 1,
	})
	if want.LastMemoryWriteAt == nil || want.ChangeSeq == 0 {
		t.Errorf("bank A after writes: LastMemoryWriteAt %v, ChangeSeq %d; want both set", want.LastMemoryWriteAt,
			want.ChangeSeq)
	}
	crWantStats(t, "BankStats(bank A)", crBankStats(t, s, bankA), want)
	crWantStats(t, "BankStats(empty bank B)", crBankStats(t, s, bankB), crWithBankRow(t, s, bankB, domain.BankStats{}))
}

// ---------- ListDocuments ----------

// crDocSeed is one document list_documents writes, in its own transaction.
type crDocSeed struct {
	id     domain.DocumentID
	tags   []string
	scopes *domain.ObservationScopeModes
}

// crDocSeeds are list_documents' documents in creation order.
func crDocSeeds() []crDocSeed {
	scopes := &domain.ObservationScopeModes{Mode: domain.ScopeModeCustom, Custom: []domain.TagSet{{Tags: []string{"x"}}}}
	return []crDocSeed{
		{"Doc-Alpha", []string{"red"}, nil},
		{"doc-beta", []string{"blue"}, nil},
		{"other_100%", []string{}, nil},
		{"doc-delta", []string{}, scopes},
	}
}

// crSeedDocuments writes list_documents' fixture into bank A (each document in its own transaction, then a
// retag of Doc-Alpha and units in Doc-Alpha and doc-beta) and returns the documents as Get returns them,
// newest first.
func crSeedDocuments(t *testing.T, s store.DB) []domain.Document {
	t.Helper()
	ensureBanks(t, s, bankA)
	for _, sd := range crDocSeeds() {
		inTx(t, s, func(tx store.Tx) error {
			return tx.Documents().Upsert(t.Context(), domain.Document{
				ID: sd.id, Bank: bankA, Tags: sd.tags, BlobKey: blob.Key(bankA, sd.id, "h0"),
				ObservationScopeModes: sd.scopes,
			})
		})
	}
	inTx(t, s, func(tx store.Tx) error {
		return tx.Documents().SetTags(t.Context(), bankA, "Doc-Alpha", []string{"green", "red"})
	})
	u1, u2, u3, u4 := fact(bankA, "u1"), fact(bankA, "u2"), fact(bankA, "u3"), fact(bankA, "u4")
	u1.DocumentID, u2.DocumentID, u3.DocumentID, u4.DocumentID = "Doc-Alpha", "Doc-Alpha", "doc-beta", "doc-beta"
	u2.Type = domain.FactTypeExperience
	obs := observation(bankA, "obs")
	for i, u := range []*domain.Unit{&u1, &u2, &u3, &u4, &obs} {
		u.CreatedAt = day(i + 1)
	}
	insertUnits(t, s, u1, u2, u3, u4, obs)
	crArchive(t, s, bankA, u4.ID)
	var docs []domain.Document
	for _, sd := range crDocSeeds() {
		d, err := getDocument(t, s, bankA, sd.id)
		if err != nil {
			t.Fatalf("Documents().Get(%s): %v", sd.id, err)
		}
		docs = append(docs, d)
	}
	slices.SortFunc(docs, func(a, b domain.Document) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return docs
}

// crDocKeep returns the ids of docs (kept in order) for which keep holds.
func crDocKeep(docs []domain.Document, keep func(domain.Document) bool) []domain.DocumentID {
	out := []domain.DocumentID{}
	for _, d := range docs {
		if keep(d) {
			out = append(out, d.ID)
		}
	}
	return out
}

// crDocsIn returns the ids of docs (kept in order) that are among ids.
func crDocsIn(docs []domain.Document, ids ...domain.DocumentID) []domain.DocumentID {
	return crDocKeep(docs, func(d domain.Document) bool { return slices.Contains(ids, d.ID) })
}

// crDocIDs returns the ids of ds in order.
func crDocIDs(ds []domain.Document) []domain.DocumentID {
	out := make([]domain.DocumentID, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

// crDocCase is one ListDocuments filter and the ids it must return, in order.
type crDocCase struct {
	name string
	q    store.DocumentQuery
	want []domain.DocumentID
}

// crDocFilterCases are list_documents' filter cases; time bounds come from the stored timestamps.
func crDocFilterCases(docs []domain.Document) []crDocCase {
	byID := map[domain.DocumentID]domain.Document{}
	for _, d := range docs {
		byID[d.ID] = d
	}
	cStart, cEnd, alpha := byID["doc-beta"].CreatedAt, byID["doc-delta"].CreatedAt, byID["Doc-Alpha"].UpdatedAt
	in := func(ids ...domain.DocumentID) []domain.DocumentID { return crDocsIn(docs, ids...) }
	return []crDocCase{
		{"Q DOC- (case-insensitive)", store.DocumentQuery{DocumentIDContains: "DOC-"}, in("Doc-Alpha", "doc-beta",
			"doc-delta")},
		{"Q % (literal)", store.DocumentQuery{DocumentIDContains: "%"}, in("other_100%")},
		{"Q _ (literal)", store.DocumentQuery{DocumentIDContains: "_"}, in("other_100%")},
		{"Q alpha", store.DocumentQuery{DocumentIDContains: "alpha"}, in("Doc-Alpha")},
		{
			"Tags red any_strict",
			store.DocumentQuery{Tags: []string{"red"}, TagsMatch: domain.TagsMatchAnyStrict},
			in("Doc-Alpha"),
		},
		{"Tags green+red all_strict", store.DocumentQuery{
			Tags: []string{"green", "red"}, TagsMatch: domain.TagsMatchAllStrict,
		}, in("Doc-Alpha")},
		{
			"Tags blue any (untagged pass)",
			store.DocumentQuery{Tags: []string{"blue"}, TagsMatch: domain.TagsMatchAny},
			in("doc-beta", "other_100%", "doc-delta"),
		},
		{"created_at [beta, delta)", store.DocumentQuery{Start: &cStart, End: &cEnd}, crDocKeep(docs,
			func(d domain.Document) bool { return !d.CreatedAt.Before(cStart) && d.CreatedAt.Before(cEnd) })},
		{
			"created_at >= beta",
			store.DocumentQuery{TimeField: domain.DocumentTimeFieldCreatedAt, Start: &cStart},
			crDocKeep(docs, func(d domain.Document) bool { return !d.CreatedAt.Before(cStart) }),
		},
		{
			"updated_at >= alpha's retag",
			store.DocumentQuery{TimeField: domain.DocumentTimeFieldUpdatedAt, Start: &alpha},
			crDocKeep(docs, func(d domain.Document) bool { return !d.UpdatedAt.Before(alpha) }),
		},
		{
			"updated_at < alpha's retag",
			store.DocumentQuery{TimeField: domain.DocumentTimeFieldUpdatedAt, End: &alpha},
			crDocKeep(docs, func(d domain.Document) bool { return d.UpdatedAt.Before(alpha) }),
		},
	}
}

// crWantDocument fails the test unless a listed document equals the one Get returned, with the given live
// unit counts per fact type.
func crWantDocument(t *testing.T, got, want domain.Document, counts map[domain.FactType]int64) {
	t.Helper()
	compareDocument(t, "ListDocuments "+string(want.ID), got, want)
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		!sameTimePtr(got.ExtractedAt, want.ExtractedAt) {
		t.Errorf("document %s times = %v %v %v, want %v %v %v", want.ID, got.CreatedAt, got.UpdatedAt,
			got.ExtractedAt, want.CreatedAt, want.UpdatedAt, want.ExtractedAt)
	}
	if got.ContentHash != want.ContentHash || got.OverridesHash != want.OverridesHash {
		t.Errorf("document %s hashes = %q %q, want %q %q", want.ID, got.ContentHash, got.OverridesHash,
			want.ContentHash, want.OverridesHash)
	}
	wantJSON(t, "document "+string(want.ID)+" overrides", got.Overrides, want.Overrides)
	wantJSON(t, "document "+string(want.ID)+" observation scopes", got.ObservationScopeModes, want.ObservationScopeModes)
	var total int64
	for _, n := range counts {
		total += n
	}
	if got.MemoryCount != total || !maps.Equal(got.MemoriesByFactType, counts) {
		t.Errorf("document %s counts = %d %v, want %d %v", want.ID, got.MemoryCount, got.MemoriesByFactType, total, counts)
	}
}

// crPageDocs lists bank A's documents page by page (Limit limit) until an empty page and returns the ids.
func crPageDocs(t *testing.T, s store.DB, limit int) []domain.DocumentID {
	t.Helper()
	var out []domain.DocumentID
	q := store.DocumentQuery{Bank: bankA, Limit: limit}
	for range 100 {
		page := crListDocs(t, s, q)
		if len(page) > limit {
			t.Errorf("ListDocuments(Limit %d) returned %d documents", limit, len(page))
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, crDocIDs(page)...)
		last := page[len(page)-1]
		q.After = &store.DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	t.Fatalf("ListDocuments paging did not end after 100 pages")
	return out
}

// testCrListDocuments: ListDocuments lists newest first with every field as Get fills it plus live unit
// counts, applies Q, Tags and time bounds, pages with After exactly once and rejects a limit <= 0.
func testCrListDocuments(t *testing.T, s store.DB) {
	docs := crSeedDocuments(t, s)
	all := crListDocs(t, s, store.DocumentQuery{Bank: bankA, Limit: 50})
	wantSeq(t, "ListDocuments(all)", crDocIDs(all), crDocIDs(docs))
	counts := map[domain.DocumentID]map[domain.FactType]int64{
		"Doc-Alpha": {domain.FactTypeWorld: 1, domain.FactTypeExperience: 1},
		"doc-beta":  {domain.FactTypeWorld: 1},
	}
	if len(all) == len(docs) {
		for i := range all {
			crWantDocument(t, all[i], docs[i], counts[docs[i].ID])
		}
	}
	for _, d := range all {
		if (d.ObservationScopeModes != nil) != (d.ID == "doc-delta") {
			t.Errorf("document %s ObservationScopeModes = %+v, want set only on doc-delta", d.ID, d.ObservationScopeModes)
		}
	}
	for _, c := range crDocFilterCases(docs) {
		c.q.Bank, c.q.Limit = bankA, 50
		wantSeq(t, "ListDocuments("+c.name+")", crDocIDs(crListDocs(t, s, c.q)), c.want)
	}
	for _, limit := range []int{1, 2, 3} {
		wantSeq(t, fmt.Sprintf("ListDocuments paged by %d", limit), crPageDocs(t, s, limit), crDocIDs(docs))
	}
	for _, limit := range []int{0, -1} {
		_, err := s.Read().ListDocuments(t.Context(), store.DocumentQuery{Bank: bankA, Limit: limit})
		wantErrIs(t, fmt.Sprintf("ListDocuments(Limit %d)", limit), err, domain.ErrInvalidArgument)
	}
}

// ---------- ListMentalModels ----------

// crModelSeed is one mental model list_mental_models creates.
type crModelSeed struct {
	id     domain.MentalModelID
	folder string
	tags   []string
	rac    bool
}

// crModelSeeds are the models of list_mental_models; in byte order the ids are Z9 < m1 < ... < m5.
func crModelSeeds() []crModelSeed {
	return []crModelSeed{
		{"m1", "a", []string{"red"}, true},
		{"m2", "a/b", []string{"blue", "red"}, false},
		{"m3", "ab", []string{"blue"}, true},
		{"m4", "", []string{}, false},
		{"m5", "a/b/c", []string{"green"}, true},
		{"Z9", "zz", []string{}, false},
	}
}

// crCreateModels creates one model per seed in bank b, built from sampleModel with the seed's folder, tags,
// refresh_after_consolidation and a content naming the model.
func crCreateModels(t *testing.T, s store.DB, b domain.BankRef, seeds []crModelSeed) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		for _, sd := range seeds {
			m := sampleModel(b, sd.id)
			m.Spec.FolderPath, m.Spec.Tags = sd.folder, sd.tags
			m.Spec.Schedule.AfterConsolidation = sd.rac
			m.ContentMD = "# model " + string(sd.id)
			if err := tx.MentalModels().Create(t.Context(), m); err != nil {
				return err
			}
		}
		return nil
	})
}

// crModelIDs returns the ids of ms in order.
func crModelIDs(ms []domain.MentalModel) []domain.MentalModelID {
	out := make([]domain.MentalModelID, len(ms))
	for i, m := range ms {
		out[i] = m.Spec.ID
	}
	return out
}

// crModelCases are list_mental_models' filter cases (PageSize 0) and the ids they return, in order.
func crModelCases() []struct {
	name string
	q    store.ModelQuery
	want []domain.MentalModelID
} {
	tags := func(match domain.TagsMatch, ts ...string) store.ModelQuery {
		return store.ModelQuery{Tags: ts, TagsMatch: match}
	}
	return []struct {
		name string
		q    store.ModelQuery
		want []domain.MentalModelID
	}{
		{"all", store.ModelQuery{}, []domain.MentalModelID{"Z9", "m1", "m2", "m3", "m4", "m5"}},
		{"folder a", store.ModelQuery{FolderPrefix: "a"}, []domain.MentalModelID{"m1", "m2", "m5"}},
		{"folder a/b", store.ModelQuery{FolderPrefix: "a/b"}, []domain.MentalModelID{"m2", "m5"}},
		{"folder ab", store.ModelQuery{FolderPrefix: "ab"}, []domain.MentalModelID{"m3"}},
		{"folder z (not a path prefix of zz)", store.ModelQuery{FolderPrefix: "z"}, nil},
		{"tags red any_strict", tags(domain.TagsMatchAnyStrict, "red"), []domain.MentalModelID{"m1", "m2"}},
		{"tags red any (untagged pass)", tags(domain.TagsMatchAny, "red"), []domain.MentalModelID{"Z9", "m1", "m2", "m4"}},
		{"tags blue+red all_strict", tags(domain.TagsMatchAllStrict, "blue", "red"), []domain.MentalModelID{"m2"}},
		{"tags blue exact", tags(domain.TagsMatchExact, "blue"), []domain.MentalModelID{"m3"}},
		{
			"refresh_after_consolidation",
			store.ModelQuery{RefreshAfterConsolidation: true},
			[]domain.MentalModelID{"m1", "m3", "m5"},
		},
		{
			"folder a and refresh_after_consolidation",
			store.ModelQuery{FolderPrefix: "a", RefreshAfterConsolidation: true},
			[]domain.MentalModelID{"m1", "m5"},
		},
	}
}

// crPageModels lists q page by page with PageSize size until the token is "" and returns the ids and the
// number of pages.
func crPageModels(t *testing.T, s store.DB, q store.ModelQuery, size int) ([]domain.MentalModelID, int) {
	t.Helper()
	var out []domain.MentalModelID
	q.PageSize, q.PageToken = size, ""
	for pages := 1; pages <= 100; pages++ {
		ms, next := crListModels(t, s, q)
		if len(ms) > size {
			t.Errorf("ListMentalModels(PageSize %d) returned %d models", size, len(ms))
		}
		out = append(out, crModelIDs(ms)...)
		if next == "" {
			return out, pages
		}
		if len(ms) == 0 || next != string(ms[len(ms)-1].Spec.ID) {
			t.Fatalf("ListMentalModels page %d: token %q after %v, want the last listed id", pages, next, crModelIDs(ms))
		}
		q.PageToken = next
	}
	t.Fatalf("ListMentalModels paging did not end after 100 pages")
	return out, 0
}

// testCrListMentalModels: ListMentalModels applies the folder-prefix boundary, tags and
// refresh_after_consolidation filters, orders by id in byte order, returns models as Get does, pages with the
// returned token (each model exactly once, "" after the last), returns everything for PageSize 0 and rejects
// a negative PageSize.
func testCrListMentalModels(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	crCreateModels(t, s, bankA, crModelSeeds())
	for _, c := range crModelCases() {
		c.q.Bank = bankA
		ms, next := crListModels(t, s, c.q)
		wantSeq(t, "ListMentalModels("+c.name+")", crModelIDs(ms), c.want)
		if next != "" {
			t.Errorf("ListMentalModels(%s, PageSize 0) token = %q, want \"\"", c.name, next)
		}
	}
	all, _ := crListModels(t, s, store.ModelQuery{Bank: bankA})
	for _, m := range all {
		want, err := getModel(t, s, m.Spec.ID)
		if err != nil {
			t.Fatalf("MentalModels().Get(%s): %v", m.Spec.ID, err)
		}
		compareModel(t, "ListMentalModels "+string(m.Spec.ID), m, want)
		if m.ContentMD != "# model "+string(m.Spec.ID) {
			t.Errorf("model %s content = %q", m.Spec.ID, m.ContentMD)
		}
	}
	allIDs := []domain.MentalModelID{"Z9", "m1", "m2", "m3", "m4", "m5"}
	for size, wantPages := range map[int]int{1: 6, 2: 3, 3: 2, 4: 2, 6: 1, 7: 1} {
		ids, pages := crPageModels(t, s, store.ModelQuery{Bank: bankA}, size)
		wantSeq(t, fmt.Sprintf("ListMentalModels paged by %d", size), ids, allIDs)
		if pages != wantPages {
			t.Errorf("ListMentalModels paged by %d took %d pages, want %d", size, pages, wantPages)
		}
	}
	rac := store.ModelQuery{Bank: bankA, RefreshAfterConsolidation: true}
	ids, _ := crPageModels(t, s, rac, 2)
	wantSeq(t, "ListMentalModels(refresh_after_consolidation) paged by 2", ids, []domain.MentalModelID{"m1", "m3", "m5"})
	_, _, err := s.Read().ListMentalModels(t.Context(), store.ModelQuery{Bank: bankA, PageSize: -1})
	wantErrIs(t, "ListMentalModels(PageSize -1)", err, domain.ErrInvalidArgument)
}

// ---------- error regimes ----------

// crWantAllNotFound fails the test unless every curation read of bank b is ErrNotFound; unit and obs are
// ids that exist in b (or anywhere).
func crWantAllNotFound(t *testing.T, s store.DB, what string, b domain.BankRef, unit, obs domain.UnitID) {
	t.Helper()
	r, ctx := s.Read(), t.Context()
	_, err := r.GetUnit(ctx, b, unit)
	wantErrIs(t, "GetUnit("+what+")", err, domain.ErrNotFound)
	_, err = r.ListUnits(ctx, store.UnitQuery{Bank: b, Limit: 10})
	wantErrIs(t, "ListUnits("+what+")", err, domain.ErrNotFound)
	_, err = r.ObservationHistory(ctx, b, obs, 0, 100)
	wantErrIs(t, "ObservationHistory("+what+")", err, domain.ErrNotFound)
	_, err = r.BankStats(ctx, b)
	wantErrIs(t, "BankStats("+what+")", err, domain.ErrNotFound)
	_, err = r.ListDocuments(ctx, store.DocumentQuery{Bank: b, Limit: 10})
	wantErrIs(t, "ListDocuments("+what+")", err, domain.ErrNotFound)
	_, _, err = r.ListMentalModels(ctx, store.ModelQuery{Bank: b})
	wantErrIs(t, "ListMentalModels("+what+")", err, domain.ErrNotFound)
}

// testCrErrorRegimes: every curation read is ErrNotFound on an unknown bank and on a tombstoned bank that
// still holds units, versions, documents and models.
func testCrErrorRegimes(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	upsertDocs(t, s, bankA, "doc-1")
	f := fact(bankA, "f")
	f.DocumentID, f.CreatedAt = "doc-1", day(1)
	insertUnits(t, s, f)
	o := observation(bankA, "o")
	o.CreatedAt = day(2)
	upsertObservation(t, s, &o)
	crCreateModels(t, s, bankA, crModelSeeds()[:1])
	crGetUnit(t, s, bankA, f.ID)
	if _, err := s.Read().ObservationHistory(t.Context(), bankA, o.ID, 0, 100); err != nil {
		t.Fatalf("ObservationHistory(live bank): %v", err)
	}
	crWantAllNotFound(t, s, "unknown bank", crUnknownBank, f.ID, o.ID)
	crTombstone(t, s, bankA)
	crWantAllNotFound(t, s, "tombstoned bank", bankA, f.ID, o.ID)
}

// ---------- tenant isolation ----------

// crTenantSeed is what crSeedTenant wrote into one bank and the counts BankStats must report for it.
type crTenantSeed struct {
	units  []domain.UnitID
	live   []domain.UnitID
	fact   domain.UnitID
	obs    domain.UnitID
	ent    domain.EntityID
	docs   []domain.DocumentID
	models []domain.MentalModelID
	stats  domain.BankStats
}

// crSeedTenant writes documents, facts (one archived), an observation with a version, an entity, models and
// pending operations into bank b; extra adds one more document, fact, model and operation, so a leak
// between two seeded banks changes every count.
func crSeedTenant(t *testing.T, s store.DB, b domain.BankRef, extra bool) crTenantSeed {
	t.Helper()
	docs, models := []domain.DocumentID{"doc-1"}, []domain.MentalModelID{"m1"}
	facts := []domain.Unit{fact(b, "Ann Lee runs the lab"), fact(b, "lab notes")}
	jobs := []store.Job{withBank(doc("doc-1"), b)}
	if extra {
		docs, models = append(docs, "doc-x"), append(models, "m-x")
		facts = append(facts, fact(b, "extra fact"))
		jobs = append(jobs, job(b, domain.OperationKindConsolidate, "consolidate", `{}`))
	}
	upsertDocs(t, s, b, docs...)
	sd := crTenantSeed{docs: docs, models: models}
	for i := range facts {
		facts[i].DocumentID, facts[i].CreatedAt = "doc-1", day(i+1)
		sd.units = append(sd.units, facts[i].ID)
	}
	insertUnits(t, s, facts...)
	o := observation(b, "Ann runs the lab")
	o.CreatedAt = day(9)
	upsertObservation(t, s, &o)
	sd.fact, sd.obs, sd.units = facts[0].ID, o.ID, append(sd.units, o.ID)
	sd.ent = upsertEntity(t, s, domain.Entity{Bank: b, CanonicalName: "Ann Lee", Kind: "person"})
	crAttach(t, s, sd.fact, sd.ent)
	crArchive(t, s, b, facts[len(facts)-1].ID)
	sd.live = unitIDs(facts[:len(facts)-1])
	var seeds []crModelSeed
	for _, id := range models {
		seeds = append(seeds, crModelSeed{id: id, tags: []string{}})
	}
	crCreateModels(t, s, b, seeds)
	crEnqueue(t, s, jobs...)
	pending := map[domain.OperationKind]int64{}
	for _, j := range jobs {
		pending[j.Kind]++
	}
	live := int64(len(facts) - 1)
	sd.stats = domain.BankStats{
		MemoriesByFactType:  map[domain.FactType]int64{domain.FactTypeWorld: live, domain.FactTypeObservation: 1},
		InvalidatedMemories: 1, Documents: int64(len(docs)), Entities: 1, MentalModels: int64(len(models)),
		PendingOperationsByKind: pending, UnconsolidatedMemories: live,
	}
	return sd
}

// crCheckTenant fails the test if any curation read of bank b returns or counts a row of theirs.
func crCheckTenant(t *testing.T, s store.DB, b domain.BankRef, mine, theirs crTenantSeed) {
	t.Helper()
	what := b.Tenant + "/" + b.Bank
	wantSet(t, "ListUnits("+what+")", unitIDs(crListUnits(t, s, store.UnitQuery{Bank: b, Limit: 50})), mine.units)
	wantSet(t, "ListUnits("+what+", their entity)",
		unitIDs(crListUnits(t, s, store.UnitQuery{Bank: b, EntityID: theirs.ent, Limit: 50})), nil)
	wantSet(t, "ListUnits("+what+", my entity)",
		unitIDs(crListUnits(t, s, store.UnitQuery{Bank: b, EntityID: mine.ent, Limit: 50})), []domain.UnitID{mine.fact})
	crGetUnit(t, s, b, mine.fact)
	_, err := s.Read().GetUnit(t.Context(), b, theirs.fact)
	wantErrIs(t, "GetUnit("+what+", their unit)", err, domain.ErrNotFound)
	_, err = s.Read().ObservationHistory(t.Context(), b, theirs.obs, 0, 100)
	wantErrIs(t, "ObservationHistory("+what+", their observation)", err, domain.ErrNotFound)
	crWantStats(t, "BankStats("+what+")", crBankStats(t, s, b), crWithBankRow(t, s, b, mine.stats))
	wantSet(t, "ListDocuments("+what+")", crDocIDs(crListDocs(t, s, store.DocumentQuery{Bank: b, Limit: 50})),
		mine.docs)
	ms, _ := crListModels(t, s, store.ModelQuery{Bank: b})
	wantSet(t, "ListMentalModels("+what+")", crModelIDs(ms), mine.models)
	crWantDocCounts(t, s, b, "doc-1", mine.stats.MemoriesByFactType[domain.FactTypeWorld])
}

// crWantDocCounts fails the test unless ListDocuments reports exactly world live world facts for document id
// of bank b (the other tenant's document with the same id holds a different number).
func crWantDocCounts(t *testing.T, s store.DB, b domain.BankRef, id domain.DocumentID, world int64) {
	t.Helper()
	ds := crListDocs(t, s, store.DocumentQuery{Bank: b, DocumentIDContains: string(id), Limit: 50})
	want := map[domain.FactType]int64{domain.FactTypeWorld: world}
	for _, d := range ds {
		if d.ID == id && (d.MemoryCount != world || !maps.Equal(d.MemoriesByFactType, want)) {
			t.Errorf("ListDocuments(%v) %s counts = %d %v, want %d %v", b, id, d.MemoryCount, d.MemoriesByFactType, world,
				want)
		}
	}
}

// testCrTenantIsolation: the same bank id under two tenants, seeded with different amounts of everything,
// never leaks rows or counts through any curation read, in either direction.
func testCrTenantIsolation(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankOtherTenant)
	mine := crSeedTenant(t, s, bankA, false)
	theirs := crSeedTenant(t, s, bankOtherTenant, true)
	crCheckTenant(t, s, bankA, mine, theirs)
	crCheckTenant(t, s, bankOtherTenant, theirs, mine)
	inTx(t, s, func(tx store.Tx) error { return tx.Units().MarkConsolidated(t.Context(), mine.live, day(20)) })
	if crGetUnit(t, s, bankA, mine.obs).Stale {
		t.Errorf("GetUnit(my observation) Stale = true after my facts were consolidated, want false")
	}
	if !crGetUnit(t, s, bankOtherTenant, theirs.obs).Stale {
		t.Errorf("GetUnit(their observation) Stale = false while their facts are pending, want true")
	}
}
