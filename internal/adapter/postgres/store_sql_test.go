package postgres_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Store behaviour that needs direct SQL to set up or observe.

var (
	fuBank  = port.BankRef{Tenant: "tenant-fu", Bank: "bank-fu"}
	fuOther = port.BankRef{Tenant: "tenant-fu-2", Bank: "bank-fu"}
)

// fuUnit returns a world fact of b with a fresh id.
func fuUnit(b port.BankRef, text string) domain.Unit {
	return domain.Unit{ID: domain.UnitID(uuid.NewString()), Bank: b, Type: domain.FactTypeWorld, Text: text}
}

// fuInsert inserts us (all of one bank) in one transaction stamped with its Seq.
func fuInsert(t *testing.T, s *postgres.Store, us ...domain.Unit) {
	t.Helper()
	if err := s.InTx(t.Context(), func(tx port.Tx) error {
		seq, err := tx.Seq(t.Context(), us[0].Bank)
		if err != nil {
			return err
		}
		for i := range us {
			us[i].Seq = seq
		}
		return tx.Units().Insert(t.Context(), us)
	}); err != nil {
		t.Fatalf("insert units: %v", err)
	}
}

// fuTx runs fn in its own transaction and returns its error.
func fuTx(t *testing.T, s *postgres.Store, fn func(tx port.Tx) error) error {
	t.Helper()
	if err := s.InTx(t.Context(), fn); err != nil {
		return fmt.Errorf("tx: %w", err)
	}
	return nil
}

// fuMarkDeleting tombstones b.
func fuMarkDeleting(t *testing.T, s *postgres.Store, b port.BankRef) {
	t.Helper()
	mark := func(tx port.Tx) error { return tx.Banks().MarkDeleting(t.Context(), b, time.Now()) }
	if err := fuTx(t, s, mark); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
}

// TestDataExceptionIsInvalidArgument: a NUL byte in unit text is a data exception (SQLSTATE class
// 22), mapped to domain.ErrInvalidArgument so a job fails at once instead of retrying. The in-memory fake
// accepts NUL bytes, so this lives here rather than in TestStore.
func TestDataExceptionIsInvalidArgument(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ensureQueueBanks(t, s, fuBank)
	err := fuTx(t, s, func(tx port.Tx) error {
		return tx.Units().Insert(t.Context(), []domain.Unit{fuUnit(fuBank, "nul \x00 byte")})
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Errorf("Insert(text with NUL) = %v, want ErrInvalidArgument", err)
	}
}

// TestDeleteBatchLocksTombstone: DeleteBatch holds FOR KEY SHARE on the tombstoned bank row for its
// transaction, so a concurrent Delete of the bank waits until the batch commits.
func TestDeleteBatchLocksTombstone(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ensureQueueBanks(t, s, fuBank)
	fuInsert(t, s, fuUnit(fuBank, "a"), fuUnit(fuBank, "b"))
	fuMarkDeleting(t, s, fuBank)
	batched, release, batchErr := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		batchErr <- s.InTx(t.Context(), func(tx port.Tx) error {
			if _, err := tx.Banks().DeleteBatch(t.Context(), fuBank, 1); err != nil {
				return err
			}
			close(batched)
			<-release
			return nil
		})
	}()
	select {
	case <-batched:
	case err := <-batchErr:
		t.Fatalf("DeleteBatch: %v", err)
	}
	var deleted atomic.Bool
	deleteErr := make(chan error, 1)
	go func() {
		err := s.InTx(t.Context(), func(tx port.Tx) error { return tx.Banks().Delete(t.Context(), fuBank) })
		deleted.Store(true)
		deleteErr <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if deleted.Load() {
		t.Errorf("Delete finished while a DeleteBatch transaction held the bank")
	}
	close(release)
	if err := <-batchErr; err != nil {
		t.Errorf("DeleteBatch tx: %v", err)
	}
	if err := <-deleteErr; err != nil {
		t.Errorf("Delete after the batch committed: %v", err)
	}
}

// unkeyedTables are the bank-keyed tables without a foreign key to banks, with an insert of one row.
var unkeyedTables = map[string]string{
	"graph_dirty": `INSERT INTO graph_dirty (tenant_id, bank_id, unit_id)
		VALUES ($1, $2, gen_random_uuid())`,
	"entity_maintenance": `INSERT INTO entity_maintenance (tenant_id, bank_id, entity_id)
		VALUES ($1, $2, gen_random_uuid())`,
	"observation_versions": `INSERT INTO observation_versions (tenant_id, bank_id, observation_id, version, text)
		VALUES ($1, $2, gen_random_uuid(), 1, 'v1')`,
	"memory_audit": `INSERT INTO memory_audit (tenant_id, bank_id, unit_id, action)
		VALUES ($1, $2, gen_random_uuid(), 'edit')`,
	"memory_units_archive": `INSERT INTO memory_units_archive (id, tenant_id, bank_id, fact_type, text, seq)
		VALUES (gen_random_uuid(), $1, $2, 'world', 'gone', 1)`,
}

// TestDeleteRemovesUnkeyedTables: Delete without a prior DeleteBatch also removes the bank's rows
// in the tables the bank row's cascade misses, and leaves another tenant's same-named bank alone.
func TestDeleteRemovesUnkeyedTables(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ensureQueueBanks(t, s, fuBank, fuOther)
	for table, ins := range unkeyedTables {
		for _, b := range []port.BankRef{fuBank, fuOther} {
			if _, err := pool.Exec(t.Context(), ins, b.Tenant, b.Bank); err != nil {
				t.Fatalf("fill %s: %v", table, err)
			}
		}
	}
	if err := fuTx(t, s, func(tx port.Tx) error {
		if err := tx.Banks().MarkDeleting(t.Context(), fuBank, time.Now()); err != nil {
			return err
		}
		return tx.Banks().Delete(t.Context(), fuBank)
	}); err != nil {
		t.Fatalf("MarkDeleting + Delete: %v", err)
	}
	for table := range unkeyedTables {
		for b, want := range map[port.BankRef]int{fuBank: 0, fuOther: 1} {
			var n int
			q := fmt.Sprintf(`SELECT count(*) FROM %s WHERE tenant_id = $1 AND bank_id = $2`, table)
			if err := pool.QueryRow(t.Context(), q, b.Tenant, b.Bank).Scan(&n); err != nil || n != want {
				t.Errorf("%s rows of %v after Delete = %d (%v), want %d", table, b, n, err, want)
			}
		}
	}
}

// TestRejectUnnormalizedTags: unit, document and directive writes reject tags that are not in
// domain.NormalizeTags form, matching the in-memory fake; SetDocumentTags also retags archived units.
func TestRejectUnnormalizedTags(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ensureQueueBanks(t, s, fuBank)
	bad := []string{"Beta", "alpha"}
	if err := fuTx(t, s, func(tx port.Tx) error {
		return tx.Documents().Upsert(t.Context(), domain.Document{ID: "doc-1", Bank: fuBank, Tags: []string{}})
	}); err != nil {
		t.Fatalf("Upsert doc: %v", err)
	}
	writes := map[string]func(tx port.Tx) error{
		"Units().Insert": func(tx port.Tx) error {
			u := fuUnit(fuBank, "x")
			u.Tags, u.Seq = bad, 1
			return tx.Units().Insert(t.Context(), []domain.Unit{u})
		},
		"Units().SetDocumentTags": func(tx port.Tx) error {
			_, err := tx.Units().SetDocumentTags(t.Context(), fuBank, "doc-1", nil, bad, 1)
			return err
		},
		"Documents().Upsert": func(tx port.Tx) error {
			return tx.Documents().Upsert(t.Context(), domain.Document{ID: "doc-2", Bank: fuBank, Tags: bad})
		},
		"Documents().SetTags": func(tx port.Tx) error { return tx.Documents().SetTags(t.Context(), fuBank, "doc-1", bad) },
		"Directives().Upsert": func(tx port.Tx) error {
			_, err := tx.Directives().Upsert(t.Context(), domain.Directive{Bank: fuBank, Name: "d", Tags: bad})
			return err
		},
	}
	for name, w := range writes {
		if err := fuTx(t, s, w); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s(unnormalized tags) = %v, want ErrInvalidArgument", name, err)
		}
	}

	u := fuUnit(fuBank, "archived")
	u.DocumentID = "doc-1"
	fuInsert(t, s, u)
	if err := fuTx(t, s, func(tx port.Tx) error {
		if err := tx.Units().Archive(t.Context(), fuBank, u.ID, "test"); err != nil {
			return err
		}
		_, err := tx.Units().SetDocumentTags(t.Context(), fuBank, "doc-1", nil, []string{"new"}, 1)
		return err
	}); err != nil {
		t.Fatalf("Archive + SetDocumentTags: %v", err)
	}
	var tags []string
	if err := pool.QueryRow(t.Context(), `SELECT tags FROM memory_units_archive WHERE id = $1`, string(u.ID)).
		Scan(&tags); err != nil || !slices.Equal(tags, []string{"new"}) {
		t.Errorf("archived unit tags after SetDocumentTags = %v (%v), want [new]", tags, err)
	}
}

// fuDay returns a fixed day n of a test month.
func fuDay(n float64) time.Time {
	return time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n * float64(24*time.Hour)))
}

// TestSearchTemporalEdges: an empty or inverted window has no hits; End is exclusive; hits come
// closest to the window's center first; buckets keep a sparse sub-window from being crowded out.
func TestSearchTemporalEdges(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ensureQueueBanks(t, s, fuBank)
	at := map[string]float64{"d0.5": 0.5, "d2": 2, "d5": 5, "d5.1": 5.1, "d5.2": 5.2, "d9": 9, "d10": 10}
	ids := map[port.UnitID]string{}
	var us []domain.Unit
	for name, d := range at {
		u := fuUnit(fuBank, name)
		ts := fuDay(d)
		u.OccurredStart, u.OccurredEnd = &ts, &ts
		ids[u.ID] = name
		us = append(us, u)
	}
	fuInsert(t, s, us...)
	search := func(start, end float64, buckets, limit int) []string {
		t.Helper()
		hs, err := s.Read().SearchTemporal(t.Context(), port.TemporalQuery{
			Bank: fuBank, Window: domain.TimeWindow{Start: fuDay(start), End: fuDay(end)}, Buckets: buckets, Limit: limit,
		})
		if err != nil {
			t.Fatalf("SearchTemporal: %v", err)
		}
		names := make([]string, len(hs))
		for i, h := range hs {
			names[i] = ids[h.ID]
		}
		return names
	}
	if got := search(5, 5, 0, 10); len(got) != 0 {
		t.Errorf("empty window: %v, want no hits", got)
	}
	if got := search(9, 1, 0, 10); len(got) != 0 {
		t.Errorf("inverted window: %v, want no hits", got)
	}
	if got, want := search(0, 10, 0, 10), []string{"d5", "d5.1", "d5.2", "d2", "d9", "d0.5"}; !slices.Equal(got, want) {
		t.Errorf("window [0,10): %v, want %v (d10 excluded, closest to day 5 first)", got, want)
	}
	if got, want := search(0, 10, 0, 2), []string{"d5", "d5.1"}; !slices.Equal(got, want) {
		t.Errorf("limit 2 without buckets: %v, want %v", got, want)
	}
	if got, want := search(0, 10, 2, 2), []string{"d5", "d2"}; !slices.Equal(got, want) {
		t.Errorf("limit 2 over 2 buckets: %v, want %v (one per half)", got, want)
	}
}

// TestInTxRetriesDeadlock: two transactions that take two banks' Seq in opposite order deadlock;
// InTx re-runs the loser, so both succeed and fn runs three times in all.
func TestInTxRetriesDeadlock(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	x, y := port.BankRef{Tenant: "t", Bank: "x"}, port.BankRef{Tenant: "t", Bank: "y"}
	ensureQueueBanks(t, s, x, y)
	var calls atomic.Int64
	ready := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	run := func(me int, first, second port.BankRef) error {
		var attempt int
		return s.InTx(t.Context(), func(tx port.Tx) error {
			calls.Add(1)
			attempt++
			if _, err := tx.Seq(t.Context(), first); err != nil {
				return err
			}
			if attempt == 1 { // first attempt only: wait until both hold their first bank
				close(ready[me])
				<-ready[1-me]
			}
			_, err := tx.Seq(t.Context(), second)
			return err
		})
	}
	errs := make(chan error, 2)
	go func() { errs <- run(0, x, y) }()
	go func() { errs <- run(1, y, x) }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("InTx: %v, want both transactions to commit", err)
		}
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("fn ran %d times, want 3 (one deadlock loser re-run)", n)
	}
}

// TestSearchTextSmallBank: BM25 hits of a small bank are found among thousands of matching rows in
// another bank, also with a tag filter.
func TestSearchTextSmallBank(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	big := port.BankRef{Tenant: fuBank.Tenant, Bank: "big"}
	ensureQueueBanks(t, s, fuBank, big)
	var bigUnits []domain.Unit
	for i := range 2000 {
		bigUnits = append(bigUnits, fuUnit(big, fmt.Sprintf("apple orchard %d", i)))
	}
	fuInsert(t, s, bigUnits...)
	var small []domain.Unit
	for i := range 5 {
		small = append(small, fuUnit(fuBank, fmt.Sprintf("apple pie %d", i)))
	}
	small[0].Tags = []string{"x"}
	fuInsert(t, s, small...)
	cases := []struct {
		name string
		tags *port.SQLPredicate
		want int
	}{
		{"no filter", nil, 5},
		{"tag x", &port.SQLPredicate{Op: port.PredicateTags, Tags: []string{"x"}, Match: domain.TagsMatchAnyStrict}, 1},
	}
	for _, c := range cases {
		hs, err := s.Read().SearchText(t.Context(), port.TextQuery{Bank: fuBank, Text: "apple", Tags: c.tags, Limit: 10})
		if err != nil {
			t.Fatalf("%s: SearchText: %v", c.name, err)
		}
		if len(hs) != c.want {
			t.Errorf("%s: %d hits, want %d", c.name, len(hs), c.want)
		}
	}
}

// TestSearchTextPunctuation: query text with query-syntax characters never errors, and the words in
// it still match. `apple's` and `apple:pie` are single words under Unicode word segmentation (apostrophe and
// colon join letters), so they don't match "apple"; they only must not error.
func TestSearchTextPunctuation(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ensureQueueBanks(t, s, fuBank)
	fuInsert(t, s, fuUnit(fuBank, "apple pie recipe"))
	noMatch := map[string]bool{`apple's`: true, `apple:pie`: true}
	queries := []string{
		`apple's`, `"apple"`, `apple:pie`, `(apple`, `apple)`, `apple AND OR NOT`, `apple~2`, `apple^3`, `apple*`,
		`-apple`, `+apple`, `apple?`, `[apple]`, `{apple}`, `apple\`, `/apple/`, `apple || pie`, `'apple`, `àpple apple`,
	}
	for _, q := range queries {
		hs, err := s.Read().SearchText(t.Context(), port.TextQuery{Bank: fuBank, Text: q, Limit: 5})
		if err != nil {
			t.Errorf("SearchText(%q): %v", q, err)
			continue
		}
		if want := 1 - boolInt(noMatch[q]); len(hs) != want {
			t.Errorf("SearchText(%q): %d hits, want %d", q, len(hs), want)
		}
	}
}

// boolInt is 1 for true, else 0.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestRetryOnTombstonedBank: Retry takes the bank tombstone check Enqueue takes: a failed job of a
// tombstoned bank can't be re-queued, except its delete_bank job.
func TestRetryOnTombstonedBank(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ensureQueueBanks(t, s, fuBank)
	failed := map[domain.OperationKind]port.OperationID{}
	for _, k := range []domain.OperationKind{domain.OperationKindRetainDocument, domain.OperationKindDeleteBank} {
		id := enqueueJob(t, s, port.Job{Bank: fuBank, Kind: k, SerialKey: "k-" + string(k), Payload: json.RawMessage(`{}`)})
		if js, err := s.Claim(t.Context(), "w", []port.JobKind{k}, 1); err != nil || len(js) != 1 {
			t.Fatalf("Claim %s: %v, %v", k, js, err)
		}
		if err := s.Fail(t.Context(), id, errors.New("boom"), nil); err != nil {
			t.Fatalf("Fail %s: %v", k, err)
		}
		failed[k] = id
	}
	fuMarkDeleting(t, s, fuBank)
	retry := func(id port.OperationID) error {
		return fuTx(t, s, func(tx port.Tx) error {
			_, err := tx.Queue().Retry(t.Context(), fuBank, id)
			return err
		})
	}
	if err := retry(failed[domain.OperationKindRetainDocument]); !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("Retry(retain_document) on a tombstoned bank = %v, want ErrBankDeleting", err)
	}
	if err := retry(failed[domain.OperationKindDeleteBank]); err != nil {
		t.Errorf("Retry(delete_bank) on a tombstoned bank = %v, want nil", err)
	}
}

// TestClaimFailsUndecodableRow: a pending row whose stored overrides can't be decoded is failed for
// good by Claim instead of erroring every claim; other jobs are still claimed.
func TestClaimFailsUndecodableRow(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ensureQueueBanks(t, s, fuBank)
	kind := domain.OperationKindRetainDocument
	bad := enqueueJob(t, s, port.Job{Bank: fuBank, Kind: kind, SerialKey: "doc:bad", Payload: json.RawMessage(`{}`)})
	good := enqueueJob(t, s, port.Job{Bank: fuBank, Kind: kind, SerialKey: "doc:good", Payload: json.RawMessage(`{}`)})
	if _, err := pool.Exec(t.Context(), `UPDATE operations SET overrides = '{"recall": 5}' WHERE id = $1`,
		string(bad)); err != nil {
		t.Fatalf("corrupt overrides: %v", err)
	}
	js, err := s.Claim(t.Context(), "w", []port.JobKind{kind}, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(js) != 1 || js[0].ID != good {
		t.Errorf("Claim = %v, want only %s", js, good)
	}
	wantOpStatus(t, pool, bad, domain.OperationStatusFailed)
}
