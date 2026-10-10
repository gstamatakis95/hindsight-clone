package porttest

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"
)

// controlCases are RunQueueSuite's operation-control subtests: Get, List, Cancel, Retry, Prune, CancelBank,
// deleting and deleted banks, unknown ids and tenant isolation.
func controlCases() []queueCase {
	return []queueCase{
		{"UnknownID", testUnknownID},
		{"GetReflectsJob", testGetFields},
		{"ListFiltersAndPages", testList},
		{"CancelPending", testCancelPending},
		{"CancelProcessingReportedLost", testCancelProcessing},
		{"CancelProcessingHoldsKey", testCancelHoldsKey},
		{"CancelStaleFinishedByReclaim", testCancelReclaimed},
		{"CancelledPendingFenced", testCancelledStays},
		{"RetryFailedAndCancelled", testRetry},
		{"Prune", testPrune},
		{"CancelBank", testCancelBank},
		{"DeletingBankClaims", testDeletingBank},
		{"EnqueueTombstonedOrUnknownBank", testEnqueueTombstoned},
		{"BankDeleteRemovesOperationsAndEvents", testBankDeleteOps},
		{"TenantIsolation", testQueueTenants},
	}
}

// cancel runs Queue.Cancel on bank A's id in its own transaction and returns its result unchanged.
func (q queueEnv) cancel(t *testing.T, id port.OperationID) (domain.Operation, error) {
	t.Helper()
	var op domain.Operation
	err := q.queueTx(t, func(qu port.Queue) error {
		var err error
		op, err = qu.Cancel(t.Context(), bankA, id)
		return err
	})
	return op, err
}

// mustCancel cancels bank A's id and fails the test unless the returned operation has status want
// (CANCELLED for a pending row; PROCESSING for a processing row, whose cancel is only requested).
func (q queueEnv) mustCancel(t *testing.T, id port.OperationID, want domain.OperationStatus) {
	t.Helper()
	op, err := q.cancel(t, id)
	if err != nil {
		t.Fatalf("Cancel(%s): %v", id, err)
	}
	if op.ID != id || op.Status != want {
		t.Errorf("Cancel(%s) returned %s %q, want the operation, %q", id, op.ID, op.Status, want)
	}
}

// retry runs Queue.Retry on bank A's id in its own transaction and returns its result unchanged.
func (q queueEnv) retry(t *testing.T, id port.OperationID) (domain.Operation, error) {
	t.Helper()
	var op domain.Operation
	err := q.queueTx(t, func(qu port.Queue) error {
		var err error
		op, err = qu.Retry(t.Context(), bankA, id)
		return err
	})
	return op, err
}

// cancelBank runs CancelBank(b) in its own transaction and returns how many it cancelled.
func (q queueEnv) cancelBank(t *testing.T, b port.BankRef) int {
	t.Helper()
	var n int
	if err := q.queueTx(t, func(qu port.Queue) error {
		var err error
		n, err = qu.CancelBank(t.Context(), b)
		return err
	}); err != nil {
		t.Fatalf("CancelBank(%v): %v", b, err)
	}
	return n
}

// ---------- Get and List ----------

// testUnknownID: Get, Complete, Fail and Progress of an unknown id are ErrNotFound; Alive is false.
func testUnknownID(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := newID[port.OperationID]()
	q.wantAlive(t, "unknown id", id, false)
	_, err := getOp(t, q.s, bankA, id)
	wantErrIs(t, "Get(unknown)", err, domain.ErrNotFound)
	wantErrIs(t, "Complete(unknown)", q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil), domain.ErrNotFound)
	wantErrIs(t, "Fail(unknown)", q.c.Fail(t.Context(), id, errors.New("x"), nil), domain.ErrNotFound)
	wantErrIs(t, "Progress(unknown)", q.c.Progress(t.Context(), id, domain.Progress{Stage: "x", At: day(1)}),
		domain.ErrNotFound)
}

// testGetFields: Get reports a job's bank, kind, key, parent, status and attempts, and once claimed its
// worker and heartbeat.
func testGetFields(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	parent := q.add(t, job(bankA, domain.OperationKindRetainBatch, "", `{}`))
	j := doc("1")
	j.ParentID = parent
	id := q.add(t, j)
	op := q.get(t, bankA, id)
	if op.ID != id || op.Bank != bankA || op.Kind != j.Kind || op.SerialKey != j.SerialKey || op.ParentID != parent {
		t.Errorf("Get = %s %v %q %q parent %q, want %s %v %q %q parent %q", op.ID, op.Bank, op.Kind, op.SerialKey,
			op.ParentID, id, bankA, j.Kind, j.SerialKey, parent)
	}
	if op.Status != domain.OperationStatusPending || op.Attempts != 0 || op.WorkerID != "" {
		t.Errorf("Get(pending) = %q attempts %d worker %q, want pending, 0, none", op.Status, op.Attempts, op.WorkerID)
	}
	q.claimOne(t, id)
	op = q.get(t, bankA, id)
	if op.Status != domain.OperationStatusProcessing || op.WorkerID != worker1 || op.HeartbeatAt == nil {
		t.Errorf("Get(processing) = %q worker %q heartbeat %v, want processing, %s, set", op.Status, op.WorkerID,
			op.HeartbeatAt, worker1)
	}
}

// listAllPages pages through bank A's operations with page size 1 and returns every id, failing the test if
// a page holds more than one or paging does not end.
func listAllPages(t *testing.T, q queueEnv) []port.OperationID {
	t.Helper()
	var (
		ids   []port.OperationID
		token string
	)
	for range 20 {
		var page []domain.Operation
		inTx(t, q.s, func(tx port.Tx) error {
			var err error
			page, token, err = tx.Queue().List(t.Context(), bankA, port.OperationFilter{PageSize: 1, PageToken: token})
			return err
		})
		if len(page) > 1 {
			t.Errorf("List(page size 1) returned %d operations", len(page))
		}
		ids = append(ids, opIDs(page)...)
		if token == "" {
			return ids
		}
	}
	t.Fatalf("List paging did not end after 20 pages")
	return ids
}

// testList: List is newest first and bank-scoped, filters by status and kind, drops parents on request and
// pages with tokens.
func testList(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	parent := q.add(t, job(bankA, domain.OperationKindRetainBatch, "", `{}`))
	d1, d2 := q.add(t, doc("1")), q.add(t, doc("2"))
	g := q.add(t, job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`))
	other := q.add(t, withBank(doc("x"), bankB))
	wantSeq(t, "Claim(retain_document, 1)",
		jobIDs(q.claim(t, worker1, []port.JobKind{domain.OperationKindRetainDocument}, 1)), []port.OperationID{d1})
	q.complete(t, d1)
	all := []port.OperationID{g, d2, d1, parent}
	wantSeq(t, "List(all)", opIDs(listOps(t, q.s, bankA, port.OperationFilter{})), all)
	completed := port.OperationFilter{Status: domain.OperationStatusCompleted}
	wantSeq(t, "List(completed)", opIDs(listOps(t, q.s, bankA, completed)), []port.OperationID{d1})
	graph := port.OperationFilter{Kind: domain.OperationKindGraphMaintenance}
	wantSeq(t, "List(graph_maintenance)", opIDs(listOps(t, q.s, bankA, graph)), []port.OperationID{g})
	wantSeq(t, "List(exclude parents)", opIDs(listOps(t, q.s, bankA, port.OperationFilter{ExcludeParents: true})),
		[]port.OperationID{g, d2, d1})
	wantSeq(t, "List paged by 1", listAllPages(t, q), all)
	wantSeq(t, "List(bank B)", opIDs(listOps(t, q.s, bankB, port.OperationFilter{})), []port.OperationID{other})
}

// ---------- Cancel and Retry ----------

// testCancelPending: cancelling a pending job finishes it at once (a younger same-key job is claimable);
// cancelling a cancelled or completed job is ErrFailedPrecondition.
func testCancelPending(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	first, second := q.add(t, doc("k")), q.add(t, doc("k"))
	q.mustCancel(t, first, domain.OperationStatusCancelled)
	q.wantStatus(t, "after Cancel(pending)", first, domain.OperationStatusCancelled)
	q.claimOne(t, second)
	_, err := q.cancel(t, first)
	wantErrIs(t, "Cancel(cancelled)", err, domain.ErrFailedPrecondition)
	q.complete(t, second)
	_, err = q.cancel(t, second)
	wantErrIs(t, "Cancel(completed)", err, domain.ErrFailedPrecondition)
	q.wantStatus(t, "after Cancel(completed)", second, domain.OperationStatusCompleted)
}

// testCancelProcessing: a job whose cancel was requested while processing stays PROCESSING but is not
// Alive; heartbeats report it lost yet keep it fresh, so ReclaimStale leaves it alone; its handler's Complete
// or Fail moves it to CANCELLED and returns ErrCancelled, releasing its serial key.
func testCancelProcessing(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	a1, b1 := q.add(t, doc("k1")), q.add(t, doc("k1"))
	a2, b2 := q.add(t, doc("k2")), q.add(t, doc("k2"))
	wantSet(t, "first Claim", jobIDs(q.claimAll(t)), []port.OperationID{a1, a2})
	time.Sleep(staleSleep)
	for _, id := range []port.OperationID{a1, a2} {
		q.mustCancel(t, id, domain.OperationStatusProcessing)
		q.wantAlive(t, "after Cancel(processing)", id, false)
	}
	err := q.c.Heartbeat(t.Context(), worker1, []port.OperationID{a1, a2})
	wantErrIs(t, "Heartbeat of cancel-requested jobs", err, domain.ErrConflict)
	wantSet(t, "LostJobs of cancel-requested jobs", port.LostJobs(err), []port.OperationID{a1, a2})
	if n := q.reclaim(t); n != 0 {
		t.Errorf("ReclaimStale after a heartbeat of cancel-requested jobs touched %d rows, want 0", n)
	}
	q.claimNone(t, "while the cancel-requested jobs' handlers still run")
	wantErrIs(t, "Complete(cancel requested)", q.c.Complete(t.Context(), a1, json.RawMessage(`{}`), nil),
		domain.ErrCancelled)
	wantErrIs(t, "Fail(cancel requested)", q.c.Fail(t.Context(), a2, errors.New("stopped"), nil),
		domain.ErrCancelled)
	q.wantStatus(t, "after Complete", a1, domain.OperationStatusCancelled)
	q.wantStatus(t, "after Fail", a2, domain.OperationStatusCancelled)
	wantSet(t, "Claim after the handlers finished", jobIDs(q.claimAll(t)), []port.OperationID{b1, b2})
}

// testCancelHoldsKey: cancelling a processing graph_maintenance job leaves it PROCESSING (Get), holding its
// serial key; Retry is ErrFailedPrecondition; a DedupePendingOrRunning enqueue no longer joins it; the
// handler's Complete is ErrCancelled and moves it to CANCELLED, after which the new same-key row is claimable.
func testCancelHoldsKey(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	g := job(bankA, domain.OperationKindGraphMaintenance, "graph", `{}`)
	g.Dedupe = port.DedupePendingOrRunning
	held := q.add(t, g)
	q.claimOne(t, held)
	if id, joined := q.enqueue(t, g); !joined || id != held {
		t.Fatalf("Enqueue before the cancel = (%s, %v), want to join the running %s", id, joined, held)
	}
	q.mustCancel(t, held, domain.OperationStatusProcessing)
	q.wantStatus(t, "after Cancel(processing)", held, domain.OperationStatusProcessing)
	q.wantAlive(t, "after Cancel(processing)", held, false)
	next, joined := q.enqueue(t, g)
	if joined || next == held {
		t.Errorf("Enqueue after the cancel = (%s, %v), want a new row", next, joined)
	}
	q.claimNone(t, "while the cancel-requested job holds its key")
	_, err := q.retry(t, held)
	wantErrIs(t, "Retry(cancel requested)", err, domain.ErrFailedPrecondition)
	q.wantStatus(t, "after Retry(cancel requested)", held, domain.OperationStatusProcessing)
	wantErrIs(t, "Complete(cancel requested)", q.c.Complete(t.Context(), held, json.RawMessage(`{}`), nil),
		domain.ErrCancelled)
	q.wantStatus(t, "after Complete", held, domain.OperationStatusCancelled)
	q.claimOne(t, next)
}

// testCancelReclaimed: ReclaimStale moves a cancel-requested job whose worker died to CANCELLED, releasing
// its key; the dead worker's late Complete is then ErrFailedPrecondition.
func testCancelReclaimed(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	first, second := q.add(t, doc("k")), q.add(t, doc("k"))
	q.claimOne(t, first)
	q.mustCancel(t, first, domain.OperationStatusProcessing)
	q.claimNone(t, "while the cancel-requested job holds its key")
	time.Sleep(staleSleep)
	if n := q.reclaim(t); n != 1 {
		t.Errorf("ReclaimStale touched %d rows, want 1 (the stale cancel-requested job)", n)
	}
	q.wantStatus(t, "after ReclaimStale", first, domain.OperationStatusCancelled)
	q.claimOne(t, second)
	wantErrIs(t, "late Complete of a cancelled job", q.c.Complete(t.Context(), first, json.RawMessage(`{}`), nil),
		domain.ErrFailedPrecondition)
}

// testCancelledStays: Complete, Fail and Reschedule of a job cancelled while pending (finished at once) are
// ErrFailedPrecondition and never resurrect it.
func testCancelledStays(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	id := q.add(t, doc("1"))
	if n := q.cancelBank(t, bankA); n != 1 {
		t.Errorf("CancelBank = %d, want 1", n)
	}
	wantErrIs(t, "Complete(cancelled)", q.c.Complete(t.Context(), id, json.RawMessage(`{}`), nil),
		domain.ErrFailedPrecondition)
	wantErrIs(t, "Fail(cancelled)", q.c.Fail(t.Context(), id, errors.New("late failure"), ptr(past())),
		domain.ErrFailedPrecondition)
	err := q.queueTx(t, func(qu port.Queue) error { return qu.Reschedule(t.Context(), id, past()) })
	wantErrIs(t, "Reschedule(cancelled)", err, domain.ErrFailedPrecondition)
	q.wantStatus(t, "after Complete/Fail/Reschedule", id, domain.OperationStatusCancelled)
	q.wantAlive(t, "cancelled job", id, false)
	q.claimNone(t, "after Complete/Fail/Reschedule of a cancelled job")
}

// failForGood claims id twice, failing it first with a retry and then for good, so it ends FAILED after an
// attempt.
func (q queueEnv) failForGood(t *testing.T, id port.OperationID) {
	t.Helper()
	q.claimOne(t, id)
	if err := q.c.Fail(t.Context(), id, errors.New("transient"), ptr(past())); err != nil {
		t.Fatalf("Fail(retry): %v", err)
	}
	q.claimOne(t, id)
	if err := q.c.Fail(t.Context(), id, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
}

// testRetry: Retry resets a failed or cancelled job to a claimable PENDING row at attempt 0 with
// the error cleared; Retry of a pending job is ErrFailedPrecondition.
func testRetry(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	failed := q.add(t, doc("failed"))
	q.failForGood(t, failed)
	cancelled := q.add(t, doc("cancelled"))
	q.mustCancel(t, cancelled, domain.OperationStatusCancelled)
	for _, id := range []port.OperationID{failed, cancelled} {
		op, err := q.retry(t, id)
		if err != nil {
			t.Fatalf("Retry(%s): %v", id, err)
		}
		if op.ID != id || op.Status != domain.OperationStatusPending || op.Attempts != 0 || op.Error != "" {
			t.Errorf("Retry(%s) = %s %q attempts %d error %q, want pending, 0, no error", id, op.ID, op.Status,
				op.Attempts, op.Error)
		}
	}
	_, err := q.retry(t, failed)
	wantErrIs(t, "Retry(pending)", err, domain.ErrFailedPrecondition)
	got := attemptsByID(q.claimAll(t))
	if len(got) != 2 || got[failed] != 0 || got[cancelled] != 0 {
		t.Errorf("Claim after Retry = %v, want both jobs at attempt 0", got)
	}
}

// pruneOps runs Queue.Prune(before) and returns how many it deleted.
func (q queueEnv) pruneOps(t *testing.T, before time.Time) int {
	t.Helper()
	var n int
	if err := q.queueTx(t, func(qu port.Queue) error {
		var err error
		n, err = qu.Prune(t.Context(), before)
		return err
	}); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	return n
}

// testPrune: Prune deletes only terminal, finished operations finished before the cutoff; pending,
// processing operations (cancel-requested ones included) stay.
func testPrune(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	done := q.add(t, doc("done"))
	q.claimOne(t, done)
	q.complete(t, done)
	failed := q.add(t, doc("failed"))
	q.claimOne(t, failed)
	if err := q.c.Fail(t.Context(), failed, errors.New("fatal"), nil); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	winding := q.add(t, doc("winding-down"))
	q.claimOne(t, winding)
	q.mustCancel(t, winding, domain.OperationStatusProcessing)
	running := q.add(t, doc("running"))
	q.claimOne(t, running)
	cancelled := q.add(t, doc("cancelled"))
	q.mustCancel(t, cancelled, domain.OperationStatusCancelled)
	pending := q.add(t, doc("pending"))
	if n := q.pruneOps(t, past()); n != 0 {
		t.Errorf("Prune(an hour ago) = %d, want 0", n)
	}
	if n := q.pruneOps(t, future()); n != 3 {
		t.Errorf("Prune(in an hour) = %d, want 3 (completed, failed, cancelled)", n)
	}
	for _, id := range []port.OperationID{done, failed, cancelled} {
		_, err := getOp(t, q.s, bankA, id)
		wantErrIs(t, "Get of a pruned operation", err, domain.ErrNotFound)
	}
	q.wantStatus(t, "cancel requested while processing", winding, domain.OperationStatusProcessing)
	q.wantStatus(t, "processing", running, domain.OperationStatusProcessing)
	q.wantStatus(t, "pending", pending, domain.OperationStatusPending)
}

// ---------- CancelBank, deleting and deleted banks ----------

// testCancelBank: CancelBank cancels the bank's pending jobs (parked included) except its delete_bank job;
// processing jobs and other banks' jobs are untouched.
func testCancelBank(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA, bankB)
	running := q.add(t, doc("running"))
	q.claimOne(t, running)
	p1, p2 := q.add(t, doc("p1")), q.add(t, doc("p2"))
	parked := doc("parked")
	parked.RunAt = future()
	p3 := q.add(t, parked)
	del := q.add(t, job(bankA, domain.OperationKindDeleteBank, "bank", `{}`))
	other := q.add(t, withBank(doc("other"), bankB))
	if n := q.cancelBank(t, bankA); n != 3 {
		t.Errorf("CancelBank = %d, want 3 (the pending jobs, parked included, not delete_bank)", n)
	}
	for _, id := range []port.OperationID{p1, p2, p3} {
		q.wantStatus(t, "after CancelBank", id, domain.OperationStatusCancelled)
	}
	q.wantStatus(t, "processing job after CancelBank", running, domain.OperationStatusProcessing)
	q.wantAlive(t, "processing job of a cancelled bank", running, true)
	wantSet(t, "Claim after CancelBank", jobIDs(q.claimAll(t)), []port.OperationID{del, other})
}

// testDeletingBank: a tombstoned bank's jobs are skipped by Claim except delete_bank; Alive is false for its
// other jobs and true for delete_bank; CancelBank spares delete_bank.
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
	q.wantAlive(t, "processing job of a tombstoned bank", running, false)
	if n := q.cancelBank(t, bankA); n != 1 {
		t.Errorf("CancelBank(tombstoned) = %d, want 1 (the pending job, not delete_bank)", n)
	}
	q.wantStatus(t, "pending job after CancelBank", pending, domain.OperationStatusCancelled)
	wantSet(t, "Claim with bank A tombstoned", jobIDs(q.claimAll(t)), []port.OperationID{del, other})
	q.wantAlive(t, "delete_bank job of a tombstoned bank", del, true)
	q.wantAlive(t, "other bank's job", other, true)
}

// testEnqueueTombstoned: Enqueue on a tombstoned bank is ErrBankDeleting except for delete_bank; on an
// unknown bank it is ErrNotFound.
func testEnqueueTombstoned(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	inTx(t, q.s, func(tx port.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	err := q.queueTx(t, func(qu port.Queue) error {
		_, _, err := qu.Enqueue(t.Context(), doc("1"))
		return err
	})
	wantErrIs(t, "Enqueue(tombstoned bank)", err, domain.ErrBankDeleting)
	del := q.add(t, job(bankA, domain.OperationKindDeleteBank, "bank", `{}`))
	err = q.queueTx(t, func(qu port.Queue) error {
		_, _, err := qu.Enqueue(t.Context(), withBank(doc("1"), bankB))
		return err
	})
	wantErrIs(t, "Enqueue(unknown bank)", err, domain.ErrNotFound)
	q.claimOne(t, del)
}

// testBankDeleteOps: BankRepo.Delete removes the bank's operations (the running delete_bank job's own row
// included, so its Complete is ErrNotFound) and events; the bank re-created afterwards is empty.
func testBankDeleteOps(t *testing.T, q queueEnv) {
	ensureBanks(t, q.s, bankA)
	appendEvents(t, q.s, bankA, "event")
	old := q.add(t, doc("1"))
	del := q.add(t, job(bankA, domain.OperationKindDeleteBank, "bank", `{}`))
	inTx(t, q.s, func(tx port.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	q.claimOne(t, del)
	inTx(t, q.s, func(tx port.Tx) error { return tx.Banks().Delete(t.Context(), bankA) })
	wantErrIs(t, "Complete of the deleted delete_bank job", q.c.Complete(t.Context(), del, json.RawMessage(`{}`), nil),
		domain.ErrNotFound)
	ensureBanks(t, q.s, bankA)
	for _, id := range []port.OperationID{old, del} {
		_, err := getOp(t, q.s, bankA, id)
		wantErrIs(t, "Get of an operation of the deleted bank", err, domain.ErrNotFound)
	}
	wantSeq(t, "List after Delete and Ensure", opIDs(listOps(t, q.s, bankA, port.OperationFilter{})), nil)
	wantSeq(t, "Events after Delete and Ensure", eventSubjects(listEvents(t, q.s, bankA, 0, 10)), nil)
	if got := bankSeq(t, q.s, bankA); got != 0 {
		t.Errorf("BankSeq of the re-created bank = %d, want 0", got)
	}
	q.claimNone(t, "after the bank was deleted")
}

// testQueueTenants: the same bank id under another tenant shares no queue rows: dedupe, Get, List,
// CancelBank and serial keys stay within one tenant.
func testQueueTenants(t *testing.T, q queueEnv) {
	other := bankOtherTenant
	ensureBanks(t, q.s, bankA, other)
	j := job(bankA, domain.OperationKindConsolidate, "consolidate", `{}`)
	j.Dedupe = port.DedupePending
	mine := q.add(t, j)
	theirs := q.add(t, withBank(j, other)) // must not join tenant-1's row
	_, err := getOp(t, q.s, other, mine)
	wantErrIs(t, "Get(other tenant, tenant-1 operation)", err, domain.ErrNotFound)
	_, err = getOp(t, q.s, bankA, theirs)
	wantErrIs(t, "Get(tenant-1, other tenant's operation)", err, domain.ErrNotFound)
	wantSeq(t, "List(other tenant)", opIDs(listOps(t, q.s, other, port.OperationFilter{})), []port.OperationID{theirs})
	if n := q.cancelBank(t, other); n != 1 {
		t.Errorf("CancelBank(other tenant) = %d, want 1", n)
	}
	q.wantStatus(t, "tenant-1 job after the other tenant's CancelBank", mine, domain.OperationStatusPending)
	k1, k2 := q.add(t, doc("k")), q.add(t, withBank(doc("k"), other))
	wantSet(t, "Claim of one serial key in two tenants", jobIDs(q.claimAll(t)), []port.OperationID{mine, k1, k2})
}
