package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/port"
	"time"
)

// Maintenance defaults (SPEC "Jobs": 60 s tick, 2 min heartbeat staleness).
const (
	// DefaultMaintenanceInterval is the maintenance tick period.
	DefaultMaintenanceInterval = 60 * time.Second
	// DefaultStaleAfter is how old a PROCESSING row's heartbeat must be before it is reclaimed.
	DefaultStaleAfter = 2 * time.Minute
	// DefaultOperationRetention is how long terminal operations are kept before pruning.
	DefaultOperationRetention = 7 * 24 * time.Hour
	// DefaultEventRetention is how long lifecycle events are kept before pruning.
	DefaultEventRetention = 30 * 24 * time.Hour
	// DefaultConsolidationGrace is how old an unconsolidated unit must be before the tick consolidates its
	// bank, so skip_consolidation followed by a manual Consolidate is not pre-empted (decision 2000).
	DefaultConsolidationGrace = 10 * time.Minute
	// consolidateSerialKey is the consolidate job's serial key: one per bank.
	consolidateSerialKey = "consolidate"
)

// ConsolidationCandidates finds banks the consolidation reconcile must look at. It is a read the ports
// don't expose; the Postgres adapter implements it (decision 2000).
type ConsolidationCandidates interface {
	// UnconsolidatedBanks returns every bank that is not tombstoned and has at least one live fact with an
	// embedding, consolidated_at and consolidation_failed_at unset, created before before.
	UnconsolidatedBanks(ctx context.Context, before time.Time) ([]port.BankRef, error)
}

// StalledDeleteSource finds tombstoned banks whose delete stopped: no delete_bank operation is pending or
// processing (ReclaimStale or a shutdown release failed it at the attempt cap, or it failed for good). The
// Postgres adapter implements it (decision 2001).
type StalledDeleteSource interface {
	// StalledBankDeletes returns every tombstoned bank with no pending or processing delete_bank operation.
	StalledBankDeletes(ctx context.Context) ([]port.BankRef, error)
}

// GraphWorkSource finds banks with graph maintenance queued; the Postgres adapter implements it (decision
// 2002).
type GraphWorkSource interface {
	// BanksWithGraphWork returns every bank that is not tombstoned and has graph_dirty or entity_maintenance
	// rows.
	BanksWithGraphWork(ctx context.Context) ([]port.BankRef, error)
}

// CronRefresher enqueues the refreshes of mental models whose refresh_cron schedule has fired; the mental
// model engine (WP-12) implements it.
type CronRefresher interface {
	// EnqueueDueCronRefreshes enqueues one refresh_mental_model job per model with a refresh_cron whose
	// latest fire time at or before now has not been served yet, skipping models with auto_refresh_paused
	// and tombstoned banks, and returns how many it inserted (joins excluded). It must be idempotent under
	// concurrent callers (every worker ticks, no leader): refresh dedupe joins only a PENDING row, so
	// due-ness must be keyed on the fire time (for example: no refresh operation of the model created at or
	// after that fire time), not on "is a refresh queued", or a refresh that is already running gets
	// enqueued again on the next tick.
	EnqueueDueCronRefreshes(ctx context.Context, now time.Time) (int, error)
}

// RefreshFailureReconciler turns failed refreshes into paused models (SPEC "Paused by failure"; decision
// 012, R10'); the mental model engine (WP-12) implements it.
type RefreshFailureReconciler interface {
	// ReconcileFailedRefreshes looks, per model, at the latest terminal refresh_mental_model operation.
	// When it is FAILED and newer than the model's last successful refresh, and not yet recorded, it sets
	// auto_refresh_paused and last_refresh_failed_at and appends one REFRESH_FAILED history entry (with its
	// event). Keying on the latest terminal operation, not the latest operation, keeps a quickly
	// re-enqueued refresh from hiding the failure. Idempotent: a failure already recorded is skipped, so
	// concurrent and repeated ticks record it once. It returns how many models it paused.
	ReconcileFailedRefreshes(ctx context.Context) (int, error)
}

// MaintenanceDeps are RunMaintenance's dependencies; zero durations take the defaults.
type MaintenanceDeps struct {
	// Claimer reclaims stale jobs.
	Claimer port.Claimer
	// Store enqueues consolidation and prunes operations and events.
	Store port.Store
	// Clock dates the prune cut-offs and cron checks; nil = the system clock.
	Clock port.Clock
	// Consolidation lists banks for the consolidation reconcile; nil skips the step.
	Consolidation ConsolidationCandidates
	// Config resolves each candidate bank's effective config: banks with auto_consolidation or
	// enable_observations false are skipped. nil = no filtering.
	Config port.ConfigResolver
	// ConsolidationGrace is how old an unconsolidated unit must be before the reconcile consolidates its bank
	// (default DefaultConsolidationGrace).
	ConsolidationGrace time.Duration
	// Deletes lists tombstoned banks whose delete_bank stopped; nil skips the step.
	Deletes StalledDeleteSource
	// Graph lists banks with graph maintenance queued; nil skips the step.
	Graph GraphWorkSource
	// CronRefresh enqueues cron refreshes; nil skips the step.
	CronRefresh CronRefresher
	// RefreshFailures pauses models whose refresh failed; nil skips the step.
	RefreshFailures RefreshFailureReconciler
	// Batches settles retain_batch parents whose children finished; nil skips the step.
	Batches BatchSettler
	// Stranded finds facts with a NULL embedding left by a failed rederive; nil skips the step.
	Stranded StrandedFactSource
	// StrandedRetryAfter is how long after a unit's last rederive ended the step may queue another one
	// (default DefaultStrandedRetryAfter).
	StrandedRetryAfter time.Duration
	// Interval is the tick period (default DefaultMaintenanceInterval).
	Interval time.Duration
	// StaleAfter is the reclaim heartbeat age (default DefaultStaleAfter).
	StaleAfter time.Duration
	// OperationRetention keeps terminal operations this long (default DefaultOperationRetention). Keep it
	// far above Interval so the refresh-failure reconcile sees every FAILED refresh before it is pruned.
	OperationRetention time.Duration
	// EventRetention keeps lifecycle events this long (default DefaultEventRetention).
	EventRetention time.Duration
	// Logger receives maintenance logs; nil = slog.Default().
	Logger *slog.Logger
	// TraceTick, when set, wraps every tick in a trace span (Datadog when MEMD_DATADOG_ENABLED), so the
	// steps' Postgres spans nest under it; nil = no tracing.
	TraceTick TickTraceFunc
}

// withDefaults fills zero fields.
func (d MaintenanceDeps) withDefaults() MaintenanceDeps {
	if d.Clock == nil {
		d.Clock = systemClock{}
	}
	if d.Interval <= 0 {
		d.Interval = DefaultMaintenanceInterval
	}
	if d.StaleAfter <= 0 {
		d.StaleAfter = DefaultStaleAfter
	}
	if d.OperationRetention <= 0 {
		d.OperationRetention = DefaultOperationRetention
	}
	if d.ConsolidationGrace <= 0 {
		d.ConsolidationGrace = DefaultConsolidationGrace
	}
	if d.StrandedRetryAfter <= 0 {
		d.StrandedRetryAfter = DefaultStrandedRetryAfter
	}
	if d.EventRetention <= 0 {
		d.EventRetention = DefaultEventRetention
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return d
}

// RunMaintenance ticks every 60s in every worker: reclaim stale jobs, resume stalled bank deletes, cron
// refreshes, consolidation and graph-maintenance reconciles, re-queue rederives of stranded facts, settle
// retain_batch parents, prune terminal ops and events. No leader: each step is idempotent. The delete resume
// and the refresh-failure reconcile run right after reclaim, so they see jobs reclaim failed in the same
// tick. It ticks once at start and returns nil when ctx is
// done; a failing step is logged and never stops the others.
func RunMaintenance(ctx context.Context, d MaintenanceDeps) error {
	if d.Claimer == nil || d.Store == nil {
		return errors.New("worker: maintenance needs a Claimer and a Store")
	}
	d = d.withDefaults()
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		tickTraced(ctx, d)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// tickTraced runs one tick, inside d.TraceTick's span when set, and logs its errors.
func tickTraced(ctx context.Context, d MaintenanceDeps) {
	tctx, finish := ctx, func(error) {}
	if d.TraceTick != nil {
		tctx, finish = d.TraceTick(ctx)
	}
	err := maintenanceTick(tctx, d)
	finish(err)
	if err != nil && ctx.Err() == nil {
		d.Logger.WarnContext(tctx, "maintenance tick had errors", "err", err)
	}
}

// maintenanceStep is one idempotent step of the tick; it returns how many rows it changed.
type maintenanceStep struct {
	name string
	run  func(ctx context.Context, d MaintenanceDeps) (int, error)
}

// maintenanceSteps run in this order on every tick.
var maintenanceSteps = []maintenanceStep{
	{"reclaim_stale", reclaimStale},
	{"resume_bank_deletes", resumeBankDeletes},
	{"refresh_failures", reconcileRefreshFailures},
	{"cron_refresh", enqueueCronRefreshes},
	{"consolidation_reconcile", reconcileConsolidation},
	{"graph_reconcile", reconcileGraph},
	{"rederive_stranded", rederiveStranded},
	{"settle_retain_batches", settleBatches},
	{"prune_operations", pruneOperations},
	{"prune_events", pruneEvents},
}

// maintenanceTick runs every step once and joins their errors.
func maintenanceTick(ctx context.Context, d MaintenanceDeps) error {
	var errs []error
	for _, s := range maintenanceSteps {
		if ctx.Err() != nil {
			break
		}
		n, err := s.run(ctx, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
			continue
		}
		if n > 0 {
			d.Logger.InfoContext(ctx, "maintenance", "step", s.name, "changed", n)
		}
	}
	return errors.Join(errs...)
}

// reclaimStale returns stale PROCESSING jobs to PENDING (or fails them past their max attempts).
func reclaimStale(ctx context.Context, d MaintenanceDeps) (int, error) {
	return d.Claimer.ReclaimStale(ctx, d.StaleAfter)
}

// reconcileRefreshFailures pauses models whose latest terminal refresh failed.
func reconcileRefreshFailures(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.RefreshFailures == nil {
		return 0, nil
	}
	n, err := d.RefreshFailures.ReconcileFailedRefreshes(ctx)
	if err != nil {
		return n, fmt.Errorf("reconcile failed refreshes: %w", err)
	}
	return n, nil
}

// enqueueCronRefreshes enqueues refreshes whose cron schedule fired.
func enqueueCronRefreshes(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.CronRefresh == nil {
		return 0, nil
	}
	n, err := d.CronRefresh.EnqueueDueCronRefreshes(ctx, d.Clock.Now())
	if err != nil {
		return n, fmt.Errorf("cron refreshes: %w", err)
	}
	return n, nil
}

// reconcileConsolidation enqueues an automatic consolidate for every bank with a unit left unconsolidated for
// longer than the grace, unless the bank's effective config turns automatic consolidation or observations
// off (decision 2000). One short transaction per bank. DedupePendingOrRunning joins the bank's queued or
// running automatic consolidate (a running one re-enqueues itself while units remain), so concurrent ticks
// insert at most one, and a tick never queues a job that would make a failing running consolidate skip its
// retry. A bank being deleted (or already gone) is skipped; other errors don't stop the remaining banks. It
// returns how many jobs it inserted.
func reconcileConsolidation(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Consolidation == nil {
		return 0, nil
	}
	banks, err := d.Consolidation.UnconsolidatedBanks(ctx, d.Clock.Now().Add(-d.ConsolidationGrace))
	if err != nil {
		return 0, fmt.Errorf("list banks to consolidate: %w", err)
	}
	var keep []port.BankRef
	var errs []error
	for _, b := range banks {
		ok, err := autoConsolidates(ctx, d.Config, b)
		switch {
		case gone(err):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: config: %w", b.Tenant, b.Bank, err))
		case ok:
			keep = append(keep, b)
		}
	}
	n, err := enqueueEach(ctx, d.Store, keep, domain.OperationKindConsolidate, consolidateSerialKey)
	return n, errors.Join(append(errs, err)...)
}

// autoConsolidates reports whether b's effective config lets the tick consolidate it: auto_consolidation and
// enable_observations both unset or true. A nil resolver lets every bank through.
func autoConsolidates(ctx context.Context, r port.ConfigResolver, b port.BankRef) (bool, error) {
	if r == nil {
		return true, nil
	}
	cfg, err := r.Effective(ctx, b)
	if err != nil {
		return false, err
	}
	on := func(p *bool) bool { return p == nil || *p }
	return on(cfg.AutoConsolidation) && on(cfg.EnableObservations), nil
}

// resumeBankDeletes re-enqueues delete_bank for every tombstoned bank whose delete stopped (decision 2001):
// ReclaimStale and the shutdown release charge attempts and fail delete_bank at its cap with no
// continuation, which would leave the bank tombstoned forever. The enqueue joins a delete_bank another tick
// inserted meanwhile (DedupePendingOrRunning); a bank whose delete just finished is skipped.
func resumeBankDeletes(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Deletes == nil {
		return 0, nil
	}
	banks, err := d.Deletes.StalledBankDeletes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list stalled bank deletes: %w", err)
	}
	return enqueueEach(ctx, d.Store, banks, domain.OperationKindDeleteBank, bank.DeleteBankSerialKey)
}

// reconcileGraph enqueues graph_maintenance for every bank with graph_dirty or entity_maintenance rows
// (decision 2002). A removal that commits while the bank's graph_maintenance is finishing joins that running
// row, so without this step its rows would wait for the next removal.
func reconcileGraph(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Graph == nil {
		return 0, nil
	}
	banks, err := d.Graph.BanksWithGraphWork(ctx)
	if err != nil {
		return 0, fmt.Errorf("list banks with graph work: %w", err)
	}
	return enqueueEach(ctx, d.Store, banks, domain.OperationKindGraphMaintenance, graphSerialKey)
}

// gone reports an error meaning the bank is being deleted or no longer exists.
func gone(err error) bool {
	return errors.Is(err, domain.ErrBankDeleting) || errors.Is(err, domain.ErrNotFound)
}

// enqueueEach enqueues (or joins) an automatic job of kind for every bank, one short transaction each, and
// returns how many it inserted. A bank being deleted (or gone) is skipped; other errors don't stop the rest.
func enqueueEach(
	ctx context.Context, s port.Store, banks []port.BankRef, kind port.JobKind, serialKey string,
) (int, error) {
	inserted := 0
	var errs []error
	for _, b := range banks {
		joined, err := enqueueAutomatic(ctx, s, b, kind, serialKey)
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

// enqueueAutomatic enqueues (or joins) bank b's automatic job of kind: no overrides, an empty payload object
// (decision 502), DedupePendingOrRunning. It takes no change seq: nothing here is memory content, and Seq
// refuses a tombstoned bank, which the delete_bank resume must still reach.
func enqueueAutomatic(
	ctx context.Context, s port.Store, b port.BankRef, kind port.JobKind, serialKey string,
) (joined bool, err error) {
	err = s.InTx(ctx, func(tx port.Tx) error {
		_, j, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank: b, Kind: kind, SerialKey: serialKey, Payload: json.RawMessage(`{}`),
			Dedupe: port.DedupePendingOrRunning,
		})
		joined = j
		return err
	})
	return joined, err
}

// pruneOperations deletes terminal operations that finished more than d.OperationRetention before
// d.Clock.Now() and returns how many; pending and processing operations are never pruned.
func pruneOperations(ctx context.Context, d MaintenanceDeps) (int, error) {
	before := d.Clock.Now().Add(-d.OperationRetention)
	n := 0
	err := d.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		n, err = tx.Queue().Prune(ctx, before)
		return err
	})
	return n, err
}

// pruneEvents prunes lifecycle events past their retention.
func pruneEvents(ctx context.Context, d MaintenanceDeps) (int, error) {
	before := d.Clock.Now().Add(-d.EventRetention)
	n := 0
	err := d.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		n, err = tx.Events().Prune(ctx, before)
		return err
	})
	return n, err
}
