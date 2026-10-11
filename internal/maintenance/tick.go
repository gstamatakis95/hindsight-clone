package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/memory"
	"memd/internal/store"
	"time"
)

// Defaults of the operator settings (SPEC "Jobs": 60 s tick, 7-day operations, 30-day events).
const (
	// DefaultInterval is the maintenance tick period.
	DefaultInterval = 60 * time.Second
	// DefaultOperationRetention is how long terminal operations are kept before pruning.
	DefaultOperationRetention = 7 * 24 * time.Hour
	// DefaultEventRetention is how long lifecycle events are kept before pruning.
	DefaultEventRetention = 30 * 24 * time.Hour
	// DefaultConsolidationGrace is how old an unconsolidated unit must be before the tick consolidates its
	// bank when its config sets no observations.reconcile_grace_seconds, so skip_consolidation followed by a
	// manual Consolidate is not pre-empted (decision 2000).
	DefaultConsolidationGrace = 10 * time.Minute
	// DefaultGraphBudget bounds one graph_maintenance run (SPEC "Jobs": 240 s); leftover work re-enqueues the
	// job.
	DefaultGraphBudget = 240 * time.Second
)

// TraceFunc starts a trace span around one maintenance tick and returns ctx carrying it and a finish func
// called once with the tick's joined error (nil when every step succeeded).
type TraceFunc func(ctx context.Context) (context.Context, func(err error))

// Config holds the operator settings of the tick and the graph job; zero fields take the defaults.
type Config struct {
	// Interval is the tick period (default DefaultInterval).
	Interval time.Duration
	// StaleAfter is the reclaim heartbeat age (default jobs.DefaultStaleAfter). It must be the runner's
	// StaleAfter, so the runner's self-fence (3/4 of it) fires before a reclaim.
	StaleAfter time.Duration
	// OperationRetention keeps terminal operations this long (default DefaultOperationRetention). Keep it
	// far above Interval so the refresh-failure reconcile sees every FAILED refresh before it is pruned.
	OperationRetention time.Duration
	// EventRetention keeps lifecycle events this long (default DefaultEventRetention).
	EventRetention time.Duration
	// GraphBudget bounds one graph_maintenance run (default DefaultGraphBudget).
	GraphBudget time.Duration
}

// withDefaults fills zero fields.
func (c Config) withDefaults() Config {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&c.Interval, DefaultInterval)
	def(&c.StaleAfter, jobs.DefaultStaleAfter)
	def(&c.OperationRetention, DefaultOperationRetention)
	def(&c.EventRetention, DefaultEventRetention)
	def(&c.GraphBudget, DefaultGraphBudget)
	return c
}

// Linker adds the links units lack, inside the caller's transaction; linking.Linker satisfies it.
type Linker interface {
	// Link tops up the temporal and semantic links of units (and adds causes' causal links) within the per-unit
	// caps.
	Link(ctx context.Context, tx store.Tx, b domain.BankRef, units []domain.Unit, causes linking.CauseMap) error
}

// Deps are the Service's dependencies. Claimer and Store are required by Run, Store and Linker by
// RunGraphJob; every other dependency is optional and a nil one skips its tick step.
type Deps struct {
	// Claimer reclaims stale jobs.
	Claimer jobs.Claimer
	// Store enqueues, prunes, and runs the graph job's batches.
	Store store.DB
	// Clock dates the cut-offs, cron checks and the graph budget; nil = the system clock.
	Clock clock.Clock
	// Resolver resolves each consolidation candidate's effective config: its reconcile grace, and banks with
	// observations.auto_consolidation or observations.enabled false are skipped. nil = no filtering and the
	// default grace.
	Resolver *config.Resolver
	// Consolidation lists banks for the consolidation reconcile (UnconsolidatedBanks).
	Consolidation store.Maintenance
	// Deletes lists tombstoned banks whose delete_bank stopped (StalledBankDeletes).
	Deletes store.Maintenance
	// Graph lists banks with graph maintenance queued (BanksWithGraphWork).
	Graph store.Maintenance
	// Memory re-queues the rederives of stranded facts and settles finished retain_batch parents.
	Memory *memory.Service
	// CronRefresh enqueues mental-model refreshes whose refresh_cron fired.
	CronRefresh mentalmodel.Triggers
	// RefreshFailures pauses models whose latest refresh failed.
	RefreshFailures mentalmodel.RefreshFailureReconciler
	// Linker tops up the links of graph_dirty units in the graph job (linking.Linker).
	Linker Linker
	// Config holds the operator settings.
	Config Config
	// Logger receives maintenance logs; nil = slog.Default().
	Logger *slog.Logger
	// TraceTick, when set, wraps every tick in a trace span (Datadog when MEMD_DATADOG_ENABLED), so the
	// steps' Postgres spans nest under it; nil = no tracing.
	TraceTick TraceFunc
}

// Service runs the maintenance tick and the graph_maintenance job.
type Service struct {
	d Deps
}

// New returns the Service, filling the optional settings with their defaults.
func New(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	d.Config = d.Config.withDefaults()
	return &Service{d: d}
}

// Run ticks every Interval in every worker: reclaim stale jobs, resume stalled bank deletes, cron
// refreshes, consolidation and graph-maintenance reconciles, re-queue rederives of stranded facts, settle
// retain_batch parents, prune terminal ops and events. No leader: each step is idempotent. The delete resume
// and the refresh-failure reconcile run right after reclaim, so they see jobs reclaim failed in the same
// tick. It ticks once at start and returns nil when ctx is done; a failing step is logged and never stops
// the others.
func (s *Service) Run(ctx context.Context) error {
	if s.d.Claimer == nil || s.d.Store == nil {
		return errors.New("maintenance: the tick needs a Claimer and a Store")
	}
	t := time.NewTicker(s.d.Config.Interval)
	defer t.Stop()
	for {
		s.tickTraced(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// tickTraced runs one tick, inside TraceTick's span when set, and logs its errors.
func (s *Service) tickTraced(ctx context.Context) {
	tctx, finish := ctx, func(error) {}
	if s.d.TraceTick != nil {
		tctx, finish = s.d.TraceTick(ctx)
	}
	err := s.tick(tctx)
	finish(err)
	if err != nil && ctx.Err() == nil {
		s.d.Logger.WarnContext(tctx, "maintenance tick had errors", "err", err)
	}
}

// step is one idempotent step of the tick; it returns how many rows it changed.
type step struct {
	name string
	run  func(s *Service, ctx context.Context) (int, error)
}

// steps run in this order on every tick.
var steps = []step{
	{"reclaim_stale", (*Service).reclaimStale},
	{"resume_bank_deletes", (*Service).resumeBankDeletes},
	{"refresh_failures", (*Service).reconcileRefreshFailures},
	{"cron_refresh", (*Service).enqueueCronRefreshes},
	{"consolidation_reconcile", (*Service).reconcileConsolidation},
	{"graph_reconcile", (*Service).reconcileGraph},
	{"rederive_stranded", (*Service).rederiveStranded},
	{"settle_retain_batches", (*Service).settleBatches},
	{"prune_operations", (*Service).pruneOperations},
	{"prune_events", (*Service).pruneEvents},
}

// rederiveStranded re-enqueues rederive_memory for facts a failed rederive left without an embedding.
func (s *Service) rederiveStranded(ctx context.Context) (int, error) {
	if s.d.Memory == nil {
		return 0, nil
	}
	return s.d.Memory.RederiveStranded(ctx) //nolint:wrapcheck // the sweep wraps its own errors
}

// settleBatches ends retain_batch parents whose children are all terminal.
func (s *Service) settleBatches(ctx context.Context) (int, error) {
	if s.d.Memory == nil {
		return 0, nil
	}
	return s.d.Memory.SettleRetainBatches(ctx) //nolint:wrapcheck // the sweep wraps its own errors
}

// tick runs every step once and joins their errors.

func (s *Service) tick(ctx context.Context) error {
	var errs []error
	for _, st := range steps {
		if ctx.Err() != nil {
			break
		}
		n, err := st.run(s, ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", st.name, err))
			continue
		}
		if n > 0 {
			s.d.Logger.InfoContext(ctx, "maintenance", "step", st.name, "changed", n)
		}
	}
	return errors.Join(errs...)
}

// reclaimStale returns stale PROCESSING jobs to PENDING (or fails them past their max attempts).
func (s *Service) reclaimStale(ctx context.Context) (int, error) {
	return s.d.Claimer.ReclaimStale(ctx, s.d.Config.StaleAfter)
}

// reconcileRefreshFailures pauses models whose latest terminal refresh failed.
func (s *Service) reconcileRefreshFailures(ctx context.Context) (int, error) {
	if s.d.RefreshFailures == nil {
		return 0, nil
	}
	n, err := s.d.RefreshFailures.ReconcileFailedRefreshes(ctx)
	if err != nil {
		return n, fmt.Errorf("reconcile failed refreshes: %w", err)
	}
	return n, nil
}

// enqueueCronRefreshes enqueues refreshes whose cron schedule fired.
func (s *Service) enqueueCronRefreshes(ctx context.Context) (int, error) {
	if s.d.CronRefresh == nil {
		return 0, nil
	}
	n, err := s.d.CronRefresh.EnqueueDueCronRefreshes(ctx, s.d.Clock.Now())
	if err != nil {
		return n, fmt.Errorf("cron refreshes: %w", err)
	}
	return n, nil
}

// reconcileConsolidation enqueues an automatic consolidate for every bank with a unit left unconsolidated for
// longer than the bank's reconcile grace, unless the bank's effective config turns automatic consolidation or
// observations off (decision 2000). One short transaction per bank. jobs.EnqueueAutoConsolidate joins the
// bank's queued or running automatic consolidate (a running one re-enqueues itself while units remain), so
// concurrent ticks insert at most one, and a tick never queues a job that would make a failing running
// consolidate skip its retry. A bank being deleted (or already gone) is skipped; other errors don't stop the
// remaining banks. It returns how many jobs it inserted.
func (s *Service) reconcileConsolidation(ctx context.Context) (int, error) {
	if s.d.Consolidation == nil {
		return 0, nil
	}
	now := s.d.Clock.Now()
	banks, err := s.d.Consolidation.UnconsolidatedBanks(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list banks to consolidate: %w", err)
	}
	var keep []domain.BankRef
	var errs []error
	for _, b := range banks {
		ok, err := autoConsolidates(ctx, s.d.Resolver, b, now)
		switch {
		case gone(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: config: %w", b.Bank.Tenant, b.Bank.Bank, err))
		case ok:
			keep = append(keep, b.Bank)
		}
	}
	n, err := s.enqueueEach(ctx, keep, jobs.EnqueueAutoConsolidate)
	return n, errors.Join(append(errs, err)...)
}

// autoConsolidates reports whether the tick consolidates b now: its effective observations.auto_consolidation
// and observations.enabled are both unset or true, and its oldest waiting fact is older than its
// observations.reconcile_grace_seconds (unset = DefaultConsolidationGrace). A nil resolver applies the default
// grace to every bank.
func autoConsolidates(
	ctx context.Context, r *config.Resolver, b store.UnconsolidatedBank, now time.Time,
) (bool, error) {
	grace := DefaultConsolidationGrace
	if r != nil {
		cfg, err := r.Effective(ctx, b.Bank)
		if err != nil {
			return false, fmt.Errorf("effective config: %w", err)
		}
		obs := cfg.GetObservations()
		on := func(p *bool) bool { return p == nil || *p }
		if !on(obs.AutoConsolidation) || !on(obs.Enabled) {
			return false, nil
		}
		if g := obs.ReconcileGraceSeconds; g != nil {
			grace = time.Duration(*g) * time.Second
		}
	}
	return b.Oldest.Before(now.Add(-grace)), nil
}

// resumeBankDeletes re-enqueues delete_bank for every tombstoned bank whose delete stopped (decision 2001):
// ReclaimStale and the shutdown release charge attempts and fail delete_bank at its cap with no
// continuation, which would leave the bank tombstoned forever. jobs.EnqueueDeleteBank joins a delete_bank
// another tick inserted meanwhile; a bank whose delete just finished is skipped.
func (s *Service) resumeBankDeletes(ctx context.Context) (int, error) {
	if s.d.Deletes == nil {
		return 0, nil
	}
	banks, err := s.d.Deletes.StalledBankDeletes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list stalled bank deletes: %w", err)
	}
	return s.enqueueEach(ctx, banks, jobs.EnqueueDeleteBank)
}

// reconcileGraph enqueues graph_maintenance for every bank with graph_dirty or entity_maintenance rows
// (decision 2002). A removal that commits while the bank's graph_maintenance is finishing joins that running
// row, so without this step its rows would wait for the next removal.
func (s *Service) reconcileGraph(ctx context.Context) (int, error) {
	if s.d.Graph == nil {
		return 0, nil
	}
	banks, err := s.d.Graph.BanksWithGraphWork(ctx)
	if err != nil {
		return 0, fmt.Errorf("list banks with graph work: %w", err)
	}
	return s.enqueueEach(ctx, banks, jobs.EnqueueGraph)
}

// gone reports an error meaning the bank is being deleted or no longer exists.
func gone(err error) bool {
	return errors.Is(err, domain.ErrBankDeleting) || errors.Is(err, domain.ErrNotFound)
}

// enqueueFunc is a catalogue helper enqueueing a bank's automatic job.
type enqueueFunc func(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.OperationID, bool, error)

// enqueueEach enqueues (or joins) a bank's automatic job with enqueue for every bank, one short transaction
// each, and returns how many it inserted. It takes no change seq: nothing here is memory content, and Seq
// refuses a tombstoned bank, which the delete resume must still reach. A bank being deleted (or gone) is
// skipped; other errors don't stop the rest.
func (s *Service) enqueueEach(ctx context.Context, banks []domain.BankRef, enqueue enqueueFunc) (int, error) {
	inserted := 0
	var errs []error
	for _, b := range banks {
		var joined bool
		err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
			var err error
			_, joined, err = enqueue(ctx, tx, b)
			return err
		})
		switch {
		case gone(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: %w", b.Tenant, b.Bank, err))
		case !joined:
			inserted++
		}
	}
	return inserted, errors.Join(errs...)
}

// pruneOperations deletes terminal operations that finished more than OperationRetention before now and
// returns how many; pending and processing operations are never pruned.
func (s *Service) pruneOperations(ctx context.Context) (int, error) {
	before := s.d.Clock.Now().Add(-s.d.Config.OperationRetention)
	n := 0
	err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
		var err error
		n, err = tx.Queue().Prune(ctx, before)
		return err
	})
	return n, err
}

// pruneEvents prunes lifecycle events past their retention.
func (s *Service) pruneEvents(ctx context.Context) (int, error) {
	before := s.d.Clock.Now().Add(-s.d.Config.EventRetention)
	n := 0
	err := s.d.Store.InTx(ctx, func(tx store.Tx) error {
		var err error
		n, err = tx.Events().Prune(ctx, before)
		return err
	})
	return n, err
}
