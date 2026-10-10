package worker

import (
	"context"
	"errors"
	"log/slog"
	"memd/internal/domain"
	"memd/internal/port"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Runner defaults (SPEC "Jobs").
const (
	// DefaultSlots is how many jobs a worker runs at once.
	DefaultSlots = 10
	// DefaultReservedConsolidate is how many of the slots only consolidate jobs may use.
	DefaultReservedConsolidate = 2
	// DefaultPollInterval is how often the runner claims due jobs.
	DefaultPollInterval = 500 * time.Millisecond
	// DefaultHeartbeatInterval is how often the runner heartbeats its running jobs.
	DefaultHeartbeatInterval = 15 * time.Second
	// settleTimeout bounds the terminal Complete or Fail of a job, which runs even during shutdown.
	settleTimeout = 30 * time.Second
)

// Handler runs one job kind. Return ErrRetryAt to reschedule, ErrPermanent to fail with no retry.
type Handler func(ctx context.Context, j port.Job) error

// JobRunner claims due jobs from the operations table and runs them.
type JobRunner interface {
	// Handle registers h for kind, replacing any earlier handler. Only registered kinds are claimed.
	// Register every handler before Run.
	Handle(kind port.JobKind, h Handler)
	// Run polls, dispatches and heartbeats until ctx is done. It then stops claiming, cancels the running
	// handlers, waits for them, settles their rows and returns nil. Call it once.
	Run(ctx context.Context) error
}

// RunnerConfig tunes a Runner; zero fields take the defaults.
type RunnerConfig struct {
	// WorkerID names this worker on the rows it claims; empty = "worker-<uuid>".
	WorkerID string
	// Clock stamps retry times; nil = the system clock.
	Clock port.Clock
	// Slots is how many jobs run at once (default DefaultSlots).
	Slots int
	// ReservedConsolidate slots only consolidate may use (default DefaultReservedConsolidate; capped at
	// Slots-1).
	ReservedConsolidate int
	// PollInterval is the claim period (default DefaultPollInterval). A finished job also triggers a poll.
	PollInterval time.Duration
	// HeartbeatInterval is the heartbeat period (default DefaultHeartbeatInterval).
	HeartbeatInterval time.Duration
	// Pending lets a failed consolidate skip its retry when another consolidate is pending for the bank;
	// nil = always retry.
	Pending PendingChecker
	// Logger receives runner logs; nil = slog.Default().
	Logger *slog.Logger
}

// Runner is the worker-mode job runner: it claims due jobs through port.Claimer, runs each in a slot,
// heartbeats them and completes, retries or fails them. Construct it with NewRunner.
type Runner struct {
	c          port.Claimer
	cfg        RunnerConfig
	log        *slog.Logger
	handlers   map[port.JobKind]Handler
	otherKinds []port.JobKind // registered kinds except consolidate, sorted

	mu      sync.Mutex
	running map[port.OperationID]*active
	wake    chan struct{}
	wg      sync.WaitGroup
}

// active is one running job.
type active struct {
	kind   port.JobKind
	cancel context.CancelCauseFunc
}

var _ JobRunner = (*Runner)(nil)

var (
	// errLost cancels a handler whose job the worker no longer holds (cancel requested or reclaimed).
	errLost = errors.New("worker lost the job: cancel requested or reclaimed")
	// errShutdown cancels the running handlers when Run's context is done.
	errShutdown = errors.New("worker shutting down")
)

// NewRunner returns a Runner claiming through c. Slots default to 10, 2 of them reserved for consolidate.
func NewRunner(c port.Claimer, cfg RunnerConfig) *Runner {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "worker-" + uuid.NewString()
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
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
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		c: c, cfg: cfg, log: log.With("worker", cfg.WorkerID),
		handlers: map[port.JobKind]Handler{},
		running:  map[port.OperationID]*active{},
		wake:     make(chan struct{}, 1),
	}
}

// Handle registers h for kind.
func (r *Runner) Handle(kind port.JobKind, h Handler) {
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
// HeartbeatInterval. Heartbeats continue after ctx is done until the last handler has returned.
func (r *Runner) Run(ctx context.Context) error {
	if len(r.handlers) == 0 {
		return errors.New("worker: no handlers registered")
	}
	base := context.WithoutCancel(ctx)
	hbCtx, stopHB := context.WithCancel(base)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		r.heartbeatLoop(hbCtx)
	}()
	r.pollLoop(ctx, base)
	r.cancelAll(errShutdown)
	r.wg.Wait()
	stopHB()
	<-hbDone
	return nil
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
	if ctx.Err() != nil {
		return
	}
	free, otherFree := r.capacity()
	if free > 0 && r.handlers[domain.OperationKindConsolidate] != nil {
		free -= r.claim(ctx, base, []port.JobKind{domain.OperationKindConsolidate}, free)
	}
	if n := min(free, otherFree); n > 0 && len(r.otherKinds) > 0 {
		r.claim(ctx, base, r.otherKinds, n)
	}
}

// claim claims up to n jobs of kinds and starts them; it returns how many it started.
func (r *Runner) claim(ctx, base context.Context, kinds []port.JobKind, n int) int {
	jobs, err := r.c.Claim(ctx, r.cfg.WorkerID, kinds, n)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("claim failed", "kinds", kinds, "err", err)
		}
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

// start runs j in its own goroutine under a context that only the runner cancels.
func (r *Runner) start(base context.Context, j port.Job) {
	hctx, cancel := context.WithCancelCause(base)
	r.mu.Lock()
	r.running[j.ID] = &active{kind: j.Kind, cancel: cancel}
	r.mu.Unlock()
	r.wg.Add(1)
	go r.execute(hctx, base, j)
}

// release forgets a settled job and wakes the poll loop to refill its slot.
func (r *Runner) release(id port.OperationID) {
	r.mu.Lock()
	a := r.running[id]
	delete(r.running, id)
	r.mu.Unlock()
	if a != nil {
		a.cancel(nil)
	}
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

// systemClock is the default Clock.
type systemClock struct{}

// Now returns the wall-clock time.
func (systemClock) Now() time.Time { return time.Now() }
