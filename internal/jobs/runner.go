package jobs

import (
	"context"
	"errors"
	"log/slog"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Runner defaults (docs/design/architecture.md#runner).
const (
	// DefaultSlots is how many jobs a worker runs at once.
	DefaultSlots = 10
	// DefaultReservedConsolidate is how many of the slots only consolidate jobs may use.
	DefaultReservedConsolidate = 2
	// DefaultPollInterval is how often the runner claims due jobs.
	DefaultPollInterval = 500 * time.Millisecond
	// DefaultHeartbeatInterval is how often the runner heartbeats its running jobs.
	DefaultHeartbeatInterval = 15 * time.Second
	// DefaultShutdownGrace is how long running handlers may finish on their own after Run's context ends.
	// A shutdown can then take up to grace + settleTimeout (50 s by default), so the k8s pod's
	// terminationGracePeriodSeconds must be at least that (60 s recommended).
	DefaultShutdownGrace = 20 * time.Second
	// settleTimeout bounds the terminal Complete or Fail of a job (with its retries), which runs even
	// during shutdown.
	settleTimeout = 30 * time.Second
	// claimTimeout bounds one Claim call.
	claimTimeout = 10 * time.Second
	// DefaultStaleAfter is how old a PROCESSING row's heartbeat must be before the maintenance tick reclaims it.
	DefaultStaleAfter = 2 * time.Minute
)

// Handler runs one job kind. It reads its payload from run.Job, reports progress and its result through run,
// and returns ErrPermanent to fail with no retry; any other error retries.
type Handler func(ctx context.Context, run *Run) error

// Job outcomes passed to a TraceFunc's finish func.
const (
	// OutcomeCompleted: the handler succeeded and the runner completed the row.
	OutcomeCompleted = "completed"
	// OutcomeRetry: the handler failed and the row was rescheduled.
	OutcomeRetry = "retry"
	// OutcomeFailed: the handler failed for good (permanent error or out of attempts).
	OutcomeFailed = "failed"
	// OutcomeReleased: the worker shut down mid-run and released the row for another worker.
	OutcomeReleased = "released"
	// OutcomeLost: the worker lost the job (cancel requested, reclaimed or not heartbeated in time).
	OutcomeLost = "lost"
)

// TraceFunc starts a trace span around one run of j and returns ctx carrying it (the handler and the
// terminal Complete/Fail run under it) and a finish func the runner calls exactly once, after the row is
// settled, with one of the Outcome* values and the handler's error (nil on success). It is the runner's
// only tracing and metrics hook; internal/observability provides a Datadog one, so this package stays
// vendor-free.
type TraceFunc func(ctx context.Context, j store.Job) (context.Context, func(outcome string, err error))

// Config tunes a Runner; zero fields take the defaults.
type Config struct {
	// WorkerID names this worker on the rows it claims; empty = "worker-<uuid>".
	WorkerID string
	// Clock stamps retry times and heartbeat ages; nil = the system clock.
	Clock clock.Clock
	// Slots is how many jobs run at once (default DefaultSlots).
	Slots int
	// ReservedConsolidate slots only consolidate may use (default DefaultReservedConsolidate; capped at
	// Slots-1).
	ReservedConsolidate int
	// PollInterval is the claim period (default DefaultPollInterval). A finished job also triggers a poll.
	PollInterval time.Duration
	// HeartbeatInterval is the heartbeat period and per-call timeout (default DefaultHeartbeatInterval).
	HeartbeatInterval time.Duration
	// StaleAfter is the reclaim threshold the maintenance tick uses (default DefaultStaleAfter). A job whose
	// last successful heartbeat is older than 3/4 of it is treated as lost: the runner stops its handler
	// before another worker can reclaim and re-run it.
	StaleAfter time.Duration
	// ShutdownGrace is how long handlers may finish after Run's context ends before they are cancelled
	// (default DefaultShutdownGrace; negative = cancel at once).
	ShutdownGrace time.Duration
	// DB lets a failed automatic consolidate skip its retry when another automatic consolidate is already
	// due for the bank (a probe transaction that is always rolled back); nil = always retry.
	DB store.DB
	// Logger receives runner logs; nil = slog.Default().
	Logger *slog.Logger
	// TraceJob, when set, wraps every job run in a trace span and records its metrics (Datadog when
	// MEMD_DATADOG_ENABLED); nil = no tracing and no extra work.
	TraceJob TraceFunc
}

// Runner is the worker-mode job runner: it claims due jobs through a Claimer, runs each in a slot,
// heartbeats them and completes, retries or fails them. Construct it with New.
type Runner struct {
	c          Claimer
	cfg        Config
	pending    pendingChecker // nil = a failed consolidate always keeps its retry
	log        *slog.Logger
	handlers   map[domain.OperationKind]Handler
	otherKinds []domain.OperationKind // registered kinds except consolidate, sorted

	mu      sync.Mutex
	running map[domain.OperationID]*active
	wake    chan struct{}
	wg      sync.WaitGroup
}

// active is one run of a job. Its pointer is the run's token: a re-claim of the same id is a new run.
type active struct {
	kind     domain.OperationKind
	cancel   context.CancelCauseFunc
	lastBeat time.Time // claim time, then the last heartbeat that covered this run
}

var (
	// errLost cancels a handler whose job the worker no longer holds (cancel requested, reclaimed, or not
	// heartbeated for too long).
	errLost = errors.New("worker lost the job: cancel requested or reclaimed")
	// errShutdown cancels the running handlers when the shutdown grace runs out.
	errShutdown = errors.New("worker shutting down")
)

// New returns a Runner claiming through c. Slots default to 10, 2 of them reserved for consolidate.
func New(c Claimer, cfg Config) *Runner {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "worker-" + uuid.NewString()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System{}
	}
	if cfg.Slots <= 0 {
		cfg.Slots = DefaultSlots
	}
	if cfg.ReservedConsolidate <= 0 {
		cfg.ReservedConsolidate = DefaultReservedConsolidate
	}
	cfg.ReservedConsolidate = min(cfg.ReservedConsolidate, cfg.Slots-1)
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = DefaultStaleAfter
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = DefaultShutdownGrace
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	var pending pendingChecker
	if cfg.DB != nil {
		pending = &consolidateProbe{s: cfg.DB, c: cfg.Clock}
	}
	return &Runner{
		c: c, cfg: cfg, pending: pending, log: log.With("worker", cfg.WorkerID),
		handlers: map[domain.OperationKind]Handler{},
		running:  map[domain.OperationID]*active{},
		wake:     make(chan struct{}, 1),
	}
}

// Handle registers h for kind, replacing any earlier handler. Only registered kinds are claimed. Register
// every handler before Run.
func (r *Runner) Handle(kind domain.OperationKind, h Handler) {
	r.handlers[kind] = h
	r.otherKinds = r.otherKinds[:0]
	for k := range r.handlers {
		if k != domain.OperationKindConsolidate {
			r.otherKinds = append(r.otherKinds, k)
		}
	}
	sort.Slice(r.otherKinds, func(i, j int) bool { return r.otherKinds[i] < r.otherKinds[j] })
}

// Run polls every PollInterval, dispatches claimed jobs to their handlers and heartbeats every
// HeartbeatInterval until ctx is done. It then stops claiming, lets the running handlers finish within the
// shutdown grace, cancels the rest, waits for them, settles their rows and returns nil; heartbeats continue
// until the last handler has returned. Call it once.
func (r *Runner) Run(ctx context.Context) error {
	if len(r.handlers) == 0 {
		return errors.New("jobs: no handlers registered")
	}
	base := context.WithoutCancel(ctx)
	hbCtx, stopHB := context.WithCancel(base)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		r.heartbeatLoop(hbCtx)
	}()
	r.pollLoop(ctx, base)
	r.drain()
	stopHB()
	<-hbDone
	return nil
}

// drain waits up to ShutdownGrace for the running handlers to finish, then cancels the rest and waits
// for them. Nothing is claimed any more.
func (r *Runner) drain() {
	idle := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(idle)
	}()
	if r.cfg.ShutdownGrace > 0 {
		t := time.NewTimer(r.cfg.ShutdownGrace)
		defer t.Stop()
		select {
		case <-idle:
			return
		case <-t.C:
		}
	}
	r.cancelAll(errShutdown)
	<-idle
}

// pollLoop claims until ctx is done; base outlives ctx and parents the handlers.
func (r *Runner) pollLoop(ctx, base context.Context) {
	t := time.NewTicker(r.cfg.PollInterval)
	defer t.Stop()
	for {
		r.poll(ctx, base)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// poll fills free slots: consolidate first (it may use any slot), then the other kinds up to
// Slots-ReservedConsolidate running non-consolidate jobs.
func (r *Runner) poll(ctx, base context.Context) {
	free, otherFree := r.capacity()
	if free > 0 && r.handlers[domain.OperationKindConsolidate] != nil {
		free -= r.claim(ctx, base, []domain.OperationKind{domain.OperationKindConsolidate}, free)
	}
	if n := min(free, otherFree); n > 0 && len(r.otherKinds) > 0 {
		r.claim(ctx, base, r.otherKinds, n)
	}
}

// claim claims up to n jobs of kinds and starts them; it returns how many it started. The Claim call runs
// under base (with a timeout), not ctx, so a claim that commits as shutdown begins still starts its jobs.
func (r *Runner) claim(ctx, base context.Context, kinds []domain.OperationKind, n int) int {
	if ctx.Err() != nil {
		return 0
	}
	cctx, cancel := context.WithTimeout(base, claimTimeout)
	defer cancel()
	jobs, err := r.c.Claim(cctx, r.cfg.WorkerID, kinds, n)
	if err != nil {
		r.log.Warn("claim failed", "kinds", kinds, "err", err)
		return 0
	}
	for _, j := range jobs {
		r.start(base, j)
	}
	return len(jobs)
}

// capacity returns the free slots overall and for non-consolidate kinds.
func (r *Runner) capacity() (free, otherFree int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	other := 0
	for _, a := range r.running {
		if a.kind != domain.OperationKindConsolidate {
			other++
		}
	}
	return r.cfg.Slots - len(r.running), r.cfg.Slots - r.cfg.ReservedConsolidate - other
}

// start runs j in its own goroutine under a context that only the runner cancels. If this runner still
// runs an older run of the same id (its job was reclaimed and re-claimed here), that run is cancelled as
// lost; its settle then finds the row alive under the new run and writes nothing.
func (r *Runner) start(base context.Context, j store.Job) {
	hctx, cancel := context.WithCancelCause(base)
	a := &active{kind: j.Kind, cancel: cancel, lastBeat: r.cfg.Clock.Now()}
	r.mu.Lock()
	if old := r.running[j.ID]; old != nil {
		r.log.Warn("job re-claimed while an older run is still going; stopping the older run", "op", j.ID)
		old.cancel(errLost)
	}
	r.running[j.ID] = a
	r.mu.Unlock()
	r.wg.Add(1)
	go r.execute(hctx, base, j, a)
}

// release forgets run a of job id (unless a newer run replaced it) and wakes the poll loop.
func (r *Runner) release(id domain.OperationID, a *active) {
	r.mu.Lock()
	if r.running[id] == a {
		delete(r.running, id)
	}
	r.mu.Unlock()
	a.cancel(nil)
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// cancelAll cancels every running handler with cause.
func (r *Runner) cancelAll(cause error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.running {
		a.cancel(cause)
	}
}
