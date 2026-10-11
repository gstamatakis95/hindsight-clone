package postgres_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
	"time"
)

// workerCases are RunQueueSuite's Claimer subtests: heartbeats, reclaim, fencing, terminal transitions and
// concurrent claims.
func workerCases() []queueCase {
	return []queueCase{
		{"HeartbeatPreventsReclaim", testHeartbeat},
		{"HeartbeatReportsLostJobs", testHeartbeatLostJobs},
		{"ReclaimStale", testReclaimStale},
		{"ReclaimMaxAttempts", testReclaimMaxAttempts},
		{"ReclaimPastMaxFails", testReclaimPastMaxFails},
		{"ReclaimSkipsTerminalAndPending", testReclaimSkips},
		{"StaleWorkerFenced", testStaleWorkerFenced},
		{"TerminalRowsFenced", testTerminalFenced},
		{"FailWithRetry", testFailRetry},
		{"FailTerminalAppendsEvent", testFailTerminal},
		{"Complete", testComplete},
		{"Progress", testProgress},
		{"ConcurrentClaims", testConcurrentClaims},
	}
}

// wantFailedEvent fails the test unless bank A's events after seq before hold exactly one OPERATION_FAILED
// event, for id, at a seq above before.
func wantFailedEvent(t *testing.T, s store.DB, before int64, id domain.OperationID) {
	t.Helper()
	var failed []domain.OperationID
	for _, e := range listEvents(t, s, bankA, before, 100) {
		if e.Type != domain.EventTypeOperationFailed {
			continue
		}
		failed = append(failed, e.OperationID)
		if e.Seq <= before || e.Bank != bankA {
			t.Errorf("OPERATION_FAILED event seq %d bank %v, want a seq above %d in %v", e.Seq, e.Bank, before, bankA)
		}
	}
	wantSeq(t, "OPERATION_FAILED events", failed, []domain.OperationID{id})
}

// attemptsByID maps claimed jobs to their Attempt.
func attemptsByID(js []store.Job) map[domain.OperationID]int {
	out := map[domain.OperationID]int{}
	for _, j := range js {
		out[j.ID] = j.Attempt
	}
	return out
}

// ---------- heartbeats and reclaim ----------

// testHeartbeat: a job heartbeated after going stale is not reclaimed.
func testHeartbeat(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	time.Sleep(staleSleep)
	if err := q.c.Heartbeat(t.Context(), worker1, []domain.OperationID{id}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale after a fresh heartbeat touched %d rows, want 0", n)
	}
	q.wantAlive(t, "after heartbeat", id, true)
	q.claimNone(t, "(the job is still processing)")
}

// testHeartbeatLostJobs: a heartbeat naming jobs the worker no longer holds (finished, or held by another
// worker) refreshes the held one and returns a *LostJobsError (ErrConflict) naming exactly the lost ones.
func testHeartbeatLostJobs(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	held, other, done := q.add(t, doc("held")), q.add(t, doc("other")), q.add(t, doc("done"))
	wantSeq(t, "Claim as worker 1", jobIDs(q.claim(t, worker1, workKinds, 1)), []domain.OperationID{held})
	wantSeq(t, "Claim as worker 2", jobIDs(q.claim(t, worker2, workKinds, 1)), []domain.OperationID{other})
	wantSeq(t, "second Claim as worker 1", jobIDs(q.claim(t, worker1, workKinds, 1)), []domain.OperationID{done})
	q.complete(t, done)
	time.Sleep(staleSleep)
	err := q.c.Heartbeat(t.Context(), worker1, []domain.OperationID{held, other, done})
	wantErrIs(t, "Heartbeat with lost jobs", err, domain.ErrConflict)
	wantSet(t, "LostJobs", store.LostJobs(err), []domain.OperationID{other, done})
	if n := q.reclaim(t); n != 1 {
		t.Errorf("ReclaimStale after a partial heartbeat touched %d rows, want 1 (worker 2's job only)", n)
	}
	q.wantStatus(t, "of the heartbeated job", held, domain.OperationStatusProcessing)
	q.wantAlive(t, "heartbeated job", held, true)
	if op := q.get(t, bankA, other); op.Status != domain.OperationStatusPending || op.Attempts != 1 {
		t.Errorf("worker 2's stale job = %q attempts %d, want pending, 1", op.Status, op.Attempts)
	}
}

// testReclaimStale: a stale processing job returns to pending with Attempt+1 and is no longer Alive.
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
	if op := q.get(t, bankA, id); op.Status != domain.OperationStatusPending || op.Attempts != 1 {
		t.Errorf("after reclaim: %q attempts %d, want pending, 1", op.Status, op.Attempts)
	}
	q.wantAlive(t, "reclaimed (pending) job", id, false)
	if got := q.claimOne(t, id); got.Attempt != 1 {
		t.Errorf("Attempt after one reclaim = %d, want 1", got.Attempt)
	}
}

// testReclaimMaxAttempts: a retain_document job fails for good after MaxAttempts reclaims (with an
// OPERATION_FAILED event); a consolidate job (unbounded) keeps coming back.
func testReclaimMaxAttempts(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	retain := q.add(t, doc("1"))
	cons := q.add(t, job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`))
	before := bankSeq(t, q.s, bankA)
	maxAttempts := domain.OperationKindRetainDocument.MaxAttempts()
	if maxAttempts == 0 {
		t.Fatalf("test assumes retain_document has a bounded MaxAttempts")
	}
	for round := range maxAttempts {
		got := attemptsByID(q.claimAll(t))
		for _, id := range []domain.OperationID{retain, cons} {
			if a, ok := got[id]; !ok || a != round {
				t.Fatalf("round %d: Claim = %v, want %s at attempt %d", round, got, id, round)
			}
		}
		time.Sleep(staleSleep)
		if n := q.reclaim(t); n != 2 {
			t.Errorf("round %d: ReclaimStale touched %d rows, want 2", round, n)
		}
	}
	want := map[domain.OperationID]int{cons: maxAttempts}
	if got := attemptsByID(q.claimAll(t)); !maps.Equal(got, want) {
		t.Errorf("Claim after %d reclaims = %v, want %v (consolidate only)", maxAttempts, got, want)
	}
	q.wantStatus(t, "retain_document past max attempts", retain, domain.OperationStatusFailed)
	q.wantAlive(t, "retain_document past max attempts", retain, false)
	wantFailedEvent(t, q.s, before, retain)
}

// testReclaimPastMaxFails: a refresh_mental_model job whose worker keeps crashing becomes FAILED at max
// attempts with exactly one OPERATION_FAILED event (no finalizer hand-back); a later reclaim leaves it alone
// and a later refresh of the model is a new row, not a join.
func testReclaimPastMaxFails(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	ref := q.add(t, refresh("a", `{"model_id":"a"}`))
	before := bankSeq(t, q.s, bankA)
	for round := range domain.OperationKindRefreshMentalModel.MaxAttempts() {
		if got := q.claimOne(t, ref); got.Attempt != round {
			t.Errorf("round %d: Attempt = %d", round, got.Attempt)
		}
		time.Sleep(staleSleep)
		if n := q.reclaim(t); n != 1 {
			t.Errorf("round %d: ReclaimStale touched %d rows, want 1", round, n)
		}
	}
	q.wantStatus(t, "refresh past max attempts", ref, domain.OperationStatusFailed)
	q.claimNone(t, "after the refresh failed for good")
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale after the refresh failed touched %d rows, want 0", n)
	}
	wantFailedEvent(t, q.s, before, ref)
	if id, joined := q.enqueue(t, refresh("a", `{"model_id":"a"}`)); joined || id == ref {
		t.Errorf("refresh after the failed one = (%s, %v), want a new row", id, joined)
	}
}

// testReclaimSkips: ReclaimStale never touches completed, failed or pending rows, however old.
func testReclaimSkips(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	done, failed, stale := q.add(t, doc("done")), q.add(t, doc("failed")), q.add(t, doc("stale"))
	wantSet(t, "Claim", jobIDs(q.claimAll(t)), []domain.OperationID{done, failed, stale})
	q.complete(t, done)
	if err := q.c.Fail(t.Context(), failed, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	pending := q.add(t, doc("pending"))
	time.Sleep(staleSleep)
	if n := q.reclaim(t); n != 1 {
		t.Errorf("ReclaimStale touched %d rows, want 1 (the stale processing job)", n)
	}
	q.wantStatus(t, "completed before going stale", done, domain.OperationStatusCompleted)
	q.wantStatus(t, "failed before going stale", failed, domain.OperationStatusFailed)
	for id, attempts := range map[domain.OperationID]int{stale: 1, pending: 0} {
		if op := q.get(t, bankA, id); op.Status != domain.OperationStatusPending || op.Attempts != attempts {
			t.Errorf("job %s = %q attempts %d, want pending, %d", id, op.Status, op.Attempts, attempts)
		}
	}
}

// ---------- fencing ----------

// testStaleWorkerFenced: once a job is reclaimed, its stale worker's Fail, Complete and Progress return
// ErrFailedPrecondition and change nothing; the job is claimable exactly once more, at Attempt 1.
func testStaleWorkerFenced(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	time.Sleep(staleSleep)
	if n := q.reclaim(t); n != 1 {
		t.Fatalf("ReclaimStale touched %d rows, want 1", n)
	}
	cause := errors.New("stale worker failure")
	wantErrIs(t, "Fail by the stale worker", q.c.Fail(t.Context(), id, cause, ptr(past())), domain.ErrFailedPrecondition)
	wantErrIs(t, "Complete by the stale worker", q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil),
		domain.ErrFailedPrecondition)
	wantErrIs(t, "Progress by the stale worker", q.c.Progress(t.Context(), id, domain.Progress{Stage: "late", At: day(1)}),
		domain.ErrFailedPrecondition)
	op := q.get(t, bankA, id)
	if op.Status != domain.OperationStatusPending || op.Attempts != 1 || op.Progress != nil ||
		strings.Contains(op.Error, cause.Error()) {
		t.Errorf("after fenced calls: %q attempts %d progress %+v error %q; want pending, 1, none, not %q",
			op.Status, op.Attempts, op.Progress, op.Error, cause)
	}
	if got := q.claimOne(t, id); got.Attempt != 1 {
		t.Errorf("Attempt of the re-claimed job = %d, want 1", got.Attempt)
	}
	q.claimNone(t, "after the single re-claim")
}

// testTerminalFenced: Complete, Fail and Progress on a completed job return ErrFailedPrecondition and keep
// its status and result.
func testTerminalFenced(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{"units":1}`), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	cause := errors.New("late failure")
	wantErrIs(t, "second Complete", q.c.Complete(t.Context(), id, json.RawMessage(`{"units":2}`), nil),
		domain.ErrFailedPrecondition)
	wantErrIs(t, "Fail(completed)", q.c.Fail(t.Context(), id, cause, nil), domain.ErrFailedPrecondition)
	wantErrIs(t, "Fail(completed, retry)", q.c.Fail(t.Context(), id, cause, ptr(past())), domain.ErrFailedPrecondition)
	wantErrIs(t, "Progress(completed)", q.c.Progress(t.Context(), id, domain.Progress{Stage: "late", At: day(1)}),
		domain.ErrFailedPrecondition)
	op := q.get(t, bankA, id)
	if op.Status != domain.OperationStatusCompleted || strings.Contains(op.Error, cause.Error()) {
		t.Errorf("after fenced calls: %q error %q, want completed with no %q", op.Status, op.Error, cause)
	}
	wantJSON(t, "result after a fenced Complete", op.Result, json.RawMessage(`{"units":1}`))
	q.claimNone(t, "after fenced calls on a completed job")
}

// ---------- terminal transitions ----------

// testFailRetry: Fail with a retry time returns the job to pending at that time with Attempt+1 and records
// the cause.
func testFailRetry(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	soon, later := q.add(t, doc("soon")), q.add(t, doc("later"))
	wantSet(t, "first Claim", jobIDs(q.claimAll(t)), []domain.OperationID{soon, later})
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
	op := q.get(t, bankA, later)
	if op.Status != domain.OperationStatusPending || op.Attempts != 1 || !strings.Contains(op.Error, cause.Error()) {
		t.Errorf("parked retry = %q attempts %d error %q, want pending, 1, containing %q", op.Status, op.Attempts,
			op.Error, cause)
	}
	q.wantAlive(t, "parked retry", later, false)
}

// testFailTerminal: Fail without a retry time is terminal, records the cause and appends an OPERATION_FAILED
// event under a fresh bank seq.
func testFailTerminal(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	before := bankSeq(t, q.s, bankA)
	cause := errors.New("schema violation")
	if err := q.c.Fail(t.Context(), id, cause, nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if op := q.get(t, bankA, id); op.Status != domain.OperationStatusFailed || !strings.Contains(op.Error, cause.Error()) {
		t.Errorf("after terminal Fail: %q error %q, want failed containing %q", op.Status, op.Error, cause)
	}
	q.wantAlive(t, "after terminal Fail", id, false)
	q.claimNone(t, "after terminal Fail")
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale touched %d rows after terminal Fail, want 0", n)
	}
	wantFailedEvent(t, q.s, before, id)
	if got := bankSeq(t, q.s, bankA); got <= before {
		t.Errorf("BankSeq after terminal Fail = %d, want above %d", got, before)
	}
}

// testComplete: Complete is terminal, stores result and usage, and frees the serial key.
func testComplete(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	usage := []domain.Usage{
		{Slot: domain.ModelSlotExtract, Model: "m", InputTokens: 10, OutputTokens: 5},
		{Slot: domain.ModelSlotReflect, Model: "r", InputTokens: 7, OutputTokens: 1},
	}
	if err := q.c.Complete(t.Context(), id, json.RawMessage(`{"units":3}`), usage); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	op := q.get(t, bankA, id)
	if op.Status != domain.OperationStatusCompleted || !slices.Equal(op.Usage, usage) {
		t.Errorf("after Complete: %q usage %+v, want completed, %+v", op.Status, op.Usage, usage)
	}
	wantJSON(t, "result after Complete", op.Result, json.RawMessage(`{"units":3}`))
	q.wantAlive(t, "after Complete", id, false)
	q.claimNone(t, "after Complete")
	next := q.add(t, doc("1"))
	q.claimOne(t, next)
}

// testProgress: Progress records the latest checkpoint on a processing job, which Get reports, and leaves it
// running.
func testProgress(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	q.claimOne(t, id)
	for _, p := range []domain.Progress{
		{Stage: "extract", At: day(3), Processed: 1, Total: 3},
		{Stage: "embed", At: day(4), Processed: 2, Total: 3},
	} {
		if err := q.c.Progress(t.Context(), id, p); err != nil {
			t.Fatalf("Progress: %v", err)
		}
		got := q.get(t, bankA, id).Progress
		if got == nil || got.Stage != p.Stage || got.Processed != p.Processed || got.Total != p.Total || !got.At.Equal(p.At) {
			t.Errorf("Get().Progress = %+v, want %+v", got, p)
		}
	}
	q.wantAlive(t, "after Progress", id, true)
}

// ---------- concurrency ----------

// claimResult is one concurrent claimer's outcome.
type claimResult struct {
	jobs []store.Job
	err  error
}

// testConcurrentClaims: concurrent claimers never fail, never return a job twice or two jobs with one serial
// key; after a final sweep exactly the oldest job of every key and every keyless job is processing.
func testConcurrentClaims(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	const keys, perKey, keyless, claimers = 5, 3, 4, 4
	oldest := map[string]domain.OperationID{}
	var want []domain.OperationID
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
	start := make(chan struct{})
	results := make(chan claimResult, claimers)
	for i := range claimers {
		go func() {
			<-start
			js, err := q.c.Claim(t.Context(), fmt.Sprintf("worker-%d", i), workKinds, 100)
			results <- claimResult{js, err}
		}()
	}
	close(start)
	var claimed []store.Job
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
func assertClaimInvariants(t *testing.T, claimed []store.Job, oldest map[string]domain.OperationID) {
	t.Helper()
	seenID := map[domain.OperationID]bool{}
	seenKey := map[string]domain.OperationID{}
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
