package postgres_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// edgeCases cover tenant and bank isolation of Cancel and Retry, Fail with a retry on a cancel-requested job,
// terminal failures of jobs in tombstoned banks (Fail(nil) and ReclaimStale), and the join-then-Upgrade race
// with a concurrent claimer.
func edgeCases() []queueCase {
	return []queueCase{
		{"CancelRetryScopedToBank", testGapCancelRetryScoped},
		{"FailRetryOnCancelRequested", testGapFailRetryCancelRequested},
		{"FailTombstonedBankNoEvent", testGapFailTombstoned},
		{"ReclaimTombstonedBankAtMax", testGapReclaimTombstoned},
		{"JoinUpgradeVersusClaim", testGapJoinUpgradeRace},
	}
}

// gapCancelIn runs Queue.Cancel(b, id) in its own transaction and returns its error unchanged.
func (q queueEnv) gapCancelIn(t *testing.T, b domain.BankRef, id domain.OperationID) error {
	t.Helper()
	return q.queueTx(t, func(qu store.Queue) error {
		_, err := qu.Cancel(t.Context(), b, id)
		return err
	})
}

// gapRetryIn runs Queue.Retry(b, id) in its own transaction and returns its error unchanged.
func (q queueEnv) gapRetryIn(t *testing.T, b domain.BankRef, id domain.OperationID) error {
	t.Helper()
	return q.queueTx(t, func(qu store.Queue) error {
		_, err := qu.Retry(t.Context(), b, id)
		return err
	})
}

// gapMarkDeleting tombstones bank b.
func (q queueEnv) gapMarkDeleting(t *testing.T, b domain.BankRef) {
	t.Helper()
	inTx(t, q.s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), b, day(1)) })
}

// gapFailedEvents returns the operation ids of bank b's OPERATION_FAILED events.
func gapFailedEvents(t *testing.T, s store.DB, b domain.BankRef) []domain.OperationID {
	t.Helper()
	var ids []domain.OperationID
	for _, e := range listEvents(t, s, b, 0, 1000) {
		if e.Type == domain.EventTypeOperationFailed {
			ids = append(ids, e.OperationID)
		}
	}
	return ids
}

// testGapCancelRetryScoped: Cancel and Retry of a bank-A operation through another tenant's bank with the
// same bank id, or through another bank of the same tenant, are ErrNotFound and change nothing.
func testGapCancelRetryScoped(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB, bankOtherTenant)
	failed := q.add(t, doc("failed"))
	q.failForGood(t, failed)
	pending := q.add(t, doc("pending"))
	for _, other := range []domain.BankRef{bankOtherTenant, bankB} {
		wantErrIs(t, fmt.Sprintf("Cancel(%v, bank A's pending job)", other), q.gapCancelIn(t, other, pending),
			domain.ErrNotFound)
		wantErrIs(t, fmt.Sprintf("Retry(%v, bank A's failed job)", other), q.gapRetryIn(t, other, failed),
			domain.ErrNotFound)
	}
	q.wantStatus(t, "after another bank's Cancel", pending, domain.OperationStatusPending)
	q.wantStatus(t, "after another bank's Retry", failed, domain.OperationStatusFailed)
	q.claimOne(t, pending)
}

// testGapFailRetryOnCancelRequested: Fail with a retry time on a cancel-requested PROCESSING job ends it
// CANCELLED (not re-queued, attempts unchanged) and returns ErrCancelled; its serial key is released.
func testGapFailRetryCancelRequested(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	first, second := q.add(t, doc("k")), q.add(t, doc("k"))
	q.claimOne(t, first)
	q.mustCancel(t, first, domain.OperationStatusProcessing)
	err := q.c.Fail(t.Context(), first, errors.New("transient"), ptr(past()))
	wantErrIs(t, "Fail(retry) of a cancel-requested job", err, domain.ErrCancelled)
	if op := q.get(t, bankA, first); op.Status != domain.OperationStatusCancelled || op.Attempts != 0 {
		t.Errorf("after Fail(retry) of a cancel-requested job: %q attempts %d, want cancelled, 0", op.Status,
			op.Attempts)
	}
	q.wantAlive(t, "cancelled job", first, false)
	q.claimOne(t, second)
}

// testGapFailTombstoned: Fail(nil) on a job whose bank was tombstoned while it ran ends FAILED with no
// OPERATION_FAILED event and no seq bump.
func testGapFailTombstoned(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	q.gapMarkDeleting(t, bankA)
	before := bankSeq(t, q.s, bankA)
	if err := q.c.Fail(t.Context(), id, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail(nil) in a tombstoned bank: %v", err)
	}
	q.wantStatus(t, "after Fail(nil) in a tombstoned bank", id, domain.OperationStatusFailed)
	wantSeq(t, "OPERATION_FAILED events of the tombstoned bank", gapFailedEvents(t, q.s, bankA), nil)
	if got := bankSeq(t, q.s, bankA); got != before {
		t.Errorf("BankSeq after Fail(nil) in a tombstoned bank = %d, want %d", got, before)
	}
}

// testGapReclaimTombstoned: a stale job at max attempts in a bank tombstoned while it ran becomes FAILED
// without an event, and the same sweep re-queues another bank's stale job without error.
func testGapReclaimTombstoned(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	id := q.add(t, doc("1"))
	maxAttempts := domain.OperationKindRetainDocument.MaxAttempts()
	if maxAttempts == 0 {
		t.Fatalf("test assumes retain_document has a bounded MaxAttempts")
	}
	for round := range maxAttempts - 1 {
		if got := q.claimOne(t, id); got.Attempt != round {
			t.Fatalf("round %d: Attempt = %d", round, got.Attempt)
		}
		time.Sleep(staleSleep)
		if n := q.reclaim(t); n != 1 {
			t.Fatalf("round %d: ReclaimStale touched %d rows, want 1", round, n)
		}
	}
	other := q.add(t, withBank(doc("other"), bankB))
	wantSet(t, "last Claim", jobIDs(q.claimAll(t)), []domain.OperationID{id, other})
	q.gapMarkDeleting(t, bankA)
	time.Sleep(staleSleep)
	if n := q.reclaim(t); n != 2 {
		t.Errorf("ReclaimStale with a tombstoned bank touched %d rows, want 2", n)
	}
	q.wantStatus(t, "stale job at max attempts in a tombstoned bank", id, domain.OperationStatusFailed)
	wantSeq(t, "OPERATION_FAILED events of the tombstoned bank", gapFailedEvents(t, q.s, bankA), nil)
	if op := q.get(t, bankB, other); op.Status != domain.OperationStatusPending || op.Attempts != 1 {
		t.Errorf("other bank's stale job = %q attempts %d, want pending, 1", op.Status, op.Attempts)
	}
}

// gapJoinRounds is how many join-versus-claim races testGapJoinUpgradeRace runs.
const gapJoinRounds = 10

// joinOutcome is what the joiner of one race round saw.
type joinOutcome struct {
	id     domain.OperationID
	joined bool
}

// testGapJoinUpgradeRace: a joiner that enqueues and upgrades a queued refresh in one transaction races a
// claimer. Either the claimer wins (the joiner then inserts a new row and the claimed row keeps its payload)
// or the joiner wins (the row is claimed once, after the upgrade, with the upgraded payload). A claim never
// lands between the join and the Upgrade.
func testGapJoinUpgradeRace(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	refreshKind := []domain.OperationKind{domain.OperationKindRefreshMentalModel}
	joins := 0
	defer func() { t.Logf("the joiner won %d of %d rounds", joins, gapJoinRounds) }()
	for round := range gapJoinRounds {
		model := fmt.Sprintf("m%d", round)
		base := refresh(model, fmt.Sprintf(`{"model_id":%q}`, model))
		baseID := q.add(t, base)
		start := make(chan struct{})
		claims := make(chan claimResult, 1)
		go func() {
			<-start
			js, err := q.c.Claim(t.Context(), worker2, refreshKind, 10)
			claims <- claimResult{js, err}
		}()
		close(start)
		var out joinOutcome
		err := q.s.InTx(t.Context(), func(tx store.Tx) error {
			var err error
			if out.id, out.joined, err = tx.Queue().Enqueue(t.Context(), base); err != nil || !out.joined {
				return err
			}
			time.Sleep(5 * time.Millisecond) // widen the window between the join and the Upgrade
			return tx.Queue().Upgrade(t.Context(), out.id, json.RawMessage(`{"force_full":true}`), nil)
		})
		if err != nil {
			t.Fatalf("round %d: joiner: %v", round, err)
		}
		first := waitFor(t, "the racing Claim", claims)
		if first.err != nil {
			t.Fatalf("round %d: racing Claim: %v", round, first.err)
		}
		claimed := append(first.jobs, q.claim(t, worker1, refreshKind, 10)...)
		gapCheckJoinRound(t, round, baseID, out, claimed)
		if out.joined {
			joins++
		}
		for _, j := range claimed {
			q.complete(t, j.ID)
		}
	}
}

// gapCheckJoinRound checks one race round: the base row is claimed exactly once, upgraded iff it was joined.
func gapCheckJoinRound(t *testing.T, round int, baseID domain.OperationID, out joinOutcome, claimed []store.Job) {
	t.Helper()
	var base []store.Job
	for _, j := range claimed {
		if j.ID == baseID {
			base = append(base, j)
		}
	}
	if len(base) != 1 {
		t.Fatalf("round %d: base row claimed %d times, want once (claimed %v)", round, len(base), jobIDs(claimed))
	}
	upgraded := jsonTrueField(t, base[0].Payload, "force_full")
	switch {
	case out.joined && out.id != baseID:
		t.Errorf("round %d: joiner joined %s, want the base row %s", round, out.id, baseID)
	case out.joined && !upgraded:
		t.Errorf("round %d: joined row claimed without the joiner's upgrade: %s", round, base[0].Payload)
	case !out.joined && upgraded:
		t.Errorf("round %d: joiner inserted %s yet the base row carries an upgrade: %s", round, out.id,
			base[0].Payload)
	}
}

// jsonTrueField reports whether the JSON object raw has field set to true.
func jsonTrueField(t *testing.T, raw json.RawMessage, field string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload %s is not a JSON object: %v", raw, err)
	}
	v, ok := m[field].(bool)
	return ok && v
}

// TestQueueGoneBank covers a bank whose row vanished under a processing job, which the ports cannot build
// (BankRepo.Delete removes the bank's operations too): Fail(nil) and ReclaimStale at max attempts still end
// the job FAILED without an event and without error, and the sweep handles the other bank's job.
func TestQueueGoneBank(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	ctx := t.Context()
	gone := domain.NewBankRef("t-gone", "org", "gone")
	live := domain.NewBankRef("t-gone", "org", "live")
	ensureQueueBanks(t, s, gone, live)
	retain := domain.OperationKindRetainDocument
	failNow := enqueueJob(t, s, store.Job{Bank: gone, Kind: retain, SerialKey: "doc:a", Payload: json.RawMessage(`{}`)})
	atMax := enqueueJob(t, s, store.Job{
		Bank: gone, Kind: retain, SerialKey: "doc:b", Payload: json.RawMessage(`{}`), Attempt: retain.MaxAttempts() - 1,
	})
	other := enqueueJob(t, s, store.Job{Bank: live, Kind: retain, SerialKey: "doc:c", Payload: json.RawMessage(`{}`)})
	if js, err := s.Claim(ctx, "w", []domain.OperationKind{retain}, 10); err != nil || len(js) != 3 {
		t.Fatalf("Claim = %d jobs, %v; want 3", len(js), err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM banks WHERE tenant_id = $1 AND org_id = $2 AND id = $3`,
		gone.Tenant, gone.Org, gone.Bank); err != nil {
		t.Fatalf("delete bank row: %v", err)
	}
	if err := s.Fail(ctx, failNow, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail(nil) with the bank gone: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	n, err := s.ReclaimStale(ctx, 200*time.Millisecond)
	if err != nil || n != 2 {
		t.Fatalf("ReclaimStale = %d, %v; want 2, nil", n, err)
	}
	wantOpStatus(t, pool, failNow, domain.OperationStatusFailed)
	wantOpStatus(t, pool, atMax, domain.OperationStatusFailed)
	wantOpStatus(t, pool, other, domain.OperationStatusPending)
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE type = 'operation_failed'`).
		Scan(&events); err != nil || events != 0 {
		t.Errorf("OPERATION_FAILED events = %d (%v), want 0", events, err)
	}
}

// ensureQueueBanks creates the banks with Ensure.
func ensureQueueBanks(t *testing.T, s *postgres.DB, banks ...domain.BankRef) {
	t.Helper()
	pgtest.EnsureBanks(t, s, banks...)
}

// enqueueJob enqueues j in its own transaction and returns its id.
func enqueueJob(t *testing.T, s *postgres.DB, j store.Job) domain.OperationID {
	t.Helper()
	var id domain.OperationID
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), j)
		return err
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

// wantOpStatus fails the test unless operation id has status want (read straight from the table).
func wantOpStatus(t *testing.T, pool *pgxpool.Pool, id domain.OperationID, want domain.OperationStatus) {
	t.Helper()
	var got string
	err := pool.QueryRow(t.Context(), `SELECT status FROM operations WHERE id = $1`, string(id)).Scan(&got)
	if err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	if got != string(want) {
		t.Errorf("status of %s = %q, want %q", id, got, want)
	}
}
