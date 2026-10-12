package postgres_test

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"memd/internal/domain"
	"memd/internal/graph"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"slices"
	"testing"
	"time"
)

// TestStore covers Store, Tx, Reader and every repository a Tx exposes except the Queue (TestQueue): commit,
// rollback, read isolation, the recall arms, tenant isolation, the repositories and the curation writes. Each
// case runs in parallel on its own empty database.
func TestStore(t *testing.T) {
	t.Parallel()
	cases := []storeCase{
		{"TxCommitsEverything", testTxCommits},
		{"TxErrorRollsBackEverything", testTxRollback},
		{"TxPanicRollsBack", testTxPanicRollback},
		{"ReadSeesCommittedOnly", testReadCommittedOnly},
		{"BankSeqUnknownBank", testBankSeqUnknown},
		{"GetUnitsOrderAndSkips", testGetUnitsOrder},
		{"UnitFieldRoundTrip", testUnitRoundTrip},
		{"SearchVectorNearestFirst", testSearchVectorOrder},
		{"SearchVectorFilters", testSearchVectorFilters},
		{"SearchText", testSearchText},
		{"SearchTemporal", testSearchTemporal},
		{"ReaderReusedAcrossCommits", testReaderReuse},
	}
	cases = append(cases, armCases()...)
	cases = append(cases, repoCases()...)
	cases = append(cases, curationCases()...)
	cases = append(cases, isolationCases()...)
	runStoreCases(t, cases)
}

// txFixture is what writeEverything wrote in one transaction.
type txFixture struct {
	unit domain.UnitID
	doc  domain.DocumentID
	job  store.Job
}

// writeEverything writes a document, a unit, a queue row and an event in tx; it is the payload of the
// commit and rollback tests.
func writeEverything(t *testing.T, tx store.Tx) (txFixture, error) {
	f := txFixture{
		unit: newID[domain.UnitID](),
		doc:  "doc-tx",
		job: store.Job{
			Bank: bankA, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate",
			Payload: []byte(`{}`), Dedupe: store.DedupePending,
		},
	}
	ctx := t.Context()
	seq, err := tx.Seq(ctx, bankA)
	if err != nil {
		return f, err
	}
	if err := tx.Documents().Upsert(ctx, domain.Document{ID: f.doc, Bank: bankA, Tags: []string{}}); err != nil {
		return f, err
	}
	u := fact(bankA, "written in a transaction")
	u.ID, u.DocumentID, u.Seq = f.unit, f.doc, seq
	if err := tx.Units().Insert(ctx, []domain.Unit{u}); err != nil {
		return f, err
	}
	if f.job.ID, _, err = tx.Queue().Enqueue(ctx, f.job); err != nil {
		return f, err
	}
	ev := domain.LifecycleEvent{Seq: seq, Type: domain.EventTypeRetainCompleted, Bank: bankA, SubjectID: string(f.doc)}
	return f, tx.Events().Append(ctx, ev)
}

// listEvents lists bank b's events (every type) after afterSeq in a read transaction.
func listEvents(t *testing.T, s store.DB, b domain.BankRef, afterSeq int64, limit int) []domain.LifecycleEvent {
	t.Helper()
	evs, _ := listEventsOf(t, s, b, afterSeq, limit, nil)
	return evs
}

// listEventsOf lists bank b's events of types (empty = all) after afterSeq and returns them with List's
// lastSeq.
func listEventsOf(
	t *testing.T, s store.DB, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64) {
	t.Helper()
	var (
		evs     []domain.LifecycleEvent
		lastSeq int64
	)
	inTx(t, s, func(tx store.Tx) error {
		var err error
		evs, lastSeq, err = tx.Events().List(t.Context(), b, afterSeq, limit, types)
		return err
	})
	return evs, lastSeq
}

// getDocument reads a document in its own transaction and returns Get's error unchanged.
func getDocument(t *testing.T, s store.DB, b domain.BankRef, id domain.DocumentID) (domain.Document, error) {
	t.Helper()
	var d domain.Document
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		d, err = tx.Documents().Get(t.Context(), b, id)
		return err
	})
	return d, err
}

// testTxCommits: everything written in a committed transaction is visible afterwards.
func testTxCommits(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	var f txFixture
	inTx(t, s, func(tx store.Tx) error {
		var err error
		f, err = writeEverything(t, tx)
		return err
	})
	if f.job.ID == "" {
		t.Errorf("Enqueue returned an empty id")
	}
	if got := unitIDs(getUnits(t, s, bankA, f.unit)); !slices.Equal(got, []domain.UnitID{f.unit}) {
		t.Errorf("GetUnits after commit = %v, want [%s]", got, f.unit)
	}
	if _, err := getDocument(t, s, bankA, f.doc); err != nil {
		t.Errorf("Documents().Get after commit: %v", err)
	}
	if evs := listEvents(t, s, bankA, 0, 10); len(evs) != 1 || evs[0].SubjectID != string(f.doc) {
		t.Errorf("Events().List after commit = %+v, want one event for %s", evs, f.doc)
	}
	if op, err := getOp(t, s, bankA, f.job.ID); err != nil || op.Kind != f.job.Kind {
		t.Errorf("Queue().Get after commit = %+v, %v; want the %s row", op, err, f.job.Kind)
	}
	if got := bankSeq(t, s, bankA); got != 1 {
		t.Errorf("BankSeq after one committed Seq = %d, want 1", got)
	}
}

// assertNothingWritten checks that none of writeEverything's rows is visible and BankSeq is unchanged.
func assertNothingWritten(t *testing.T, s store.DB, f txFixture, seqBefore int64) {
	t.Helper()
	if got := getUnits(t, s, bankA, f.unit); len(got) != 0 {
		t.Errorf("GetUnits after rollback = %v, want none", unitIDs(got))
	}
	if _, err := getDocument(t, s, bankA, f.doc); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Documents().Get after rollback: got %v, want ErrNotFound", err)
	}
	if evs := listEvents(t, s, bankA, 0, 10); len(evs) != 0 {
		t.Errorf("Events().List after rollback = %+v, want none", evs)
	}
	if f.job.ID == "" {
		t.Fatalf("the rolled-back transaction never enqueued its job")
	}
	_, err := getOp(t, s, bankA, f.job.ID)
	wantErrIs(t, "Queue().Get of a rolled-back row", err, domain.ErrNotFound)
	if got := bankSeq(t, s, bankA); got != seqBefore {
		t.Errorf("BankSeq after rollback = %d, want %d", got, seqBefore)
	}
}

// testTxRollback: an error from fn rolls back units, documents, queue rows, events and the seq bump.
func testTxRollback(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	before := bankSeq(t, s, bankA)
	var f txFixture
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		if f, err = writeEverything(t, tx); err != nil {
			return fmt.Errorf("writeEverything: %w", err)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("InTx returned %v, want the callback's error", err)
	}
	assertNothingWritten(t, s, f, before)
}

// testTxPanicRollback: a panic in fn rolls back. InTx may re-panic or return an error; both are accepted.
func testTxPanicRollback(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	before := bankSeq(t, s, bankA)
	var f txFixture
	func() {
		defer func() { _ = recover() }()
		_ = s.InTx(t.Context(), func(tx store.Tx) error {
			var err error
			if f, err = writeEverything(t, tx); err != nil {
				return fmt.Errorf("writeEverything: %w", err)
			}
			panic("forced panic")
		})
	}()
	if f.unit == "" {
		t.Fatalf("callback did not run")
	}
	assertNothingWritten(t, s, f, before)
}

// openTxRead is what a Read() view returned while another transaction held the bank's Seq.
type openTxRead struct {
	units   []domain.UnitID
	bankSeq int64
	err     error
}

// readDuringTx reads ids and bankA's BankSeq through Read() on another goroutine and waits at most
// waitTimeout, so a Read() that blocks on the caller's open transaction fails instead of hanging.
func readDuringTx(t *testing.T, s store.DB, ids []domain.UnitID) openTxRead {
	ch := make(chan openTxRead, 1)
	go func() {
		var r openTxRead
		us, err := s.Read().GetUnits(t.Context(), bankA, ids)
		r.units, r.err = unitIDs(us), err
		if err == nil {
			r.bankSeq, r.err = s.Read().BankSeq(t.Context(), bankA)
		}
		ch <- r
	}()
	select {
	case r := <-ch:
		return r
	case <-time.After(waitTimeout):
		return openTxRead{err: errors.New("Read() blocked on an open transaction")}
	}
}

// testReadCommittedOnly: while a transaction holds bank A's Seq S, Read() does not block, BankSeq returns
// the committed S-1 and GetUnits does not see the transaction's units; after commit both are visible.
func testReadCommittedOnly(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	committed := fact(bankA, "committed before")
	insertUnits(t, s, committed)
	pending := fact(bankA, "written in the open transaction")
	var (
		held   int64
		inside openTxRead
	)
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if held, err = stampAndInsert(t, tx, []domain.Unit{pending}); err != nil {
			return err
		}
		inside = readDuringTx(t, s, []domain.UnitID{committed.ID, pending.ID})
		return inside.err
	})
	wantSeq(t, "Read().GetUnits during open tx", inside.units, []domain.UnitID{committed.ID})
	if inside.bankSeq != held-1 {
		t.Errorf("Read().BankSeq while a tx holds Seq %d = %d, want the committed %d", held, inside.bankSeq, held-1)
	}
	wantSeq(t, "Read() after commit", unitIDs(getUnits(t, s, bankA, committed.ID, pending.ID)),
		[]domain.UnitID{committed.ID, pending.ID})
	if got := bankSeq(t, s, bankA); got != held {
		t.Errorf("BankSeq after commit = %d, want %d", got, held)
	}
}

// testBankSeqUnknown: BankSeq of an unknown bank is ErrNotFound; a new bank starts at 0.
func testBankSeqUnknown(t *testing.T, s store.DB) {
	_, err := s.Read().BankSeq(t.Context(), bankA)
	wantErrIs(t, "BankSeq(unknown bank)", err, domain.ErrNotFound)
	ensureBanks(t, s, bankA)
	if got := bankSeq(t, s, bankA); got != 0 {
		t.Errorf("BankSeq(new bank) = %d, want 0", got)
	}
}

// testGetUnitsOrder: GetUnits follows the order of ids and skips unknown, archived and other-bank ids.
func testGetUnitsOrder(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	u1, u2, u3 := fact(bankA, "one"), fact(bankA, "two"), fact(bankA, "three")
	insertUnits(t, s, u1, u2, u3)
	other := fact(bankB, "other bank")
	insertUnits(t, s, other)
	inTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		return tx.Units().Archive(t.Context(), bankA, u2.ID, "test")
	})
	got := getUnits(t, s, bankA, u3.ID, newID[domain.UnitID](), u1.ID, u2.ID, other.ID)
	wantSeq(t, "GetUnits ids", unitIDs(got), []domain.UnitID{u3.ID, u1.ID})
	if len(got) == 2 && (got[0].Text != "three" || got[1].Text != "one") {
		t.Errorf("GetUnits texts = %q, %q; want three, one", got[0].Text, got[1].Text)
	}
	if got := getUnits(t, s, bankA); len(got) != 0 {
		t.Errorf("GetUnits(no ids) = %v, want none", unitIDs(got))
	}
}

// richUnits returns two units exercising every stored field: a dated experience with a chunk, and a
// timeless world fact with no times, context or metadata.
func richUnits(b domain.BankRef, doc domain.DocumentID, chunk domain.ChunkID) (dated, timeless domain.Unit) {
	dated = fact(b, "Alice moved to Lisbon")
	dated.Type = domain.FactTypeExperience
	dated.DocumentID, dated.ChunkID = doc, chunk
	dated.Context = "chat with Alice"
	dated.Metadata = map[string]string{"source": "chat", "lang": "en"}
	dated.Tags = domain.NormalizeTags([]string{"Travel", "people "})
	dated.Embedding = angleVec(10)
	dated.OccurredStart, dated.OccurredEnd = ptr(day(10)), ptr(day(12).Add(90*time.Minute))
	dated.MentionedAt = ptr(day(14).Add(1500 * time.Millisecond))
	dated.CreatedAt = day(20)
	timeless = fact(b, "Water boils at 100 C at sea level")
	timeless.DocumentID = doc
	timeless.Timeless = true
	timeless.Tags = []string{}
	timeless.Embedding = angleVec(80)
	timeless.CreatedAt = day(21)
	return dated, timeless
}

// compareUnit reports every stored field of got that differs from want (reader-filled fields excluded).
func compareUnit(t *testing.T, got, want domain.Unit) {
	t.Helper()
	if got.ID != want.ID || got.Bank != want.Bank || got.DocumentID != want.DocumentID || got.ChunkID != want.ChunkID {
		t.Errorf("unit identity = (%s %v %q %q), want (%s %v %q %q)", got.ID, got.Bank, got.DocumentID,
			got.ChunkID, want.ID, want.Bank, want.DocumentID, want.ChunkID)
	}
	if got.Type != want.Type || got.Text != want.Text || got.Context != want.Context {
		t.Errorf("unit %s content = (%q %q %q), want (%q %q %q)", want.ID, got.Type, got.Text, got.Context,
			want.Type, want.Text, want.Context)
	}
	if !maps.Equal(got.Metadata, want.Metadata) || !slices.Equal(got.Tags, want.Tags) {
		t.Errorf("unit %s metadata/tags = %v %q, want %v %q", want.ID, got.Metadata, got.Tags, want.Metadata, want.Tags)
	}
	compareUnitState(t, got, want)
	compareUnitTimes(t, got, want)
	compareEmbedding(t, want.ID, got.Embedding, want.Embedding)
}

// compareUnitState reports differing timeless flag, seq, and an observation's version and proof count.
func compareUnitState(t *testing.T, got, want domain.Unit) {
	t.Helper()
	if got.Timeless != want.Timeless || got.Seq != want.Seq {
		t.Errorf("unit %s timeless/seq = %v %d, want %v %d", want.ID, got.Timeless, got.Seq, want.Timeless, want.Seq)
	}
	if want.Type == domain.FactTypeObservation && (got.Version != want.Version || got.ProofCount != want.ProofCount) {
		t.Errorf("observation %s version/proof_count = %d %d, want %d %d", want.ID, got.Version, got.ProofCount,
			want.Version, want.ProofCount)
	}
}

// compareUnitTimes reports differing timestamps of a unit.
func compareUnitTimes(t *testing.T, got, want domain.Unit) {
	t.Helper()
	if !sameTimePtr(got.OccurredStart, want.OccurredStart) || !sameTimePtr(got.OccurredEnd, want.OccurredEnd) {
		t.Errorf("unit %s occurred = %v..%v, want %v..%v", want.ID, got.OccurredStart, got.OccurredEnd,
			want.OccurredStart, want.OccurredEnd)
	}
	if !sameTimePtr(got.MentionedAt, want.MentionedAt) {
		t.Errorf("unit %s mentioned_at = %v, want %v", want.ID, got.MentionedAt, want.MentionedAt)
	}
	if !want.CreatedAt.IsZero() && !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("unit %s created_at = %v, want %v", want.ID, got.CreatedAt, want.CreatedAt)
	}
	if !sameTimePtr(got.ConsolidatedAt, want.ConsolidatedAt) ||
		!sameTimePtr(got.ConsolidationFailedAt, want.ConsolidationFailedAt) {
		t.Errorf("unit %s consolidated/failed = %v/%v, want %v/%v", want.ID, got.ConsolidatedAt,
			got.ConsolidationFailedAt, want.ConsolidatedAt, want.ConsolidationFailedAt)
	}
}

// compareEmbedding reports an embedding that lost its length or values.
func compareEmbedding(t *testing.T, id domain.UnitID, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("unit %s embedding has %d dims, want %d", id, len(got), len(want))
		return
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("unit %s embedding[%d] = %v, want %v", id, i, got[i], want[i])
			return
		}
	}
}

// testUnitRoundTrip: Insert then GetUnits returns every stored field as given.
func testUnitRoundTrip(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	doc, chunk := domain.DocumentID("doc-rt"), newID[domain.ChunkID]()
	upsertDocs(t, s, bankA, doc)
	inTx(t, s, func(tx store.Tx) error {
		c := domain.Chunk{ID: chunk, Bank: bankA, DocumentID: doc, Index: 0, Text: "chunk", ContentHash: "c0"}
		return tx.Chunks().Insert(t.Context(), []domain.Chunk{c})
	})
	dated, timeless := richUnits(bankA, doc, chunk)
	seq := insertUnits(t, s, dated, timeless)
	dated.Seq, timeless.Seq = seq, seq
	got := getUnits(t, s, bankA, dated.ID, timeless.ID)
	if len(got) != 2 {
		t.Fatalf("GetUnits returned %d units, want 2", len(got))
	}
	compareUnit(t, got[0], dated)
	compareUnit(t, got[1], timeless)
}

// ---------- SearchVector ----------

// vectorFixture holds units at known angles from the query vector angleVec(0), each in its own tx.
type vectorFixture struct {
	a0, a30, a60, a120, noEmb, otherBank domain.Unit
}

// newVectorFixture inserts a0 (world), a30 (experience), a60 and a120 (world), a unit without embedding,
// and a bank-B unit identical to the query. Seq of a0..a120 is 1..4 in bank A.
func newVectorFixture(t *testing.T, s store.DB) vectorFixture {
	t.Helper()
	ensureBanks(t, s, bankA, bankB)
	f := vectorFixture{
		a0: fact(bankA, "angle 0"), a30: fact(bankA, "angle 30"), a60: fact(bankA, "angle 60"),
		a120: fact(bankA, "angle 120"), noEmb: fact(bankA, "no embedding"), otherBank: fact(bankB, "other bank"),
	}
	f.a0.Embedding, f.a30.Embedding, f.a60.Embedding = angleVec(0), angleVec(30), angleVec(60)
	f.a120.Embedding, f.otherBank.Embedding = angleVec(120), angleVec(0)
	f.a30.Type = domain.FactTypeExperience
	for _, u := range []*domain.Unit{&f.a0, &f.a30, &f.a60, &f.a120, &f.noEmb, &f.otherBank} {
		u.Seq = insertUnits(t, s, *u)
	}
	return f
}

// searchVector runs SearchVector through Read() and fails the test on error.
func searchVector(t *testing.T, s store.DB, q store.VectorQuery) []store.Hit {
	t.Helper()
	hs, err := s.Read().SearchVector(t.Context(), q)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	return hs
}

// testSearchVectorOrder: nearest first, scores are cosine similarity, other banks and nil embeddings excluded.
func testSearchVectorOrder(t *testing.T, s store.DB) {
	f := newVectorFixture(t, s)
	hs := searchVector(t, s, store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10})
	wantSeq(t, "SearchVector order", hitIDs(hs), []domain.UnitID{f.a0.ID, f.a30.ID, f.a60.ID, f.a120.ID})
	wantScoresDescending(t, "SearchVector", hs)
	want := map[domain.UnitID]float64{f.a0.ID: 1, f.a30.ID: cosDeg(30), f.a60.ID: cosDeg(60), f.a120.ID: cosDeg(120)}
	for _, h := range hs {
		if w, ok := want[h.ID]; ok && math.Abs(h.Score-w) > scoreTolerance {
			t.Errorf("SearchVector score of %s = %v, want cosine similarity %v", h.ID, h.Score, w)
		}
	}
	wantNoHit(t, "SearchVector bank isolation", hs, f.otherBank.ID)
	wantNoHit(t, "SearchVector nil embedding", hs, f.noEmb.ID)
}

// testSearchVectorFilters: Limit, MinSimilarity and Types.
func testSearchVectorFilters(t *testing.T, s store.DB) {
	f := newVectorFixture(t, s)
	q := store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 2}
	wantSeq(t, "SearchVector Limit 2", hitIDs(searchVector(t, s, q)), []domain.UnitID{f.a0.ID, f.a30.ID})
	q = store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10, MinSimilarity: 0.6}
	wantSeq(t, "SearchVector MinSimilarity 0.6", hitIDs(searchVector(t, s, q)), []domain.UnitID{f.a0.ID, f.a30.ID})
	exp := []domain.FactType{domain.FactTypeExperience}
	q = store.VectorQuery{Bank: bankA, Embedding: angleVec(0), Limit: 10, Types: exp}
	wantSeq(t, "SearchVector Types=[experience]", hitIDs(searchVector(t, s, q)), []domain.UnitID{f.a30.ID})
	q.Types = []domain.FactType{domain.FactTypeWorld}
	wantSeq(t, "SearchVector Types=[world]", hitIDs(searchVector(t, s, q)),
		[]domain.UnitID{f.a0.ID, f.a60.ID, f.a120.ID})
	q = store.VectorQuery{Bank: bankB, Embedding: angleVec(0), Limit: 10}
	wantSeq(t, "SearchVector bank B", hitIDs(searchVector(t, s, q)), []domain.UnitID{f.otherBank.ID})
}

// namedPredicate is one tag filter of the arm tag tests.
type namedPredicate struct {
	name string
	p    *domain.TagPredicate
}

// tagPredicates are filters whose expected results come from (*domain.TagPredicate).Eval.
func tagPredicates() []namedPredicate {
	leaf := func(m domain.TagsMatch, tags ...string) domain.TagPredicate {
		return domain.TagPredicate{Op: domain.PredicateTags, Tags: tags, Match: m}
	}
	and := func(args ...domain.TagPredicate) *domain.TagPredicate {
		return &domain.TagPredicate{Op: domain.PredicateAnd, Args: args}
	}
	or := func(args ...domain.TagPredicate) *domain.TagPredicate {
		return &domain.TagPredicate{Op: domain.PredicateOr, Args: args}
	}
	alpha, beta := leaf(domain.TagsMatchAnyStrict, "alpha"), leaf(domain.TagsMatchAnyStrict, "beta")
	return []namedPredicate{
		{"nil", nil},
		{"any alpha", ptr(leaf(domain.TagsMatchAny, "alpha"))},
		{"any_strict alpha", &alpha},
		{"all alpha beta", ptr(leaf(domain.TagsMatchAll, "alpha", "beta"))},
		{"all_strict alpha beta", ptr(leaf(domain.TagsMatchAllStrict, "alpha", "beta"))},
		{"exact empty", ptr(leaf(domain.TagsMatchExact))},
		{"exact beta", ptr(leaf(domain.TagsMatchExact, "beta"))},
		{"alpha and not beta", and(alpha, notOf(beta))},
		{"exact gamma or all_strict alpha beta", or(
			leaf(domain.TagsMatchExact, "gamma"), leaf(domain.TagsMatchAllStrict, "alpha", "beta"))},
		{"not any_strict alpha", ptr(notOf(alpha))},
		{"empty and", and()},
		{"empty or", or()},
		{"or(alpha, beta) and not exact alpha beta", and(
			*or(alpha, beta), notOf(leaf(domain.TagsMatchExact, "alpha", "beta")))},
	}
}

// notOf wraps p in a not node.
func notOf(p domain.TagPredicate) domain.TagPredicate {
	return domain.TagPredicate{Op: domain.PredicateNot, Args: []domain.TagPredicate{p}}
}

// ---------- SearchText, SearchTemporal, ExpandGraph ----------

// searchText runs SearchText through Read() and fails the test on error.
func searchText(t *testing.T, s store.DB, q store.TextQuery) []store.Hit {
	t.Helper()
	hs, err := s.Read().SearchText(t.Context(), q)
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	return hs
}

// testSearchText: a keyword match ranks above a non-match; bank isolation, Types and Limit hold.
func testSearchText(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	match := fact(bankA, "The zebra crossed the river at dawn")
	noMatch := fact(bankA, "Quarterly revenue grew in March")
	exp := fact(bankA, "I photographed a zebra herd on safari")
	exp.Type = domain.FactTypeExperience
	insertUnits(t, s, match, noMatch, exp)
	other := fact(bankB, "A zebra in another bank")
	insertUnits(t, s, other)

	hs := searchText(t, s, store.TextQuery{Bank: bankA, Text: "zebra", Limit: 10})
	wantSet(t, "SearchText zebra (matching units)", matchOnly(hs, noMatch.ID), []domain.UnitID{match.ID, exp.ID})
	if i := slices.Index(hitIDs(hs), noMatch.ID); i >= 0 && i < 2 {
		t.Errorf("SearchText: non-matching unit ranked %d, above a matching one: %v", i, hitIDs(hs))
	}
	wantScoresDescending(t, "SearchText", hs)
	wantNoHit(t, "SearchText bank isolation", hs, other.ID)
	q := store.TextQuery{Bank: bankA, Text: "zebra", Types: []domain.FactType{domain.FactTypeExperience}, Limit: 10}
	wantSeq(t, "SearchText Types=[experience]", hitIDs(searchText(t, s, q)), []domain.UnitID{exp.ID})
	if hs := searchText(t, s, store.TextQuery{Bank: bankA, Text: "zebra", Limit: 1}); len(hs) != 1 {
		t.Errorf("SearchText Limit 1 returned %d hits", len(hs))
	}
	q = store.TextQuery{Bank: bankB, Text: "zebra", Limit: 10}
	wantSeq(t, "SearchText bank B", hitIDs(searchText(t, s, q)), []domain.UnitID{other.ID})
}

// matchOnly returns the hit ids except skip.
func matchOnly(hs []store.Hit, skip domain.UnitID) []domain.UnitID {
	return slices.DeleteFunc(hitIDs(hs), func(id domain.UnitID) bool { return id == skip })
}

// dated returns a fact whose occurrence spans [start, end] and that was mentioned at start.
func dated(b domain.BankRef, text string, start, end time.Time) domain.Unit {
	u := fact(b, text)
	u.OccurredStart, u.OccurredEnd, u.MentionedAt = ptr(start), ptr(end), ptr(start)
	return u
}

// testSearchTemporal: units overlapping the window (inside it, starting before it, or spanning all of it)
// are hits; units outside it, timeless units and other banks' units are not.
func testSearchTemporal(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB)
	winStart, winEnd := day(60), day(90)
	inside := dated(bankA, "inside", day(70), day(71))
	spanning := dated(bankA, "starts before, ends inside", day(50), day(65))
	covering := dated(bankA, "starts before, ends after", day(50), day(100))
	before := dated(bankA, "before", day(10), day(11))
	after := dated(bankA, "after", day(200), day(201))
	timeless := fact(bankA, "timeless")
	timeless.Timeless = true
	insertUnits(t, s, inside, spanning, covering, before, after, timeless)
	other := dated(bankB, "other bank inside", day(70), day(71))
	insertUnits(t, s, other)

	q := store.TemporalQuery{Bank: bankA, Window: domain.TimeWindow{Start: winStart, End: winEnd}, Limit: 10}
	wantSet(t, "SearchTemporal window hits", hitIDs(searchTemporal(t, s, q)),
		[]domain.UnitID{inside.ID, spanning.ID, covering.ID})
	q.Limit = 1
	if hs := searchTemporal(t, s, q); len(hs) != 1 {
		t.Errorf("SearchTemporal Limit 1 = %v, want one hit", hitIDs(hs))
	}
	q = store.TemporalQuery{Bank: bankB, Window: domain.TimeWindow{Start: winStart, End: winEnd}, Limit: 10}
	wantSeq(t, "SearchTemporal bank B", hitIDs(searchTemporal(t, s, q)), []domain.UnitID{other.ID})
}

// searchTemporal runs SearchTemporal through Read() and fails the test on error.
func searchTemporal(t *testing.T, s store.DB, q store.TemporalQuery) []store.Hit {
	t.Helper()
	hs, err := s.Read().SearchTemporal(t.Context(), q)
	if err != nil {
		t.Fatalf("SearchTemporal: %v", err)
	}
	return hs
}

// expandGraph runs Expand through the pool-backed graph (DB.Graph) and returns the hits as store hits; it fails
// the test on error.
func expandGraph(t *testing.T, s store.DB, q graph.Query) []store.Hit {
	t.Helper()
	hs, err := s.(*postgres.DB).Graph().Expand(t.Context(), q)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	out := make([]store.Hit, len(hs))
	for i, h := range hs {
		out[i] = store.Hit{ID: h.ID, Score: h.Score}
	}
	return out
}

// link builds a link of bank b.
func link(b domain.BankRef, from, to domain.UnitID, kind graph.Kind, w float64) graph.Link {
	return graph.Link{Bank: b, From: from, To: to, Kind: kind, Weight: w}
}

// insertLinks stores links in one transaction.
func insertLinks(t *testing.T, s store.DB, ls ...graph.Link) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error { return tx.Graph().AddLinks(t.Context(), ls) })
}
