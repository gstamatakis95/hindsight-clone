package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"memd/internal/domain"
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
	// consolidateSerialKey is the consolidate job's serial key: one per bank.
	consolidateSerialKey = "consolidate"
)

// ConsolidationCandidates finds banks the consolidation reconcile must look at. It is a read the ports
// don't expose yet; the Postgres adapter (or the consolidation engine) implements it.
type ConsolidationCandidates interface {
	// BanksToConsolidate returns every bank that is not tombstoned and has at least one live unit with
	// consolidated_at and consolidation_failed_at unset.
	BanksToConsolidate(ctx context.Context) ([]port.BankRef, error)
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
	// CronRefresh enqueues cron refreshes; nil skips the step.
	CronRefresh CronRefresher
	// RefreshFailures pauses models whose refresh failed; nil skips the step.
	RefreshFailures RefreshFailureReconciler
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

// RunMaintenance ticks every 60s in every worker: reclaim stale jobs, cron refreshes, consolidation
// reconcile, re-queue rederives of stranded facts, prune terminal ops and events. No leader: each step is
// idempotent. The refresh-failure
// reconcile runs right after reclaim, so it sees refreshes that reclaim failed in the same tick. It ticks
// once at start and returns nil when ctx is done; a failing step is logged and never stops the others.
func RunMaintenance(ctx context.Context, d MaintenanceDeps) error {
	if d.Claimer == nil || d.Store == nil {
		return errors.New("worker: maintenance needs a Claimer and a Store")
	}
	d = d.withDefaults()
	t := time.NewTicker(d.Interval)
	defer t.Stop()
	for {
		if err := maintenanceTick(ctx, d); err != nil && ctx.Err() == nil {
			d.Logger.Warn("maintenance tick had errors", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
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
	{"refresh_failures", reconcileRefreshFailures},
	{"cron_refresh", enqueueCronRefreshes},
	{"consolidation_reconcile", reconcileConsolidation},
	{"rederive_stranded", rederiveStranded},
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
			d.Logger.Info("maintenance", "step", s.name, "changed", n)
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

// reconcileConsolidation enqueues a consolidate job for every bank with unconsolidated units, one short
// transaction per bank. DedupePendingOrRunning joins the bank's queued or running automatic consolidate
// (a running one re-enqueues itself while units remain), so concurrent ticks insert at most one, and a
// tick never queues a job that would make a failing running consolidate skip its retry. A bank being
// deleted (or already gone) is skipped; other errors don't stop the remaining banks. It returns how many
// jobs it inserted.
func reconcileConsolidation(ctx context.Context, d MaintenanceDeps) (int, error) {
	if d.Consolidation == nil {
		return 0, nil
	}
	banks, err := d.Consolidation.BanksToConsolidate(ctx)
	if err != nil {
		return 0, fmt.Errorf("list banks to consolidate: %w", err)
	}
	inserted := 0
	var errs []error
	for _, b := range banks {
		joined, err := enqueueConsolidate(ctx, d.Store, b)
		switch {
		case errors.Is(err, domain.ErrBankDeleting), errors.Is(err, domain.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("bank %s/%s: %w", b.Tenant, b.Bank, err))
		case !joined:
			inserted++
		}
	}
	return inserted, errors.Join(errs...)
}

// enqueueConsolidate enqueues (or joins) bank b's automatic consolidate: no overrides, no scopes, an empty
// payload object (decision 502).
func enqueueConsolidate(ctx context.Context, s port.Store, b port.BankRef) (joined bool, err error) {
	err = s.InTx(ctx, func(tx port.Tx) error {
		_, j, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank:      b,
			Kind:      domain.OperationKindConsolidate,
			SerialKey: consolidateSerialKey,
			Payload:   json.RawMessage(`{}`),
			Dedupe:    port.DedupePendingOrRunning,
		})
		joined = j
		return err
	})
	return joined, err
}

// pruneOperations prunes terminal operations past their retention.
func pruneOperations(ctx context.Context, d MaintenanceDeps) (int, error) {
	return PruneOperations(ctx, d.Store, d.Clock, d.OperationRetention)
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

// PruneOperations deletes terminal operations that finished more than olderThan before c.Now() and returns
// how many; pending and processing operations are never pruned. (The spec's signature has no Clock; it is
// added so the cut-off comes from the injected clock, decision 503.)
func PruneOperations(ctx context.Context, s port.Store, c port.Clock, olderThan time.Duration) (int, error) {
	before := c.Now().Add(-olderThan)
	n := 0
	err := s.InTx(ctx, func(tx port.Tx) error {
		var err error
		n, err = tx.Queue().Prune(ctx, before)
		return err
	})
	return n, err
}
