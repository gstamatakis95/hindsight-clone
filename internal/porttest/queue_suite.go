package porttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
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
var workKinds = []port.JobKind{
	domain.OperationKindRetainDocument, domain.OperationKindConsolidate, domain.OperationKindRederiveMemory,
	domain.OperationKindRetagDocument, domain.OperationKindRefreshMentalModel,
	domain.OperationKindGraphMaintenance, domain.OperationKindDeleteBank,
}

// queueEnv is one queue subtest's store and claimer.
type queueEnv struct {
	s port.Store
	c port.Claimer
}

// queueCase is one named queue subtest.
type queueCase struct {
	name string
	fn   func(t *testing.T, q queueEnv)
}

// RunQueueSuite runs the contract tests of port.Queue (through Tx.Queue) and port.Claimer: claim order,
// run_at, serial keys, dedupe, upgrade, reschedule, cancel, deleting banks, heartbeats, reclaim, retries and
// concurrent claims (SPEC "Jobs" claim SQL and rules). newQueue is called once per subtest and must return
// an empty database. Claim and ReclaimStale are global across banks, so subtests never run in parallel.
func RunQueueSuite(t *testing.T, newQueue QueueFactory) {
	t.Helper()
	cases := []queueCase{
		{"EnqueueClaimRoundTrip", testEnqueueClaim},
		{"ClaimKindsLimitOldestFirst", testClaimKindsLimit},
		{"ClaimRunAt", testClaimRunAt},
		{"RescheduleReleaseAndPark", testRescheduleRelease},
		{"RescheduleProcessingKeepsAttempt", testRescheduleProcessing},
		{"SerialKeyOneAtATime", testSerialKey},
		{"SerialKeysIndependent", testSerialKeysIndependent},
		{"ParkedOlderJobDoesNotBlock", testParkedOlderDoesNotBlock},
		{"DedupeNone", testDedupeNone},
		{"DedupePending", testDedupePending},
		{"DedupePendingOrRunning", testDedupePendingOrRunning},
		{"DedupeOverridesNeverJoin", testDedupeOverrides},
		{"UpgradePending", testUpgrade},
		{"UpgradeNotPending", testUpgradeNotPending},
		{"CancelBank", testCancelBank},
		{"DeletingBankClaims", testDeletingBank},
		{"HeartbeatPreventsReclaim", testHeartbeat},
		{"HeartbeatOtherWorkerIgnored", testHeartbeatOtherWorker},
		{"ReclaimStale", testReclaimStale},
		{"ReclaimMaxAttempts", testReclaimMaxAttempts},
		{"FailWithRetry", testFailRetry},
		{"FailTerminal", testFailTerminal},
		{"Complete", testComplete},
		{"CancelledNotResurrected", testCancelledStays},
		{"Progress", testProgress},
		{"AliveUnknown", testAliveUnknown},
		{"ConcurrentClaims", testConcurrentClaims},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, cl := newQueue(t)
			c.fn(t, queueEnv{s: s, c: cl})
		})
	}
}

// job builds a job of bank b with a JSON payload.
func job(b port.BankRef, kind port.JobKind, key, payload string) port.Job {
	return port.Job{Bank: b, Kind: kind, SerialKey: key, Payload: json.RawMessage(payload)}
}

// doc builds a retain_document job of bankA with serial key "doc:{name}".
func doc(name string) port.Job {
	return job(bankA, domain.OperationKindRetainDocument, "doc:"+name, fmt.Sprintf(`{"document_id":%q}`, name))
}

// enqueue enqueues j in its own transaction (so jobs enqueued in turn have increasing created_at) and
// returns the id and whether it joined an existing row.
func (q queueEnv) enqueue(t *testing.T, j port.Job) (port.OperationID, bool) {
	t.Helper()
	var (
		id     port.OperationID
		joined bool
	)
	inTx(t, q.s, func(tx port.Tx) error {
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
func (q queueEnv) add(t *testing.T, j port.Job) port.OperationID {
	t.Helper()
	id, joined := q.enqueue(t, j)
	if joined {
		t.Fatalf("Enqueue(%s %q, dedupe %v) joined %s, want a new row", j.Kind, j.SerialKey, j.Dedupe, id)
	}
	return id
}

// claim claims up to n jobs of kinds as worker and fails the test on error.
func (q queueEnv) claim(t *testing.T, worker string, kinds []port.JobKind, n int) []port.Job {
	t.Helper()
	js, err := q.c.Claim(t.Context(), worker, kinds, n)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return js
}

// claimAll claims every claimable job of workKinds as worker1.
func (q queueEnv) claimAll(t *testing.T) []port.Job {
	t.Helper()
	return q.claim(t, worker1, workKinds, 100)
}

// claimOne claims every claimable job and fails the test unless exactly want comes back; it returns it.
func (q queueEnv) claimOne(t *testing.T, want port.OperationID) port.Job {
	t.Helper()
	js := q.claimAll(t)
	if len(js) != 1 || js[0].ID != want {
		t.Fatalf("Claim = %v, want exactly [%s]", jobIDs(js), want)
	}
	return js[0]
}

// alive reports Alive(id) and fails the test on error.
func (q queueEnv) alive(t *testing.T, id port.OperationID) bool {
	t.Helper()
	ok, err := q.c.Alive(t.Context(), id)
	if err != nil {
		t.Fatalf("Alive(%s): %v", id, err)
	}
	return ok
}

// wantAlive fails the test unless Alive(id) == want.
func (q queueEnv) wantAlive(t *testing.T, what string, id port.OperationID, want bool) {
	t.Helper()
	if got := q.alive(t, id); got != want {
		t.Errorf("Alive(%s) %s = %v, want %v", id, what, got, want)
	}
}

// queueTx runs fn against the Queue of a new transaction and returns its error unchanged.
func (q queueEnv) queueTx(t *testing.T, fn func(port.Queue) error) error {
	t.Helper()
	return q.s.InTx(t.Context(), func(tx port.Tx) error { return fn(tx.Queue()) })
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

// jobIDs returns the ids of js in order.
func jobIDs(js []port.Job) []port.OperationID {
	out := make([]port.OperationID, len(js))
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
	j.Overrides = &domain.BankConfig{RetainMission: "recipes", Recall: &domain.RecallConfig{BudgetLow: ptr(5)}}
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
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("second Claim = %v, want nothing (the job is processing)", jobIDs(js))
	}
	if js := q.claim(t, worker1, []port.JobKind{domain.OperationKindRetainBatch}, 10); len(js) != 1 ||
		js[0].ID != parent || js[0].ParentID != "" {
		t.Errorf("Claim(retain_batch) = %+v, want the parent %s with no parent", js, parent)
	}
}

// testClaimKindsLimit: Claim honours kinds and n and returns the oldest jobs first.
func testClaimKindsLimit(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	d1, d2, d3 := q.add(t, doc("1")), q.add(t, doc("2")), q.add(t, doc("3"))
	gm := q.add(t, job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`))
	retain := []port.JobKind{domain.OperationKindRetainDocument}
	wantSeq(t, "Claim(retain_document, 2)", jobIDs(q.claim(t, worker1, retain, 2)), []port.OperationID{d1, d2})
	wantSeq(t, "Claim(consolidate)", jobIDs(q.claim(t, worker1, []port.JobKind{domain.OperationKindConsolidate}, 10)),
		nil)
	wantSeq(t, "Claim(retain_document, 10)", jobIDs(q.claim(t, worker1, retain, 10)), []port.OperationID{d3})
	wantSeq(t, "Claim(all)", jobIDs(q.claimAll(t)), []port.OperationID{gm})
}

// testClaimRunAt: a job due in the future is not claimed; one due in the past is.
func testClaimRunAt(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	later, now := doc("later"), doc("now")
	later.RunAt, now.RunAt = future(), past()
	laterID, nowID := q.add(t, later), q.add(t, now)
	zero := q.add(t, doc("zero-run-at"))
	wantSet(t, "Claim with one job parked", jobIDs(q.claimAll(t)), []port.OperationID{nowID, zero})
	q.wantAlive(t, "parked pending job", laterID, true)
}

// testRescheduleRelease: Reschedule releases a parked job and parks a due one.
func testRescheduleRelease(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	parked := doc("parked")
	parked.RunAt = future()
	parkedID, dueID := q.add(t, parked), q.add(t, doc("due"))
	if err := q.queueTx(t, func(qu port.Queue) error {
		if err := qu.Reschedule(t.Context(), parkedID, past()); err != nil {
			return err
		}
		return qu.Reschedule(t.Context(), dueID, future())
	}); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	wantSeq(t, "Claim after Reschedule", jobIDs(q.claimAll(t)), []port.OperationID{parkedID})
}

// testRescheduleProcessing: Reschedule returns a processing job to pending with its attempt unchanged;
// a terminal job is ErrFailedPrecondition.
func testRescheduleProcessing(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	if err := q.queueTx(t, func(qu port.Queue) error { return qu.Reschedule(t.Context(), id, past()) }); err != nil {
		t.Fatalf("Reschedule(processing): %v", err)
	}
	if got := q.claimOne(t, id); got.Attempt != 0 {
		t.Errorf("Attempt after Reschedule of a processing job = %d, want 0", got.Attempt)
	}
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	err := q.queueTx(t, func(qu port.Queue) error { return qu.Reschedule(t.Context(), id, past()) })
	wantErrIs(t, "Reschedule(completed)", err, domain.ErrFailedPrecondition)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim after Reschedule(completed) = %v, want nothing", jobIDs(js))
	}
}

// ---------- serial keys ----------

// testSerialKey: two jobs with one key run one at a time, older first.
func testSerialKey(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	first, second := q.add(t, doc("same")), q.add(t, doc("same"))
	q.claimOne(t, first)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim while the key is processing = %v, want nothing", jobIDs(js))
	}
	if err := q.c.Complete(t.Context(), first, json.RawMessage(`{"ok":true}`), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	q.claimOne(t, second)
}

// testSerialKeysIndependent: different keys, keys of different banks and empty keys never block each other.
func testSerialKeysIndependent(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	ids := []port.OperationID{q.add(t, doc("a")), q.add(t, doc("b"))}
	other := doc("a")
	other.Bank = bankB
	ids = append(ids, q.add(t, other))
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
	if err := q.queueTx(t, func(qu port.Queue) error { return qu.Reschedule(t.Context(), olderID, past()) }); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim of a due job whose key is processing = %v, want nothing", jobIDs(js))
	}
}

// ---------- dedupe ----------

// testDedupeNone: DedupeNone inserts every time.
func testDedupeNone(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	a, b := q.add(t, j), q.add(t, j)
	if a == b {
		t.Errorf("DedupeNone returned the same id twice: %s", a)
	}
}

// testDedupePending: DedupePending joins a pending row (left unchanged) but not a processing one, and
// matches on bank, kind and serial key.
func testDedupePending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{"v":1}`)
	j.Dedupe = port.DedupePending
	first := q.add(t, j)
	j2 := j
	j2.Payload = json.RawMessage(`{"v":2}`)
	if id, joined := q.enqueue(t, j2); !joined || id != first {
		t.Errorf("DedupePending on a pending row = (%s, %v), want (%s, true)", id, joined, first)
	}
	variants := map[string]port.Job{
		"other bank": withBank(j, bankB),
		"other kind": withKind(j, domain.OperationKindGraphMaintenance),
		"other key":  withKey(j, "consolidate-2"),
	}
	for name, v := range variants {
		if id, joined := q.enqueue(t, v); joined {
			t.Errorf("DedupePending with %s joined %s", name, id)
		}
	}
	got := q.claim(t, worker1, []port.JobKind{domain.OperationKindConsolidate}, 10)
	if i := slices.Index(jobIDs(got), first); i < 0 {
		t.Fatalf("Claim = %v, want %s among them", jobIDs(got), first)
	} else {
		wantJSON(t, "payload of the joined row", got[i].Payload, j.Payload)
	}
	if _, joined := q.enqueue(t, j2); joined {
		t.Errorf("DedupePending joined a processing row")
	}
}

// withBank returns j with another bank.
func withBank(j port.Job, b port.BankRef) port.Job { j.Bank = b; return j }

// withKind returns j with another kind.
func withKind(j port.Job, k port.JobKind) port.Job { j.Kind = k; return j }

// withKey returns j with another serial key.
func withKey(j port.Job, key string) port.Job { j.SerialKey = key; return j }

// testDedupePendingOrRunning: DedupePendingOrRunning joins a pending or processing row, not a finished one.
func testDedupePendingOrRunning(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`)
	j.Dedupe = port.DedupePendingOrRunning
	first := q.add(t, j)
	if id, joined := q.enqueue(t, j); !joined || id != first {
		t.Errorf("join pending = (%s, %v), want (%s, true)", id, joined, first)
	}
	q.claimOne(t, first)
	if id, joined := q.enqueue(t, j); !joined || id != first {
		t.Errorf("join processing = (%s, %v), want (%s, true)", id, joined, first)
	}
	if err := q.c.Complete(t.Context(), first, json.RawMessage(`{}`), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if id, joined := q.enqueue(t, j); joined || id == first {
		t.Errorf("after Complete = (%s, %v), want a new row", id, joined)
	}
}

// testDedupeOverrides: a job with overrides never joins and is never joined.
func testDedupeOverrides(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	plain := job(bankA, domain.OperationKindRefreshMentalModel, "mm:team", `{"model_id":"team"}`)
	plain.Dedupe = port.DedupePending
	withO := plain
	withO.Overrides = &domain.BankConfig{ReflectMission: "override"}
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

// ---------- upgrade ----------

// testUpgrade: Upgrade replaces payload and overrides of a pending job; a nil payload keeps it and nil
// overrides clear them.
func testUpgrade(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	j := job(bankA, domain.OperationKindRefreshMentalModel, "mm:a", `{"model_id":"a","force_full":false}`)
	j.Overrides = &domain.BankConfig{ReflectMission: "old"}
	a := q.add(t, j)
	k := withKey(j, "mm:b")
	k.Payload = json.RawMessage(`{"model_id":"b"}`)
	b := q.add(t, k)
	newPayload := json.RawMessage(`{"model_id":"a","force_full":true}`)
	newO := &domain.BankConfig{ReflectMission: "new", Recall: &domain.RecallConfig{Rerank: ptr(false)}}
	if err := q.queueTx(t, func(qu port.Queue) error {
		if err := qu.Upgrade(t.Context(), a, newPayload, newO); err != nil {
			return err
		}
		return qu.Upgrade(t.Context(), b, nil, nil)
	}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	got := map[port.OperationID]port.Job{}
	for _, c := range q.claimAll(t) {
		got[c.ID] = c
	}
	if len(got) != 2 {
		t.Fatalf("Claim after Upgrade = %d jobs, want 2", len(got))
	}
	wantJSON(t, "upgraded payload", got[a].Payload, newPayload)
	wantJSON(t, "upgraded overrides", got[a].Overrides, newO)
	wantJSON(t, "payload kept by nil Upgrade", got[b].Payload, k.Payload)
	if got[b].Overrides != nil {
		t.Errorf("overrides after Upgrade(nil) = %+v, want nil", got[b].Overrides)
	}
}

// testUpgradeNotPending: Upgrade of a processing job is ErrFailedPrecondition; of an unknown id ErrNotFound.
func testUpgradeNotPending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, job(bankA, domain.OperationKindRefreshMentalModel, "mm:a", `{}`))
	q.claimOne(t, id)
	err := q.queueTx(t, func(qu port.Queue) error { return qu.Upgrade(t.Context(), id, json.RawMessage(`{}`), nil) })
	wantErrIs(t, "Upgrade(processing)", err, domain.ErrFailedPrecondition)
	unknown := newID[port.OperationID]()
	err = q.queueTx(t, func(qu port.Queue) error { return qu.Upgrade(t.Context(), unknown, nil, nil) })
	wantErrIs(t, "Upgrade(unknown)", err, domain.ErrNotFound)
}

// ---------- cancel and deleting banks ----------

// testCancelBank: CancelBank cancels the bank's pending jobs only.
func testCancelBank(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	running := q.add(t, doc("running"))
	q.claimOne(t, running)
	p1, p2 := q.add(t, doc("p1")), q.add(t, doc("p2"))
	parked := doc("parked")
	parked.RunAt = future()
	p3 := q.add(t, parked)
	other := q.add(t, withBank(doc("other"), bankB))
	var n int
	if err := q.queueTx(t, func(qu port.Queue) error {
		var err error
		n, err = qu.CancelBank(t.Context(), bankA)
		return err
	}); err != nil {
		t.Fatalf("CancelBank: %v", err)
	}
	if n != 3 {
		t.Errorf("CancelBank = %d, want 3 (the pending jobs, parked included)", n)
	}
	wantSeq(t, "Claim after CancelBank", jobIDs(q.claimAll(t)), []port.OperationID{other})
	for _, id := range []port.OperationID{p1, p2, p3} {
		q.wantAlive(t, "after CancelBank", id, false)
	}
	q.wantAlive(t, "processing job of a cancelled bank", running, true)
	q.wantAlive(t, "other bank's job", other, true)
}

// testDeletingBank: a tombstoned bank's jobs are skipped by Claim except delete_bank; Alive is false for its
// other jobs and true for delete_bank.
func testDeletingBank(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	running := q.add(t, job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`))
	q.claimOne(t, running)
	pending := q.add(t, doc("pending"))
	other := q.add(t, withBank(doc("other"), bankB))
	var del port.OperationID
	inTx(t, q.s, func(tx port.Tx) error {
		j := job(bankA, domain.OperationKindDeleteBank, "bank", `{}`)
		j.Dedupe = port.DedupePendingOrRunning
		var err error
		if del, _, err = tx.Queue().Enqueue(t.Context(), j); err != nil {
			return err
		}
		return tx.Banks().MarkDeleting(t.Context(), bankA, day(1))
	})
	wantSet(t, "Claim with bank A tombstoned", jobIDs(q.claimAll(t)), []port.OperationID{del, other})
	q.wantAlive(t, "processing job of a tombstoned bank", running, false)
	q.wantAlive(t, "pending job of a tombstoned bank", pending, false)
	q.wantAlive(t, "delete_bank job of a tombstoned bank", del, true)
	q.wantAlive(t, "other bank's job", other, true)
}

// ---------- heartbeats and reclaim ----------

// testHeartbeat: a job heartbeated after going stale is not reclaimed.
func testHeartbeat(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	time.Sleep(staleSleep)
	if err := q.c.Heartbeat(t.Context(), worker1, []port.OperationID{id}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale after a fresh heartbeat touched %d rows, want 0", n)
	}
	q.wantAlive(t, "after heartbeat", id, true)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim = %v, want nothing (the job is still processing)", jobIDs(js))
	}
}

// testHeartbeatOtherWorker: a heartbeat from a worker that does not hold the job does not refresh it.
func testHeartbeatOtherWorker(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	time.Sleep(staleSleep)
	if err := q.c.Heartbeat(t.Context(), worker2, []port.OperationID{id}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if n := q.reclaim(t); n != 1 {
		t.Errorf("ReclaimStale after another worker's heartbeat touched %d rows, want 1", n)
	}
}

// testReclaimStale: a stale processing job returns to pending with Attempt+1.
func testReclaimStale(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale of a fresh job touched %d rows, want 0", n)
	}
	time.Sleep(staleSleep)
	if n := q.reclaim(t); n != 1 {
		t.Errorf("ReclaimStale touched %d rows, want 1", n)
	}
	if got := q.claimOne(t, id); got.Attempt != 1 {
		t.Errorf("Attempt after one reclaim = %d, want 1", got.Attempt)
	}
}

// testReclaimMaxAttempts: a retain_document job fails for good after MaxAttempts reclaims; a consolidate
// job (unbounded) keeps coming back.
func testReclaimMaxAttempts(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	retain := q.add(t, doc("1"))
	cons := q.add(t, job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`))
	maxAttempts := domain.OperationKindRetainDocument.MaxAttempts()
	for round := range maxAttempts {
		got := map[port.OperationID]int{}
		for _, j := range q.claimAll(t) {
			got[j.ID] = j.Attempt
		}
		for _, id := range []port.OperationID{retain, cons} {
			if a, ok := got[id]; !ok || a != round {
				t.Fatalf("round %d: Claim = %v, want %s at attempt %d", round, got, id, round)
			}
		}
		time.Sleep(staleSleep)
		if n := q.reclaim(t); n != 2 {
			t.Errorf("round %d: ReclaimStale touched %d rows, want 2", round, n)
		}
	}
	if got := q.claimOne(t, cons); got.Attempt != maxAttempts {
		t.Errorf("consolidate Attempt = %d, want %d", got.Attempt, maxAttempts)
	}
	q.wantAlive(t, "retain_document past max attempts", retain, false)
}

// ---------- terminal transitions ----------

// testFailRetry: Fail with a retry time returns the job to pending at that time with Attempt+1.
func testFailRetry(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	soon, later := q.add(t, doc("soon")), q.add(t, doc("later"))
	wantSet(t, "first Claim", jobIDs(q.claimAll(t)), []port.OperationID{soon, later})
	cause := errors.New("gateway timeout")
	if err := q.c.Fail(t.Context(), soon, cause, ptr(past())); err != nil {
		t.Fatalf("Fail(soon): %v", err)
	}
	if err := q.c.Fail(t.Context(), later, cause, ptr(future())); err != nil {
		t.Fatalf("Fail(later): %v", err)
	}
	if got := q.claimOne(t, soon); got.Attempt != 1 {
		t.Errorf("Attempt after Fail with retry = %d, want 1", got.Attempt)
	}
	q.wantAlive(t, "pending retry", later, true)
}

// testFailTerminal: Fail without a retry time is terminal.
func testFailTerminal(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	if err := q.c.Fail(t.Context(), id, errors.New("schema violation"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	q.wantAlive(t, "after terminal Fail", id, false)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim after terminal Fail = %v, want nothing", jobIDs(js))
	}
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale touched %d rows after terminal Fail, want 0", n)
	}
}

// testComplete: Complete is terminal and frees the serial key.
func testComplete(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	usage := []port.Usage{{Slot: domain.ModelSlotExtract, Model: "m", InputTokens: 10, OutputTokens: 5}}
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{"units":3}`), usage); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	q.wantAlive(t, "after Complete", id, false)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim after Complete = %v, want nothing", jobIDs(js))
	}
	next := q.add(t, doc("1"))
	q.claimOne(t, next)
}

// testCancelledStays: Complete, Fail and Reschedule never resurrect a cancelled job.
func testCancelledStays(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	if err := q.queueTx(t, func(qu port.Queue) error {
		_, err := qu.CancelBank(t.Context(), bankA)
		return err
	}); err != nil {
		t.Fatalf("CancelBank: %v", err)
	}
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil); err != nil {
		t.Errorf("Complete(cancelled) = %v, want a silent no-op", err)
	}
	if err := q.c.Fail(t.Context(), id, errors.New("late failure"), ptr(past())); err != nil {
		t.Errorf("Fail(cancelled) = %v, want a silent no-op", err)
	}
	err := q.queueTx(t, func(qu port.Queue) error { return qu.Reschedule(t.Context(), id, past()) })
	wantErrIs(t, "Reschedule(cancelled)", err, domain.ErrFailedPrecondition)
	q.wantAlive(t, "cancelled job", id, false)
	if js := q.claimAll(t); len(js) != 0 {
		t.Errorf("Claim after Complete/Fail/Reschedule of a cancelled job = %v, want nothing", jobIDs(js))
	}
}

// testProgress: Progress succeeds on a processing job and leaves it running.
func testProgress(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	p := domain.Progress{Stage: "extract", At: nowMicro(), Processed: 1, Total: 3}
	if err := q.c.Progress(t.Context(), id, p); err != nil {
		t.Fatalf("Progress: %v", err)
	}
	q.wantAlive(t, "after Progress", id, true)
}

// testAliveUnknown: Alive of a row that does not exist is false.
func testAliveUnknown(t *testing.T, q queueEnv) {
	q.wantAlive(t, "unknown id", newID[port.OperationID](), false)
}

// ---------- concurrency ----------

// claimResult is one concurrent claimer's outcome.
type claimResult struct {
	jobs []port.Job
	err  error
}

// claimRetrying claims like Claim, retrying while the adapter reports a lost serial-key race (ErrConflict).
func claimRetrying(t *testing.T, q queueEnv, worker string) claimResult {
	for range 10 {
		js, err := q.c.Claim(t.Context(), worker, workKinds, 100)
		if !errors.Is(err, domain.ErrConflict) {
			return claimResult{js, err}
		}
	}
	return claimResult{nil, errors.New("claim kept returning ErrConflict")}
}

// testConcurrentClaims: concurrent claimers never return a job twice or two jobs with one serial key; after
// a final sweep exactly the oldest job of every key and every keyless job is processing.
func testConcurrentClaims(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	const keys, perKey, keyless, claimers = 5, 3, 4, 4
	oldest := map[string]port.OperationID{}
	var want []port.OperationID
	for r := range perKey {
		for k := range keys {
			key := fmt.Sprintf("doc:k%d", k)
			id := q.add(t, job(bankA, domain.OperationKindRetainDocument, key, `{}`))
			if r == 0 {
				oldest[key] = id
				want = append(want, id)
			}
		}
	}
	for range keyless {
		want = append(want, q.add(t, job(bankA, domain.OperationKindRetainDocument, "", `{}`)))
	}
	results := make(chan claimResult, claimers)
	for i := range claimers {
		go func() { results <- claimRetrying(t, q, fmt.Sprintf("worker-%d", i)) }()
	}
	var claimed []port.Job
	for range claimers {
		r := waitFor(t, "a concurrent Claim", results)
		if r.err != nil {
			t.Fatalf("concurrent Claim: %v", r.err)
		}
		claimed = append(claimed, r.jobs...)
	}
	claimed = append(claimed, q.claimAll(t)...)
	assertClaimInvariants(t, claimed, oldest)
	wantSet(t, "jobs processing after concurrent claims and a sweep", jobIDs(claimed), want)
}

// assertClaimInvariants fails the test if a job was claimed twice, two claimed jobs share a serial key, or a
// keyed job other than the oldest of its key was claimed.
func assertClaimInvariants(t *testing.T, claimed []port.Job, oldest map[string]port.OperationID) {
	t.Helper()
	seenID := map[port.OperationID]bool{}
	seenKey := map[string]port.OperationID{}
	for _, j := range claimed {
		if seenID[j.ID] {
			t.Errorf("job %s claimed twice", j.ID)
		}
		seenID[j.ID] = true
		if j.SerialKey == "" {
			continue
		}
		if prev, ok := seenKey[j.SerialKey]; ok {
			t.Errorf("serial key %q claimed twice: %s and %s", j.SerialKey, prev, j.ID)
		}
		seenKey[j.SerialKey] = j.ID
		if j.ID != oldest[j.SerialKey] {
			t.Errorf("key %q: claimed %s, want the oldest job %s", j.SerialKey, j.ID, oldest[j.SerialKey])
		}
	}
}
