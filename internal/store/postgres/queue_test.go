package postgres_test

import (
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"slices"
	"testing"
	"time"
)

// Queue timing. Due-ness compares run_at with the database clock, which may drift from the test process's
// clock (Docker VM), so run_at boundaries keep an hour of margin. Heartbeat staleness compares database
// time with database time, so short sleeps are safe there.
const (
	// farAhead parks a job well past any clock skew.
	farAhead = time.Hour
	// staleTTL is the ReclaimStale ttl of the reclaim tests.
	staleTTL = 200 * time.Millisecond
	// staleSleep is how long a job goes without heartbeat before ReclaimStale(staleTTL); twice the ttl.
	staleSleep = 400 * time.Millisecond
	// worker1 and worker2 are worker ids.
	worker1, worker2 = "worker-1", "worker-2"
)

// workKinds are every job kind a worker claims; retain_batch parents do no work and are never claimed here.
var workKinds = []domain.OperationKind{
	domain.OperationKindRetainDocument, domain.OperationKindConsolidate, domain.OperationKindRederiveMemory,
	domain.OperationKindRetagDocument, domain.OperationKindRefreshMentalModel,
	domain.OperationKindGraphMaintenance, domain.OperationKindDeleteBank,
}

// The DB is the job runner's Claimer; the assertion lives in a test so the adapter never imports jobs.
var _ jobs.Claimer = (*postgres.DB)(nil)

// queueEnv is one queue subtest's store and claimer.
type queueEnv struct {
	s store.DB
	c jobs.Claimer
}

// queueCase is one named queue subtest.
type queueCase struct {
	name string
	fn   func(t *testing.T, q queueEnv)
}

// TestQueue covers store.Queue (through Tx.Queue) and jobs.Claimer: claim order, run_at, serial keys, dedupe,
// upgrade, reschedule, operation control (get, list, cancel, retry, prune), deleting banks, tenant isolation,
// heartbeats, reclaim, fencing, retries and concurrent claims and enqueues (docs/design/architecture.md#job-queue).
// Each case runs in parallel on its own empty database.
func TestQueue(t *testing.T) {
	t.Parallel()
	cases := []queueCase{
		{"EnqueueClaimRoundTrip", testEnqueueClaim},
		{"ClaimKindsLimitOldestFirst", testClaimKindsLimit},
		{"ClaimRunAt", testClaimRunAt},
		{"RescheduleReleaseAndPark", testRescheduleRelease},
		{"RescheduleNotPending", testRescheduleNotPending},
		{"SerialKeyOneAtATime", testSerialKey},
		{"SerialKeysIndependent", testSerialKeysIndependent},
		{"ParkedOlderJobDoesNotBlock", testParkedOlderDoesNotBlock},
		{"DedupeNoneNeverJoinedLater", testDedupeNone},
		{"DedupePending", testDedupePending},
		{"DedupePendingJoinsParked", testDedupeParked},
		{"DedupePendingOrRunning", testDedupePendingOrRunning},
		{"DedupeOverridesNeverJoin", testDedupeOverrides},
		{"DedupeRefreshJoinsDespiteOverrides", testDedupeRefreshOverrides},
		{"RefreshDedupeNoneIsPending", testRefreshDedupeNone},
		{"DedupeConcurrentEnqueuers", testDedupeConcurrent},
		{"UpgradeMergesPayloadAndOverrides", testUpgradeMerge},
		{"UpgradeCronJoinerKeepsManualFull", testUpgradeKeepsFull},
		{"UpgradeConcurrentJoiners", testUpgradeConcurrent},
		{"UpgradeNotPending", testUpgradeNotPending},
	}
	cases = append(cases, workerCases()...)
	cases = append(cases, controlCases()...)
	cases = append(cases, edgeCases()...)
	runQueueCases(t, cases)
}

// job builds a job of bank b with a JSON payload.
func job(b domain.BankRef, kind domain.OperationKind, key, payload string) store.Job {
	return store.Job{Bank: b, Kind: kind, SerialKey: key, Payload: json.RawMessage(payload)}
}

// doc builds a retain_document job of bankA with serial key "doc:{name}".
func doc(name string) store.Job {
	return job(bankA, domain.OperationKindRetainDocument, "doc:"+name, fmt.Sprintf(`{"document_id":%q}`, name))
}

// refresh builds a DedupePending refresh_mental_model job of bankA for model m, with serial key "mm:{m}".
func refresh(m, payload string) store.Job {
	j := job(bankA, domain.OperationKindRefreshMentalModel, "mm:"+m, payload)
	j.Dedupe = store.DedupePending
	return j
}

// enqueue enqueues j in its own transaction (so jobs enqueued in turn have increasing created_at) and
// returns the id and whether it joined an existing row.
func (q queueEnv) enqueue(t *testing.T, j store.Job) (domain.OperationID, bool) {
	t.Helper()
	var (
		id     domain.OperationID
		joined bool
	)
	inTx(t, q.s, func(tx store.Tx) error {
		var err error
		id, joined, err = tx.Queue().Enqueue(t.Context(), j)
		return err
	})
	if id == "" {
		t.Fatalf("Enqueue returned an empty id")
	}
	return id, joined
}

// add enqueues j, which must insert a new row, and returns its id.
func (q queueEnv) add(t *testing.T, j store.Job) domain.OperationID {
	t.Helper()
	id, joined := q.enqueue(t, j)
	if joined {
		t.Fatalf("Enqueue(%s %q, dedupe %v) joined %s, want a new row", j.Kind, j.SerialKey, j.Dedupe, id)
	}
	return id
}

// claim claims up to n jobs of kinds as worker and fails the test on error.
func (q queueEnv) claim(t *testing.T, worker string, kinds []domain.OperationKind, n int) []store.Job {
	t.Helper()
	js, err := q.c.Claim(t.Context(), worker, kinds, n)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return js
}

// claimAll claims every claimable job of workKinds as worker1.
func (q queueEnv) claimAll(t *testing.T) []store.Job {
	t.Helper()
	return q.claim(t, worker1, workKinds, 100)
}

// claimOne claims every claimable job and fails the test unless exactly want comes back; it returns it.
func (q queueEnv) claimOne(t *testing.T, want domain.OperationID) store.Job {
	t.Helper()
	js := q.claimAll(t)
	if len(js) != 1 || js[0].ID != want {
		t.Fatalf("Claim = %v, want exactly [%s]", jobIDs(js), want)
	}
	return js[0]
}

// claimNone fails the test if any job is claimable.
func (q queueEnv) claimNone(t *testing.T, what string) {
	t.Helper()
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim %s = %v, want nothing", what, jobIDs(js))
	}
}

// alive reports Alive(id) and fails the test on error.
func (q queueEnv) alive(t *testing.T, id domain.OperationID) bool {
	t.Helper()
	ok, err := q.c.Alive(t.Context(), id)
	if err != nil {
		t.Fatalf("Alive(%s): %v", id, err)
	}
	return ok
}

// wantAlive fails the test unless Alive(id) == want.
func (q queueEnv) wantAlive(t *testing.T, what string, id domain.OperationID, want bool) {
	t.Helper()
	if got := q.alive(t, id); got != want {
		t.Errorf("Alive(%s) %s = %v, want %v", id, what, got, want)
	}
}

// get reads operation id of bank b and fails the test on error.
func (q queueEnv) get(t *testing.T, b domain.BankRef, id domain.OperationID) domain.Operation {
	t.Helper()
	op, err := getOp(t, q.s, b, id)
	if err != nil {
		t.Fatalf("Queue().Get(%s): %v", id, err)
	}
	return op
}

// wantStatus fails the test unless bank A's operation id has the given status.
func (q queueEnv) wantStatus(t *testing.T, what string, id domain.OperationID, want domain.OperationStatus) {
	t.Helper()
	if got := q.get(t, bankA, id).Status; got != want {
		t.Errorf("status of %s %s = %q, want %q", id, what, got, want)
	}
}

// queueTx runs fn against the Queue of a new transaction and returns its error unchanged.
func (q queueEnv) queueTx(t *testing.T, fn func(store.Queue) error) error {
	t.Helper()
	return q.s.InTx(t.Context(), func(tx store.Tx) error { return fn(tx.Queue()) })
}

// reclaim runs ReclaimStale(staleTTL) and returns how many rows it touched.
func (q queueEnv) reclaim(t *testing.T) int {
	t.Helper()
	n, err := q.c.ReclaimStale(t.Context(), staleTTL)
	if err != nil {
		t.Fatalf("ReclaimStale: %v", err)
	}
	return n
}

// complete completes id with an empty result and fails the test on error.
func (q queueEnv) complete(t *testing.T, id domain.OperationID) {
	t.Helper()
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("Complete(%s): %v", id, err)
	}
}

// jobIDs returns the ids of js in order.
func jobIDs(js []store.Job) []domain.OperationID {
	out := make([]domain.OperationID, len(js))
	for i, j := range js {
		out[i] = j.ID
	}
	return out
}

// past is a run_at or retry time safely in the past of the database clock.
func past() time.Time { return nowMicro().Add(-farAhead) }

// future is a run_at or retry time safely in the future of the database clock.
func future() time.Time { return nowMicro().Add(farAhead) }

// ---------- enqueue and claim ----------

// testEnqueueClaim: Claim returns the enqueued job with every field, Attempt 0.
func testEnqueueClaim(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	parent := q.add(t, job(bankA, domain.OperationKindRetainBatch, "", `{"documents":2}`))
	j := doc("d1")
	j.Payload = json.RawMessage(`{"document_id": "d1", "flags": {"force": true, "n": 3}}`)
	j.Overrides = &domain.BankConfig{
		Retain: &domain.RetainConfig{Mission: "recipes"}, Recall: &domain.RecallConfig{BudgetLow: ptr(5)},
	}
	j.ParentID = parent
	id := q.add(t, j)
	got := q.claimOne(t, id)
	if got.Bank != bankA || got.Kind != j.Kind || got.SerialKey != j.SerialKey || got.ParentID != parent {
		t.Errorf("claimed job = bank %v kind %q key %q parent %q, want %v %q %q %q", got.Bank, got.Kind,
			got.SerialKey, got.ParentID, bankA, j.Kind, j.SerialKey, parent)
	}
	if got.Attempt != 0 {
		t.Errorf("claimed job Attempt = %d, want 0", got.Attempt)
	}
	wantJSON(t, "claimed payload", got.Payload, j.Payload)
	wantJSON(t, "claimed overrides", got.Overrides, j.Overrides)
	q.wantAlive(t, "while processing", id, true)
	q.claimNone(t, "again (the job is processing)")
	if js := q.claim(t, worker1, []domain.OperationKind{domain.OperationKindRetainBatch}, 10); len(js) != 1 ||
		js[0].ID != parent || js[0].ParentID != "" {
		t.Errorf("Claim(retain_batch) = %+v, want the parent %s with no parent", js, parent)
	}
}

// testClaimKindsLimit: Claim honours kinds and n and returns the oldest jobs first.
func testClaimKindsLimit(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	d1, d2, d3 := q.add(t, doc("1")), q.add(t, doc("2")), q.add(t, doc("3"))
	gm := q.add(t, job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`))
	retain := []domain.OperationKind{domain.OperationKindRetainDocument}
	wantSeq(t, "Claim(retain_document, 2)", jobIDs(q.claim(t, worker1, retain, 2)), []domain.OperationID{d1, d2})
	consolidate := []domain.OperationKind{domain.OperationKindConsolidate}
	wantSeq(t, "Claim(consolidate)", jobIDs(q.claim(t, worker1, consolidate, 10)), nil)
	wantSeq(t, "Claim(retain_document, 10)", jobIDs(q.claim(t, worker1, retain, 10)), []domain.OperationID{d3})
	wantSeq(t, "Claim(all)", jobIDs(q.claimAll(t)), []domain.OperationID{gm})
}

// testClaimRunAt: a job due in the future is not claimed (it stays PENDING and not Alive); one due in the
// past is.
func testClaimRunAt(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	later, now := doc("later"), doc("now")
	later.RunAt, now.RunAt = future(), past()
	laterID, nowID := q.add(t, later), q.add(t, now)
	zero := q.add(t, doc("zero-run-at"))
	wantSet(t, "Claim with one job parked", jobIDs(q.claimAll(t)), []domain.OperationID{nowID, zero})
	q.wantStatus(t, "parked", laterID, domain.OperationStatusPending)
	q.wantAlive(t, "parked pending job", laterID, false)
}

// testRescheduleRelease: Reschedule releases a parked job and parks a due one.
func testRescheduleRelease(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	parked := doc("parked")
	parked.RunAt = future()
	parkedID, dueID := q.add(t, parked), q.add(t, doc("due"))
	if err := q.queueTx(t, func(qu store.Queue) error {
		if err := qu.Reschedule(t.Context(), parkedID, past()); err != nil {
			return err
		}
		return qu.Reschedule(t.Context(), dueID, future())
	}); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	wantSeq(t, "Claim after Reschedule", jobIDs(q.claimAll(t)), []domain.OperationID{parkedID})
}

// testRescheduleNotPending: Reschedule of a processing or completed job is ErrFailedPrecondition and changes
// nothing; an unknown id is ErrNotFound.
func testRescheduleNotPending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	err := q.queueTx(t, func(qu store.Queue) error { return qu.Reschedule(t.Context(), id, past()) })
	wantErrIs(t, "Reschedule(processing)", err, domain.ErrFailedPrecondition)
	if op := q.get(t, bankA, id); op.Status != domain.OperationStatusProcessing || op.Attempts != 0 ||
		op.WorkerID != worker1 {
		t.Errorf("after Reschedule(processing): status %q attempts %d worker %q, want processing, 0, %s",
			op.Status, op.Attempts, op.WorkerID, worker1)
	}
	q.wantAlive(t, "after Reschedule(processing)", id, true)
	q.claimNone(t, "after Reschedule(processing)")
	q.complete(t, id)
	err = q.queueTx(t, func(qu store.Queue) error { return qu.Reschedule(t.Context(), id, past()) })
	wantErrIs(t, "Reschedule(completed)", err, domain.ErrFailedPrecondition)
	q.wantStatus(t, "after Reschedule(completed)", id, domain.OperationStatusCompleted)
	q.claimNone(t, "after Reschedule(completed)")
	unknown := newID[domain.OperationID]()
	err = q.queueTx(t, func(qu store.Queue) error { return qu.Reschedule(t.Context(), unknown, past()) })
	wantErrIs(t, "Reschedule(unknown)", err, domain.ErrNotFound)
}

// ---------- serial keys ----------

// testSerialKey: two jobs with one key run one at a time, older first.
func testSerialKey(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	first, second := q.add(t, doc("same")), q.add(t, doc("same"))
	q.claimOne(t, first)
	q.claimNone(t, "while the key is processing")
	q.complete(t, first)
	q.claimOne(t, second)
}

// testSerialKeysIndependent: different keys, keys of different banks and empty keys never block each other.
func testSerialKeysIndependent(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	ids := []domain.OperationID{q.add(t, doc("a")), q.add(t, doc("b"))}
	ids = append(ids, q.add(t, withBank(doc("a"), bankB)))
	for i := range 3 {
		ids = append(ids, q.add(t, job(bankA, domain.OperationKindRetainDocument, "", fmt.Sprintf(`{"i":%d}`, i))))
	}
	wantSet(t, "Claim of independent keys", jobIDs(q.claimAll(t)), ids)
}

// testParkedOlderDoesNotBlock: per the claim SQL, an older PENDING job of the same key blocks only once it
// is due, so a parked older job does not block a younger due one; a processing job always blocks.
func testParkedOlderDoesNotBlock(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	older := doc("k")
	older.RunAt = future()
	olderID := q.add(t, older)
	youngerID := q.add(t, doc("k"))
	q.claimOne(t, youngerID)
	if err := q.queueTx(t, func(qu store.Queue) error { return qu.Reschedule(t.Context(), olderID, past()) }); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	q.claimNone(t, "of a due job whose key is processing")
}

// ---------- dedupe ----------

// withBank returns j with another bank.
func withBank(j store.Job, b domain.BankRef) store.Job { j.Bank = b; return j }

// withKind returns j with another kind.
func withKind(j store.Job, k domain.OperationKind) store.Job { j.Kind = k; return j }

// withKey returns j with another serial key.
func withKey(j store.Job, key string) store.Job { j.SerialKey = key; return j }

// testDedupeNone: DedupeNone inserts every time, and its rows are never joined by a later DedupePending
// Enqueue (they carry the non-joinable marker); the DedupePending row that follows is joinable.
func testDedupeNone(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{"scopes":[["team"]]}`)
	a, b := q.add(t, j), q.add(t, j)
	if a == b {
		t.Errorf("DedupeNone returned the same id twice: %s", a)
	}
	pending := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	pending.Dedupe = store.DedupePending
	c := q.add(t, pending)
	if c == a || c == b {
		t.Errorf("DedupePending returned %s, one of the DedupeNone rows %s %s", c, a, b)
	}
	if id, joined := q.enqueue(t, pending); !joined || id != c {
		t.Errorf("second DedupePending = (%s, %v), want to join %s", id, joined, c)
	}
}

// testDedupePending: DedupePending joins a pending row (left unchanged) but not a processing one, and
// matches on bank, kind and serial key.
func testDedupePending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{"v":1}`)
	j.Dedupe = store.DedupePending
	first := q.add(t, j)
	j2 := j
	j2.Payload = json.RawMessage(`{"v":2}`)
	if id, joined := q.enqueue(t, j2); !joined || id != first {
		t.Errorf("DedupePending on a pending row = (%s, %v), want (%s, true)", id, joined, first)
	}
	variants := map[string]store.Job{
		"other bank": withBank(j, bankB),
		"other kind": withKind(j, domain.OperationKindGraphMaintenance),
		"other key":  withKey(j, "consolidate-2"),
	}
	for name, v := range variants {
		if id, joined := q.enqueue(t, v); joined {
			t.Errorf("DedupePending with %s joined %s", name, id)
		}
	}
	got := q.claim(t, worker1, []domain.OperationKind{domain.OperationKindConsolidate}, 10)
	i := slices.Index(jobIDs(got), first)
	if i < 0 {
		t.Fatalf("Claim = %v, want %s among them", jobIDs(got), first)
	}
	wantJSON(t, "payload of the joined row", got[i].Payload, j.Payload)
	if _, joined := q.enqueue(t, j2); joined {
		t.Errorf("DedupePending joined a processing row")
	}
}

// testDedupeParked: DedupePending joins a parked (future run_at) pending row.
func testDedupeParked(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	j.Dedupe, j.RunAt = store.DedupePending, future()
	parked := q.add(t, j)
	j.RunAt = time.Time{}
	if id, joined := q.enqueue(t, j); !joined || id != parked {
		t.Errorf("DedupePending with a parked row queued = (%s, %v), want (%s, true)", id, joined, parked)
	}
	q.claimNone(t, "with only the parked row queued")
}

// testDedupePendingOrRunning: DedupePendingOrRunning joins a pending or processing row, not a finished one.
func testDedupePendingOrRunning(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`)
	j.Dedupe = store.DedupePendingOrRunning
	first := q.add(t, j)
	if id, joined := q.enqueue(t, j); !joined || id != first {
		t.Errorf("join pending = (%s, %v), want (%s, true)", id, joined, first)
	}
	q.claimOne(t, first)
	if id, joined := q.enqueue(t, j); !joined || id != first {
		t.Errorf("join processing = (%s, %v), want (%s, true)", id, joined, first)
	}
	q.complete(t, first)
	if id, joined := q.enqueue(t, j); joined || id == first {
		t.Errorf("after Complete = (%s, %v), want a new row", id, joined)
	}
}

// testDedupeOverrides: a consolidate job with overrides never joins and is never joined.
func testDedupeOverrides(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	plain := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	plain.Dedupe = store.DedupePending
	withO := plain
	withO.Overrides = &domain.BankConfig{Observations: &domain.ObservationConfig{Mission: "override"}}
	o1 := q.add(t, withO)
	p1 := q.add(t, plain) // must not join the overrides row
	if o1 == p1 {
		t.Errorf("plain job joined the overrides row %s", o1)
	}
	if id, joined := q.enqueue(t, withO); joined || id == p1 || id == o1 {
		t.Errorf("overrides job = (%s, %v), want a new row (never joins %s or %s)", id, joined, p1, o1)
	}
	if id, joined := q.enqueue(t, plain); !joined || id != p1 {
		t.Errorf("plain job = (%s, %v), want to join the plain row %s", id, joined, p1)
	}
}

// testDedupeRefreshOverrides: refresh_mental_model is the exception to the overrides rule: a refresh with
// overrides joins its model's queued refresh, and a refresh queued with overrides is joined by a later one.
func testDedupeRefreshOverrides(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	o := &domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "override"}}
	plainA := refresh("a", `{"model_id":"a"}`)
	first := q.add(t, plainA)
	withO := plainA
	withO.Overrides = o
	if id, joined := q.enqueue(t, withO); !joined || id != first {
		t.Errorf("refresh with overrides = (%s, %v), want to join the queued refresh %s", id, joined, first)
	}
	withOB := refresh("b", `{"model_id":"b"}`)
	withOB.Overrides = o
	second := q.add(t, withOB)
	if id, joined := q.enqueue(t, refresh("b", `{"model_id":"b"}`)); !joined || id != second {
		t.Errorf("plain refresh = (%s, %v), want to join the refresh queued with overrides %s", id, joined, second)
	}
}

// testRefreshDedupeNone: a refresh_mental_model job always dedupes as DedupePending, so a second DedupeNone
// refresh of the same model joins the queued one.
func testRefreshDedupeNone(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := refresh("a", `{"model_id":"a"}`)
	j.Dedupe = store.DedupeNone
	first := q.add(t, j)
	if id, joined := q.enqueue(t, j); !joined || id != first {
		t.Errorf("second DedupeNone refresh = (%s, %v), want to join %s", id, joined, first)
	}
}

// enqueueResult is one concurrent enqueuer's outcome.
type enqueueResult struct {
	id     domain.OperationID
	joined bool
	err    error
}

// testDedupeConcurrent: concurrent DedupePending enqueuers of one (bank, kind, serial key), each in its own
// transaction, end with exactly one row: every caller gets its id and exactly one inserted it.
func testDedupeConcurrent(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	const enqueuers = 8
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	j.Dedupe = store.DedupePending
	start := make(chan struct{})
	results := make(chan enqueueResult, enqueuers)
	for range enqueuers {
		go func() {
			<-start
			var r enqueueResult
			r.err = q.s.InTx(t.Context(), func(tx store.Tx) error {
				var err error
				r.id, r.joined, err = tx.Queue().Enqueue(t.Context(), j)
				return err
			})
			results <- r
		}()
	}
	close(start)
	ids := map[domain.OperationID]bool{}
	inserted := 0
	for range enqueuers {
		r := waitFor(t, "a concurrent Enqueue", results)
		if r.err != nil {
			t.Fatalf("concurrent Enqueue: %v", r.err)
		}
		ids[r.id] = true
		if !r.joined {
			inserted++
		}
	}
	if len(ids) != 1 || inserted != 1 {
		t.Errorf("concurrent DedupePending enqueuers got %d ids with %d inserts, want 1 id and 1 insert", len(ids), inserted)
	}
	if ops := listOps(t, q.s, bankA, store.OperationFilter{}); len(ops) != 1 || !ids[ops[0].ID] {
		t.Errorf("Queue().List after concurrent enqueues = %v, want the one row %v", opIDs(ops), ids)
	}
}

// ---------- upgrade ----------

// upgrade runs Upgrade(id, payload, o) in its own transaction and fails the test on error.
func (q queueEnv) upgrade(t *testing.T, id domain.OperationID, payload string, o *domain.BankConfig) {
	t.Helper()
	if err := q.queueTx(t, func(qu store.Queue) error {
		return qu.Upgrade(t.Context(), id, json.RawMessage(payload), o)
	}); err != nil {
		t.Fatalf("Upgrade(%s, %s): %v", id, payload, err)
	}
}

// claimedByID claims every claimable job and indexes them by id; it fails the test unless exactly want
// many come back.
func (q queueEnv) claimedByID(t *testing.T, want int) map[domain.OperationID]store.Job {
	t.Helper()
	got := map[domain.OperationID]store.Job{}
	for _, j := range q.claimAll(t) {
		got[j.ID] = j
	}
	if len(got) != want {
		t.Fatalf("Claim = %d jobs, want %d", len(got), want)
	}
	return got
}

// testUpgradeMerge: Upgrade shallow-merges the payload (present keys replace, absent keys are kept, force_full
// is OR-ed so a true one survives a false one); nil overrides keep the row's, non-nil replace them.
func testUpgradeMerge(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	oldO := &domain.BankConfig{Reflect: &domain.ReflectConfig{Mission: "old"}}
	a := refresh("a", `{"model_id":"a","force_full":true}`)
	a.Overrides = oldO
	b := refresh("b", `{"model_id":"b","strategy":"delta"}`)
	b.Overrides = oldO
	aID, bID := q.add(t, a), q.add(t, b)
	newO := &domain.BankConfig{
		Recall: &domain.RecallConfig{Rerank: ptr(false)}, Reflect: &domain.ReflectConfig{Mission: "new"},
	}
	q.upgrade(t, aID, `{"force_full":false,"strategy":"single_shot"}`, nil)
	q.upgrade(t, bID, `{}`, newO)
	got := q.claimedByID(t, 2)
	wantJSON(t, "merged payload", got[aID].Payload,
		json.RawMessage(`{"model_id":"a","force_full":true,"strategy":"single_shot"}`))
	wantJSON(t, "overrides kept by a nil Upgrade", got[aID].Overrides, oldO)
	wantJSON(t, "payload kept by an empty Upgrade", got[bID].Payload, b.Payload)
	wantJSON(t, "overrides replaced by Upgrade", got[bID].Overrides, newO)
}

// testUpgradeKeepsFull: a cron refresh joining a queued manual full refresh, then upgrading with its own
// payload (no force_full), keeps the full refresh.
func testUpgradeKeepsFull(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	manual := q.add(t, refresh("c", `{"model_id":"c","force_full":true}`))
	cron := refresh("c", `{"model_id":"c"}`)
	inTx(t, q.s, func(tx store.Tx) error {
		id, joined, err := tx.Queue().Enqueue(t.Context(), cron)
		if err != nil {
			return err
		}
		if !joined || id != manual {
			return fmt.Errorf("cron Enqueue = (%s, %v), want to join %s", id, joined, manual)
		}
		return tx.Queue().Upgrade(t.Context(), id, cron.Payload, nil)
	})
	got := q.claimOne(t, manual)
	wantJSON(t, "payload after the cron joiner", got.Payload, json.RawMessage(`{"model_id":"c","force_full":true}`))
}

// testUpgradeConcurrent: two joiners, each in its own transaction, join and upgrade one row at once; both
// payload keys are present afterwards and force_full is OR-ed.
func testUpgradeConcurrent(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	base := refresh("d", `{"model_id":"d","force_full":false}`)
	id := q.add(t, base)
	joiners := []string{`{"force_full":true,"source":"cron"}`, `{"force_full":false,"strategy":"delta"}`}
	start := make(chan struct{})
	errs := make(chan error, len(joiners))
	for _, payload := range joiners {
		go func() {
			<-start
			errs <- q.s.InTx(t.Context(), func(tx store.Tx) error {
				got, joined, err := tx.Queue().Enqueue(t.Context(), base)
				if err != nil {
					return err
				}
				if !joined || got != id {
					return fmt.Errorf("joiner Enqueue = (%s, %v), want to join %s", got, joined, id)
				}
				return tx.Queue().Upgrade(t.Context(), got, json.RawMessage(payload), nil)
			})
		}()
	}
	close(start)
	for range joiners {
		if err := waitFor(t, "a concurrent joiner", errs); err != nil {
			t.Errorf("concurrent joiner: %v", err)
		}
	}
	got := q.claimOne(t, id)
	wantJSON(t, "payload after two concurrent joiners", got.Payload,
		json.RawMessage(`{"model_id":"d","force_full":true,"source":"cron","strategy":"delta"}`))
}

// testUpgradeNotPending: Upgrade of a processing job is ErrFailedPrecondition; of an unknown id ErrNotFound.
func testUpgradeNotPending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, refresh("a", `{"model_id":"a"}`))
	q.claimOne(t, id)
	err := q.queueTx(t, func(qu store.Queue) error {
		return qu.Upgrade(t.Context(), id, json.RawMessage(`{"force_full":true}`), nil)
	})
	wantErrIs(t, "Upgrade(processing)", err, domain.ErrFailedPrecondition)
	q.wantStatus(t, "after Upgrade(processing)", id, domain.OperationStatusProcessing)
	unknown := newID[domain.OperationID]()
	err = q.queueTx(t, func(qu store.Queue) error { return qu.Upgrade(t.Context(), unknown, json.RawMessage(`{}`), nil) })
	wantErrIs(t, "Upgrade(unknown)", err, domain.ErrNotFound)
}
