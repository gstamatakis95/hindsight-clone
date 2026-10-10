package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"memd/internal/domain"
	"memd/internal/port"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// quiet is a logger that drops everything, so test output stays readable.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func init() { slog.SetDefault(quiet) }

// blockUntil blocks until release is closed or ctx is done.
func blockUntil(ctx context.Context, release <-chan struct{}) error {
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("blocked: %w", ctx.Err())
	}
}

// Accept: reserved slots hold under a retain flood.
func TestRunnerReservedSlotsHoldUnderRetainFlood(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	const flood = 40
	for i := range flood {
		ops.add(retainJob(fmt.Sprint(i)))
	}
	release := make(chan struct{})
	var running, maxRunning atomic.Int32
	r := testRunner(ops, clock, RunnerConfig{})
	r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, _ port.Job) error {
		n := running.Add(1)
		for m := maxRunning.Load(); n > m && !maxRunning.CompareAndSwap(m, n); m = maxRunning.Load() {
		}
		defer running.Add(-1)
		return blockUntil(ctx, release)
	})
	consStarted := make(chan port.OperationID, 2)
	r.Handle(domain.OperationKindConsolidate, func(ctx context.Context, j port.Job) error {
		consStarted <- j.ID
		return blockUntil(ctx, release)
	})
	startRunner(t, r)

	processing := func(kind port.JobKind) int {
		return ops.count(func(r *fakeRow) bool {
			return r.status == domain.OperationStatusProcessing && r.job.Kind == kind
		})
	}
	eventually(t, "8 retains running", func() bool { return running.Load() == 8 })
	time.Sleep(30 * time.Millisecond) // many polls with free slots
	if got := processing(domain.OperationKindRetainDocument); got != 8 {
		t.Fatalf("processing retains = %d, want 8 (10 slots, 2 reserved)", got)
	}
	ops.add(consolidateJob(bankA))
	ops.add(consolidateJob(bankB))
	for range 2 {
		select {
		case <-consStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("a consolidate did not start while retains filled their slots")
		}
	}
	if got := processing(domain.OperationKindRetainDocument); got != 8 {
		t.Errorf("processing retains with consolidates running = %d, want 8", got)
	}
	close(release)
	eventually(t, "every job completed", func() bool {
		return ops.count(func(r *fakeRow) bool { return r.status == domain.OperationStatusCompleted }) == flood+2
	})
	if m := maxRunning.Load(); m != 8 {
		t.Errorf("max concurrent retains = %d, want 8", m)
	}
}

// retryStep is one expected Fail: the retry offset from the clock at the failure, or terminal.
type retryStep struct {
	after    time.Duration
	terminal bool
}

// driveRetries runs one job of kind whose handler returns errFor(run) until the expected Fail calls have
// happened, advancing the clock to each retry time, and checks each call.
func driveRetries(t *testing.T, j port.Job, errFor func(run int, now time.Time) error, want []retryStep,
	pending PendingChecker, ops *fakeOps, clock *fakeClock,
) port.OperationID {
	t.Helper()
	r := testRunner(ops, clock, RunnerConfig{Pending: pending})
	var runs atomic.Int32
	r.Handle(j.Kind, func(_ context.Context, _ port.Job) error {
		return errFor(int(runs.Add(1)), clock.Now())
	})
	id := ops.add(j)
	startRunner(t, r)
	for i, w := range want {
		eventually(t, fmt.Sprintf("fail call %d", i+1), func() bool { return len(ops.failCalls()) > i })
		fc := ops.failCalls()[i]
		now := clock.Now()
		if fc.attempt != i {
			t.Errorf("fail %d: attempt %d, want %d", i+1, fc.attempt, i)
		}
		if w.terminal {
			if fc.retryAt != nil {
				t.Fatalf("fail %d: retry at %v, want terminal", i+1, fc.retryAt.Sub(now))
			}
			continue
		}
		if fc.retryAt == nil {
			t.Fatalf("fail %d: terminal, want retry after %v", i+1, w.after)
		}
		if got := fc.retryAt.Sub(now); got != w.after {
			t.Fatalf("fail %d: retry after %v, want %v", i+1, got, w.after)
		}
		clock.Advance(w.after)
	}
	return id
}

// Accept: retries are scheduled at the right times.
func TestRunnerRetrySchedule(t *testing.T) {
	boom := errors.New("gateway timeout")
	always := func(int, time.Time) error { return boom }
	m := time.Minute
	tests := []struct {
		name       string
		job        port.Job
		errFor     func(int, time.Time) error
		want       []retryStep
		wantStatus domain.OperationStatus
	}{
		{
			"linear backoff then the 3-attempt cap", retainJob("x"), always,
			[]retryStep{{after: m}, {after: 2 * m}, {terminal: true}},
			domain.OperationStatusFailed,
		},
		{"permanent fails at once", retainJob("x"), func(int, time.Time) error {
			return fmt.Errorf("%w: schema violation", ErrPermanent)
		}, []retryStep{{terminal: true}}, domain.OperationStatusFailed},
		{"ErrRetryAt sets the time, the cap still applies", retainJob("x"), func(_ int, now time.Time) error {
			return RetryAt(now.Add(5*m), boom)
		}, []retryStep{{after: 5 * m}, {after: 5 * m}, {terminal: true}}, domain.OperationStatusFailed},
		{"consolidate: capped exponential, unbounded", consolidateJob(bankA), func(run int, _ time.Time) error {
			if run <= 7 {
				return boom
			}
			return nil
		}, []retryStep{
			{after: m},
			{after: 2 * m},
			{after: 4 * m},
			{after: 8 * m},
			{after: 16 * m},
			{after: 30 * m},
			{after: 30 * m},
		}, domain.OperationStatusCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newClock()
			ops := newOps(clock)
			id := driveRetries(t, tt.job, tt.errFor, tt.want, nil, ops, clock)
			eventually(t, "final status "+string(tt.wantStatus), func() bool { return ops.row(id).status == tt.wantStatus })
		})
	}
}

// A failed consolidate skips its retry when another consolidate is already pending for the bank.
func TestRunnerConsolidateSkipsRetryWhenPending(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	store := &fakeStore{ops: ops}
	// A parked consolidate of bank A (overrides, so not joinable; not due, so not claimed).
	ops.add(port.Job{
		Bank: bankA, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		RunAt: clock.Now().Add(time.Hour), Overrides: &domain.BankConfig{},
	})
	id := driveRetries(t, consolidateJob(bankA), func(int, time.Time) error { return errors.New("boom") },
		[]retryStep{{terminal: true}}, NewPendingChecker(store), ops, clock)
	eventually(t, "consolidate failed", func() bool { return ops.row(id).status == domain.OperationStatusFailed })

	// Bank B has no other consolidate pending, so it retries.
	clock2 := newClock()
	ops2 := newOps(clock2)
	driveRetries(t, consolidateJob(bankB), func(int, time.Time) error { return errors.New("boom") },
		[]retryStep{{after: time.Minute}}, NewPendingChecker(&fakeStore{ops: ops2}), ops2, clock2)
}

func TestRetryPolicy(t *testing.T) {
	now := newClock().Now()
	boom := errors.New("boom")
	tests := []struct {
		name    string
		kind    port.JobKind
		attempt int
		err     error
		after   time.Duration
		retry   bool
	}{
		{"retain first failure", domain.OperationKindRetainDocument, 0, boom, time.Minute, true},
		{"retain second failure", domain.OperationKindRetainDocument, 1, boom, 2 * time.Minute, true},
		{"retain third failure is terminal", domain.OperationKindRetainDocument, 2, boom, 0, false},
		{"refresh capped too", domain.OperationKindRefreshMentalModel, 2, boom, 0, false},
		{"permanent", domain.OperationKindRetainDocument, 0, fmt.Errorf("x: %w", ErrPermanent), 0, false},
		{"invalid argument", domain.OperationKindRetainDocument, 0, domain.ErrInvalidArgument, 0, false},
		{"cancelled", domain.OperationKindConsolidate, 0, domain.ErrCancelled, 0, false},
		{"consolidate attempt 0", domain.OperationKindConsolidate, 0, boom, time.Minute, true},
		{"consolidate attempt 3", domain.OperationKindConsolidate, 3, boom, 8 * time.Minute, true},
		{"consolidate capped", domain.OperationKindConsolidate, 50, boom, ConsolidateBackoffCap, true},
		{"retry-at wins", domain.OperationKindRetainDocument, 0, RetryAt(now.Add(time.Hour), boom), time.Hour, true},
		{"zero retry-at uses backoff", domain.OperationKindRetainDocument, 1, &ErrRetryAt{}, 2 * time.Minute, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at, ok := nextRun(port.Job{Kind: tt.kind, Attempt: tt.attempt}, tt.err, now)
			if ok != tt.retry {
				t.Fatalf("retry = %v, want %v", ok, tt.retry)
			}
			if ok && at.Sub(now) != tt.after {
				t.Errorf("retry after %v, want %v", at.Sub(now), tt.after)
			}
		})
	}
}

// Accept: a cancelled job stops at its next checkpoint.
func TestRunnerCancelledJobStopsAtCheckpoint(t *testing.T) {
	t.Run("checkpoint sees Alive=false", func(t *testing.T) {
		clock := newClock()
		ops := newOps(clock)
		steps := make(chan int, 10)
		next := make(chan struct{})
		r := testRunner(ops, clock, RunnerConfig{})
		r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, j port.Job) error {
			cp := NewCheckpoint(ops, j.ID)
			for i := 1; ; i++ {
				p := domain.Progress{Stage: "chunks", Processed: int64(i), At: clock.Now()}
				if err := cp.Report(ctx, p); err != nil {
					return fmt.Errorf("checkpoint: %w", err)
				}
				steps <- i
				<-next
			}
		})
		id := ops.add(retainJob("1"))
		startRunner(t, r)
		if got := <-steps; got != 1 {
			t.Fatalf("first step = %d", got)
		}
		if _, err := ops.Cancel(context.Background(), bankA, id); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		close(next)
		eventually(t, "row CANCELLED", func() bool { return ops.row(id).status == domain.OperationStatusCancelled })
		select {
		case s := <-steps:
			t.Errorf("handler ran step %d after the cancel", s)
		default:
		}
		fails := ops.failCalls()
		if len(fails) != 1 || fails[0].retryAt != nil || !errors.Is(fails[0].cause, domain.ErrCancelled) {
			t.Errorf("fail calls = %+v, want one terminal Fail with ErrCancelled", fails)
		}
	})
	t.Run("heartbeat reports the job lost", func(t *testing.T) {
		clock := newClock()
		ops := newOps(clock)
		started := make(chan struct{})
		r := testRunner(ops, clock, RunnerConfig{HeartbeatInterval: 2 * time.Millisecond})
		r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, _ port.Job) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		id := ops.add(retainJob("1"))
		startRunner(t, r)
		<-started
		if _, err := ops.Cancel(context.Background(), bankA, id); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		eventually(t, "row CANCELLED", func() bool { return ops.row(id).status == domain.OperationStatusCancelled })
		if fails := ops.failCalls(); len(fails) != 1 || fails[0].retryAt != nil {
			t.Errorf("fail calls = %+v, want one terminal Fail", fails)
		}
	})
}

// killable wraps a Claimer; after kill every call fails without reaching it, like a dead process.
type killable struct {
	port.Claimer
	mu   sync.RWMutex
	dead bool
}

var errDead = errors.New("worker process is dead")

// kill waits for in-flight calls, then cuts the worker off.
func (k *killable) kill() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.dead = true
}

// call runs fn unless the worker is dead.
func (k *killable) call(fn func() error) error {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.dead {
		return errDead
	}
	return fn()
}

func (k *killable) Claim(ctx context.Context, w string, kinds []port.JobKind, n int) ([]port.Job, error) {
	var js []port.Job
	err := k.call(func() (err error) { js, err = k.Claimer.Claim(ctx, w, kinds, n); return err })
	return js, err
}

func (k *killable) Heartbeat(ctx context.Context, w string, ids []port.OperationID) error {
	return k.call(func() error { return k.Claimer.Heartbeat(ctx, w, ids) })
}

func (k *killable) Complete(ctx context.Context, id port.OperationID, res json.RawMessage, u []port.Usage) error {
	return k.call(func() error { return k.Claimer.Complete(ctx, id, res, u) })
}

func (k *killable) Fail(ctx context.Context, id port.OperationID, cause error, at *time.Time) error {
	return k.call(func() error { return k.Claimer.Fail(ctx, id, cause, at) })
}

// Accept: a worker killed mid-job gets its job reclaimed.
func TestKilledWorkerJobReclaimed(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	store := &fakeStore{ops: ops}
	a := &killable{Claimer: ops}
	started := make(chan struct{})
	ra := testRunner(a, clock, RunnerConfig{WorkerID: "worker-a", HeartbeatInterval: 2 * time.Millisecond})
	ra.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, _ port.Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	id := ops.add(retainJob("1"))
	startRunner(t, ra)
	<-started
	a.kill()

	clock.Advance(DefaultStaleAfter + time.Second)
	deps := MaintenanceDeps{Claimer: ops, Store: store, Clock: clock, Logger: quiet}.withDefaults()
	if err := maintenanceTick(context.Background(), deps); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if row := ops.row(id); row.status != domain.OperationStatusPending || row.job.Attempt != 1 {
		t.Fatalf("after reclaim: status %s attempt %d, want pending attempt 1", row.status, row.job.Attempt)
	}

	seen := make(chan int, 1)
	rb := testRunner(ops, clock, RunnerConfig{WorkerID: "worker-b"})
	rb.Handle(domain.OperationKindRetainDocument, func(_ context.Context, j port.Job) error {
		seen <- j.Attempt
		return nil
	})
	startRunner(t, rb)
	if got := <-seen; got != 1 {
		t.Errorf("worker b ran attempt %d, want 1", got)
	}
	eventually(t, "worker b completed the job", func() bool {
		r := ops.row(id)
		return r.status == domain.OperationStatusCompleted && r.worker == "worker-b"
	})
}

// A job lost to another worker (reclaimed and re-claimed) is stopped here and its row is left alone.
func TestRunnerLostToAnotherWorkerLeavesRowAlone(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	started := make(chan struct{})
	stopped := make(chan struct{})
	r := testRunner(ops, clock, RunnerConfig{WorkerID: "w1", HeartbeatInterval: 2 * time.Millisecond})
	r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, _ port.Job) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return fmt.Errorf("stopped: %w", ctx.Err())
	})
	id := ops.add(retainJob("1"))
	startRunner(t, r)
	<-started
	ops.mu.Lock()
	ops.rows[id].worker = "w2" // reclaimed and re-claimed elsewhere
	ops.mu.Unlock()
	<-stopped
	time.Sleep(20 * time.Millisecond)
	if fails := ops.failCalls(); len(fails) != 0 {
		t.Errorf("fail calls = %+v, want none", fails)
	}
	if row := ops.row(id); row.status != domain.OperationStatusProcessing || row.worker != "w2" {
		t.Errorf("row: status %s worker %s, want processing under w2", row.status, row.worker)
	}
}

// A panicking handler is retried like a transient failure, and its slot is freed.
func TestRunnerPanicRetries(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	r := testRunner(ops, clock, RunnerConfig{Slots: 1})
	var runs atomic.Int32
	r.Handle(domain.OperationKindRetainDocument, func(context.Context, port.Job) error {
		if runs.Add(1) == 1 {
			panic("nil map")
		}
		return nil
	})
	id := ops.add(retainJob("1"))
	other := ops.add(retainJob("2"))
	startRunner(t, r)
	eventually(t, "panic recorded as a retry", func() bool { return len(ops.failCalls()) == 1 })
	if fc := ops.failCalls()[0]; fc.id != id || fc.retryAt == nil || fc.retryAt.Sub(clock.Now()) != RetryStep {
		t.Fatalf("fail call = %+v, want a retry of %s after %v", fc, id, RetryStep)
	}
	eventually(t, "the other job ran in the freed slot", func() bool {
		return ops.row(other).status == domain.OperationStatusCompleted
	})
}

// On shutdown the runner cancels running handlers and releases their jobs for an immediate re-run.
func TestRunnerShutdownReleasesJobs(t *testing.T) {
	tests := []struct {
		name   string
		stopIn func(ctx context.Context, ops *fakeOps, j port.Job) error
	}{
		{"checkpoint error", func(ctx context.Context, ops *fakeOps, j port.Job) error {
			return NewCheckpoint(ops, j.ID).Report(ctx, domain.Progress{})
		}},
		{"error that drops context.Canceled", func(ctx context.Context, _ *fakeOps, _ port.Job) error {
			return fmt.Errorf("%w: gateway: %s", domain.ErrUnavailable, ctx.Err().Error())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newClock()
			ops := newOps(clock)
			started := make(chan struct{})
			r := testRunner(ops, clock, RunnerConfig{})
			r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, j port.Job) error {
				close(started)
				<-ctx.Done()
				return tt.stopIn(ctx, ops, j)
			})
			id := ops.add(port.Job{Bank: bankA, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:1", Attempt: 2})
			stop := startRunner(t, r)
			<-started
			stop()
			row := ops.row(id)
			if row.status != domain.OperationStatusPending || !row.job.RunAt.Equal(clock.Now()) {
				t.Fatalf("after shutdown: status %s run_at %v, want pending now", row.status, row.job.RunAt)
			}
		})
	}
}

// A handler's ReportResult reaches Complete; a delete_bank whose row went with its bank is done.
func TestRunnerReportsResult(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	r := testRunner(ops, clock, RunnerConfig{})
	r.Handle(domain.OperationKindRetainDocument, func(ctx context.Context, _ port.Job) error {
		ReportResult(ctx, json.RawMessage(`{"units":3}`), port.Usage{Slot: domain.ModelSlotExtract, InputTokens: 10})
		ReportResult(ctx, nil, port.Usage{Slot: domain.ModelSlotExtract, OutputTokens: 5})
		return nil
	})
	id := ops.add(retainJob("1"))
	startRunner(t, r)
	eventually(t, "completed", func() bool { return ops.row(id).status == domain.OperationStatusCompleted })
	row := ops.row(id)
	if string(row.result) != `{"units":3}` || len(row.usage) != 2 {
		t.Errorf("result %s usage %+v", row.result, row.usage)
	}
	ReportResult(context.Background(), json.RawMessage(`{}`)) // outside a job: a no-op
}

func TestRegisterStubs(t *testing.T) {
	clock := newClock()
	ops := newOps(clock)
	r := testRunner(ops, clock, RunnerConfig{})
	Register(r, Handlers{})
	kinds := []port.JobKind{
		domain.OperationKindRetainDocument, domain.OperationKindConsolidate,
		domain.OperationKindRefreshMentalModel, domain.OperationKindDeleteBank,
		domain.OperationKindRederiveMemory, domain.OperationKindRetagDocument,
		domain.OperationKindGraphMaintenance,
	}
	for _, k := range kinds {
		if r.handlers[k] == nil {
			t.Errorf("no handler for %s", k)
		}
		if err := r.handlers[k](context.Background(), port.Job{Kind: k}); !errors.Is(err, ErrPermanent) {
			t.Errorf("%s stub: %v, want ErrPermanent", k, err)
		}
	}
	if r.handlers[domain.OperationKindRetainBatch] != nil {
		t.Error("retain_batch must not be registered")
	}
	if err := NewRunner(ops, RunnerConfig{}).Run(context.Background()); err == nil {
		t.Error("Run with no handlers must fail")
	}
}
