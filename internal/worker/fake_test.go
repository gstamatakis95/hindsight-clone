package worker

// Test-local fakes: a stateful, race-safe operations table that implements port.Claimer and the parts of
// port.Queue the worker uses, a Store over it, and a manual Clock. They mirror the port docs (fencing,
// cancel requests, reclaim with the attempt cap, dedupe) closely enough for the runner's tests; swap them
// for internal/fake once WP-02 lands.

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manual port.Clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

// newClock starts at a fixed instant.
func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeRow is one operations row.
type fakeRow struct {
	job        port.Job
	status     domain.OperationStatus
	worker     string
	heartbeat  time.Time
	cancelReq  bool
	joinable   bool
	created    int
	finishedAt time.Time
	errMsg     string
	result     json.RawMessage
	usage      []port.Usage
	progress   []domain.Progress
}

// failCall records one Fail.
type failCall struct {
	id      port.OperationID
	attempt int
	retryAt *time.Time
	cause   error
}

// fakeOps is the operations table: port.Claimer plus Enqueue, Cancel, Retry, List and Prune of
// port.Queue (other Queue methods panic through the nil embedded interface).
type fakeOps struct {
	port.Queue
	mu       sync.Mutex
	clock    *fakeClock
	rows     map[port.OperationID]*fakeRow
	next     int
	deleting map[port.BankRef]bool
	fails    []failCall
	prunedAt []time.Time
}

var (
	_ port.Claimer = (*fakeOps)(nil)
	_ port.Queue   = (*fakeOps)(nil)
)

func newOps(c *fakeClock) *fakeOps {
	return &fakeOps{clock: c, rows: map[port.OperationID]*fakeRow{}, deleting: map[port.BankRef]bool{}}
}

// insertLocked adds a PENDING row for j.
func (f *fakeOps) insertLocked(j port.Job) port.OperationID {
	f.next++
	j.ID = port.OperationID(fmt.Sprintf("op-%03d", f.next))
	if j.RunAt.IsZero() {
		j.RunAt = f.clock.Now()
	}
	joinable := (j.Dedupe != port.DedupeNone && j.Overrides == nil) ||
		j.Kind == domain.OperationKindRefreshMentalModel
	f.rows[j.ID] = &fakeRow{job: j, status: domain.OperationStatusPending, joinable: joinable, created: f.next}
	return j.ID
}

// add inserts a job directly (test setup).
func (f *fakeOps) add(j port.Job) port.OperationID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insertLocked(j)
}

// row returns a copy of a row.
func (f *fakeOps) row(id port.OperationID) fakeRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.rows[id]
}

// count counts rows matching pred.
func (f *fakeOps) count(pred func(r *fakeRow) bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rows {
		if pred(r) {
			n++
		}
	}
	return n
}

// failCalls returns the recorded Fail calls.
func (f *fakeOps) failCalls() []failCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.fails)
}

// ---------- port.Claimer ----------

func (f *fakeOps) Claim(_ context.Context, worker string, kinds []port.JobKind, n int) ([]port.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.clock.Now()
	var due []*fakeRow
	for _, r := range f.rows {
		if r.status == domain.OperationStatusPending && !r.job.RunAt.After(now) && slices.Contains(kinds, r.job.Kind) &&
			(!f.deleting[r.job.Bank] || r.job.Kind == domain.OperationKindDeleteBank) {
			due = append(due, r)
		}
	}
	slices.SortFunc(due, func(a, b *fakeRow) int { return a.created - b.created })
	var out []port.Job
	for _, r := range due {
		if len(out) >= n {
			break
		}
		if f.serialBlockedLocked(r, now) {
			continue
		}
		r.status, r.worker, r.heartbeat = domain.OperationStatusProcessing, worker, now
		out = append(out, r.job)
	}
	return out, nil
}

// serialBlockedLocked: r's serial key has a PROCESSING row or an older due PENDING row.
func (f *fakeOps) serialBlockedLocked(r *fakeRow, now time.Time) bool {
	if r.job.SerialKey == "" {
		return false
	}
	for _, o := range f.rows {
		if o == r || o.job.Bank != r.job.Bank || o.job.SerialKey != r.job.SerialKey {
			continue
		}
		if o.status == domain.OperationStatusProcessing ||
			(o.status == domain.OperationStatusPending && !o.job.RunAt.After(now) && o.created < r.created) {
			return true
		}
	}
	return false
}

func (f *fakeOps) Heartbeat(_ context.Context, worker string, ids []port.OperationID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lost []port.OperationID
	for _, id := range ids {
		r := f.rows[id]
		if r == nil || r.status != domain.OperationStatusProcessing || r.worker != worker {
			lost = append(lost, id)
			continue
		}
		r.heartbeat = f.clock.Now()
		if r.cancelReq {
			lost = append(lost, id)
		}
	}
	if len(lost) > 0 {
		return &port.LostJobsError{IDs: lost}
	}
	return nil
}

func (f *fakeOps) Progress(_ context.Context, id port.OperationID, p domain.Progress) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil {
		return domain.ErrNotFound
	}
	if r.status != domain.OperationStatusProcessing {
		return domain.ErrFailedPrecondition
	}
	r.progress = append(r.progress, p)
	return nil
}

func (f *fakeOps) Alive(_ context.Context, id port.OperationID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	return r != nil && r.status == domain.OperationStatusProcessing && !r.cancelReq && !f.deleting[r.job.Bank], nil
}

// fenceLocked applies the Complete/Fail fencing; done=true means the call is over with err.
func (f *fakeOps) fenceLocked(id port.OperationID) (r *fakeRow, done bool, err error) {
	r = f.rows[id]
	switch {
	case r == nil:
		return nil, true, domain.ErrNotFound
	case r.status != domain.OperationStatusProcessing:
		return r, true, domain.ErrFailedPrecondition
	case r.cancelReq:
		r.status, r.finishedAt = domain.OperationStatusCancelled, f.clock.Now()
		return r, true, domain.ErrCancelled
	}
	return r, false, nil
}

func (f *fakeOps) Complete(_ context.Context, id port.OperationID, result json.RawMessage, usage []port.Usage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, done, err := f.fenceLocked(id)
	if done {
		return err
	}
	r.status, r.finishedAt, r.result, r.usage = domain.OperationStatusCompleted, f.clock.Now(), result, usage
	return nil
}

func (f *fakeOps) Fail(_ context.Context, id port.OperationID, cause error, retryAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.rows[id]; r != nil {
		f.fails = append(f.fails, failCall{id: id, attempt: r.job.Attempt, retryAt: retryAt, cause: cause})
	}
	r, done, err := f.fenceLocked(id)
	if done {
		return err
	}
	r.errMsg = fmt.Sprint(cause)
	if retryAt != nil {
		r.status, r.job.RunAt, r.worker = domain.OperationStatusPending, *retryAt, ""
		r.job.Attempt++
		return nil
	}
	r.status, r.finishedAt = domain.OperationStatusFailed, f.clock.Now()
	return nil
}

func (f *fakeOps) ReclaimStale(_ context.Context, ttl time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now, n := f.clock.Now(), 0
	for _, r := range f.rows {
		if r.status != domain.OperationStatusProcessing || now.Sub(r.heartbeat) <= ttl {
			continue
		}
		n++
		maxAttempts := r.job.Kind.MaxAttempts()
		switch {
		case r.cancelReq:
			r.status, r.finishedAt = domain.OperationStatusCancelled, now
		case maxAttempts > 0 && r.job.Attempt+1 >= maxAttempts:
			r.status, r.finishedAt = domain.OperationStatusFailed, now
		default:
			r.status, r.worker = domain.OperationStatusPending, ""
			r.job.Attempt++
		}
	}
	return n, nil
}

// ---------- port.Queue (the parts the worker uses) ----------

func (f *fakeOps) Enqueue(_ context.Context, j port.Job) (port.OperationID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleting[j.Bank] && j.Kind != domain.OperationKindDeleteBank {
		return "", false, domain.ErrBankDeleting
	}
	if j.Dedupe != port.DedupeNone && (j.Overrides == nil || j.Kind == domain.OperationKindRefreshMentalModel) {
		for id, r := range f.rows {
			if r.joinable && !r.cancelReq && r.job.Bank == j.Bank && r.job.Kind == j.Kind &&
				r.job.SerialKey == j.SerialKey && (r.status == domain.OperationStatusPending ||
				(j.Dedupe == port.DedupePendingOrRunning && r.status == domain.OperationStatusProcessing)) {
				return id, true, nil
			}
		}
	}
	return f.insertLocked(j), false, nil
}

func (f *fakeOps) op(r *fakeRow) domain.Operation {
	return domain.Operation{
		ID: r.job.ID, Bank: r.job.Bank, Kind: r.job.Kind, Status: r.status, SerialKey: r.job.SerialKey,
		Error: r.errMsg, Attempts: r.job.Attempt, WorkerID: r.worker, NextRunAt: &r.job.RunAt,
	}
}

func (f *fakeOps) Get(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil || r.job.Bank != b {
		return domain.Operation{}, domain.ErrNotFound
	}
	return f.op(r), nil
}

func (f *fakeOps) Cancel(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil || r.job.Bank != b {
		return domain.Operation{}, domain.ErrNotFound
	}
	switch r.status {
	case domain.OperationStatusPending:
		r.status, r.finishedAt = domain.OperationStatusCancelled, f.clock.Now()
	case domain.OperationStatusProcessing:
		r.cancelReq, r.joinable = true, false
	default:
		return domain.Operation{}, domain.ErrFailedPrecondition
	}
	return f.op(r), nil
}

func (f *fakeOps) Retry(_ context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[id]
	if r == nil || r.job.Bank != b {
		return domain.Operation{}, domain.ErrNotFound
	}
	if r.status != domain.OperationStatusFailed && r.status != domain.OperationStatusCancelled {
		return domain.Operation{}, domain.ErrFailedPrecondition
	}
	r.status, r.job.RunAt, r.job.Attempt = domain.OperationStatusPending, f.clock.Now(), 0
	r.cancelReq, r.errMsg = false, ""
	return f.op(r), nil
}

func (f *fakeOps) List(
	_ context.Context, b port.BankRef, flt port.OperationFilter,
) ([]domain.Operation, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Operation
	for _, r := range f.rows {
		if r.job.Bank != b || (flt.Status != "" && r.status != flt.Status) || (flt.Kind != "" && r.job.Kind != flt.Kind) {
			continue
		}
		if flt.PageSize > 0 && len(out) >= flt.PageSize {
			break
		}
		out = append(out, f.op(r))
	}
	return out, "", nil
}

func (f *fakeOps) Prune(_ context.Context, before time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prunedAt = append(f.prunedAt, before)
	n := 0
	for id, r := range f.rows {
		if r.status.Terminal() && r.finishedAt.Before(before) {
			delete(f.rows, id)
			n++
		}
	}
	return n, nil
}

// ---------- port.Store over fakeOps ----------

// fakeStore runs every transaction directly against fakeOps (each call is atomic under its mutex).
type fakeStore struct {
	ops *fakeOps
	mu  sync.Mutex
	// eventsPrunedBefore records EventRepo.Prune cut-offs.
	eventsPrunedBefore []time.Time
	// txErr, when set, fails every InTx.
	txErr error
}

var _ port.Store = (*fakeStore)(nil)

func (s *fakeStore) InTx(_ context.Context, fn func(port.Tx) error) error {
	if s.txErr != nil {
		return s.txErr
	}
	s.ops.mu.Lock()
	before := s.ops.next
	s.ops.mu.Unlock()
	err := fn(&fakeTx{s: s})
	if err != nil { // roll back the rows this tx inserted (tests don't run rolling-back txs concurrently)
		s.ops.mu.Lock()
		for id, r := range s.ops.rows {
			if r.created > before {
				delete(s.ops.rows, id)
			}
		}
		s.ops.mu.Unlock()
	}
	return err
}

func (s *fakeStore) Read() port.Reader { return nil }

// fakeTx serves Queue and Events; any other Tx method panics through the nil embedded interface.
type fakeTx struct {
	port.Tx
	s *fakeStore
}

func (t *fakeTx) Queue() port.Queue { return t.s.ops }

func (t *fakeTx) Events() port.EventRepo { return &fakeEvents{s: t.s} }

// fakeEvents records Prune calls.
type fakeEvents struct {
	port.EventRepo
	s *fakeStore
}

func (e *fakeEvents) Prune(_ context.Context, before time.Time) (int, error) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	e.s.eventsPrunedBefore = append(e.s.eventsPrunedBefore, before)
	return 0, nil
}

// ---------- helpers ----------

var (
	bankA = port.BankRef{Tenant: "t1", Bank: "a"}
	bankB = port.BankRef{Tenant: "t1", Bank: "b"}
	bankC = port.BankRef{Tenant: "t2", Bank: "c"}
)

// retainJob is a retain_document job for document doc of bankA.
func retainJob(doc string) port.Job {
	return port.Job{Bank: bankA, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + doc}
}

// consolidateJob is bank b's consolidate job.
func consolidateJob(b port.BankRef) port.Job {
	return port.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Dedupe: port.DedupePending,
	}
}

// eventually polls cond every millisecond until it holds or 5 s pass.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// startRunner runs r until the test ends (or stop is called) and returns stop, which waits for Run.
func startRunner(t *testing.T, r *Runner) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// testRunner builds a fast-polling runner over c.
func testRunner(c port.Claimer, clock port.Clock, cfg RunnerConfig) *Runner {
	cfg.Clock = clock
	if cfg.WorkerID == "" {
		cfg.WorkerID = "w1"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Millisecond
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = time.Hour
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = -1 // cancel at once, so test cleanup doesn't wait
	}
	return NewRunner(c, cfg)
}
