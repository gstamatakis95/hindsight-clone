package mentalmodel

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Triggers enqueues automatic refreshes: after consolidation, on retracted sources and on cron schedules.
// Each refresh goes through the RefreshScheduler (paused and fresh models are skipped; the rest are parked
// for the debounce or interval floor) and is enqueued in its own short transaction, joining a queued one.
type Triggers interface {
	// AfterConsolidation refreshes b's stale models whose trigger sets refresh_after_consolidation, and
	// returns how many refreshes it inserted (joins excluded).
	AfterConsolidation(ctx context.Context, b port.BankRef) (int, error)
	// Retracted refreshes b's models whose source_ids include any of ids (invalidated or replaced units),
	// and returns how many refreshes it inserted.
	Retracted(ctx context.Context, b port.BankRef, ids []port.UnitID) (int, error)
	// EnqueueDueCronRefreshes enqueues one refresh per model whose refresh_cron has an unserved fire
	// time F in (now - cronGrace, now]: no refresh operation of the model was created at or after F, and
	// F is not before the model's creation. Paused and fresh models and tombstoned banks are skipped; it
	// returns how many it inserted. A fire time missed by more than cronGrace (the service was down) is
	// skipped, so a refresh never runs off-schedule. It is idempotent under concurrent callers: due-ness
	// is keyed on the fire time, and a second enqueue joins the queued one. It implements
	// worker.CronRefresher.
	EnqueueDueCronRefreshes(ctx context.Context, now time.Time) (int, error)
}

// TriggerDeps are the Triggers' dependencies.
type TriggerDeps struct {
	// Store enqueues in short transactions.
	Store port.Store
	// Scheduler decides run now, park or skip.
	Scheduler RefreshScheduler
	// Models lists a bank's models.
	Models ModelLister
	// Cron finds models with a cron schedule; nil disables cron refreshes.
	Cron CronSource
	// Clock is the trigger time.
	Clock port.Clock
}

// triggers is the Triggers implementation.
type triggers struct {
	d      TriggerDeps
	parser cronSpecParser
}

var _ Triggers = (*triggers)(nil)

// NewTriggers returns the automatic refresh triggers.
func NewTriggers(d TriggerDeps) Triggers {
	return &triggers{d: d, parser: cronParser()}
}

// cronGrace is how long after a fire time the cron sweep still serves it. The maintenance tick runs every
// minute, so a few minutes absorb a slow or skipped tick; an older fire time waits for the next one.
const cronGrace = 5 * time.Minute

// cronSpecParser parses refresh_cron expressions with parseCron.
type cronSpecParser struct{}

// Parse parses expr with parseCron.
func (cronSpecParser) Parse(expr string) (cron.Schedule, error) { return parseCron(expr) }

// cronParser returns the refresh_cron parser (UTC, exactly 5 fields).
func cronParser() cronSpecParser { return cronSpecParser{} }

// parseCron parses a refresh_cron expression: exactly 5 fields (minute hour dom month dow), evaluated in
// UTC. It rejects @ descriptors and a TZ= or CRON_TZ= prefix.
func parseCron(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, fmt.Errorf("mentalmodel: cron %q: a time zone prefix is not allowed (UTC only)", expr)
	}
	if strings.Contains(expr, "@") {
		return nil, fmt.Errorf("mentalmodel: cron %q: @ descriptors are not allowed", expr)
	}
	if n := len(strings.Fields(expr)); n != 5 {
		return nil, fmt.Errorf("mentalmodel: cron %q: want 5 fields (minute hour dom month dow), got %d", expr, n)
	}
	// minute hour dom month dow: no seconds field and no @ descriptors.
	sched, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: cron %q: %w", expr, err)
	}
	return sched, nil
}

// AfterConsolidation lists the bank's refresh_after_consolidation models and triggers each.
func (t *triggers) AfterConsolidation(ctx context.Context, b port.BankRef) (int, error) {
	models, err := t.list(ctx, ModelQuery{Bank: b, RefreshAfterConsolidation: true})
	if err != nil {
		return 0, err
	}
	return t.triggerAll(ctx, models)
}

// Retracted lists the bank's models and triggers those citing any of ids.
func (t *triggers) Retracted(ctx context.Context, b port.BankRef, ids []port.UnitID) (int, error) {
	models, err := t.list(ctx, ModelQuery{Bank: b})
	if err != nil {
		return 0, err
	}
	var citing []domain.MentalModel
	for _, m := range models {
		if slices.ContainsFunc(m.SourceIDs, func(id port.UnitID) bool { return slices.Contains(ids, id) }) {
			citing = append(citing, m)
		}
	}
	return t.triggerAll(ctx, citing)
}

// EnqueueDueCronRefreshes triggers every cron model whose latest fire time in the grace window is unserved.
func (t *triggers) EnqueueDueCronRefreshes(ctx context.Context, now time.Time) (int, error) {
	if t.d.Cron == nil {
		return 0, nil
	}
	cms, err := t.d.Cron.CronModels(ctx)
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: cron models: %w", err)
	}
	var due []domain.MentalModel
	for _, cm := range cms {
		if !cm.Model.AutoRefreshPaused && t.cronDue(cm, now) {
			due = append(due, cm.Model)
		}
	}
	return t.triggerAll(ctx, due)
}

// cronDue reports whether cm's schedule has a fire time F in (now - cronGrace, now] that is not yet served
// (no refresh operation created at or after F) and not before the model's creation. An unparsable schedule
// never fires.
func (t *triggers) cronDue(cm CronModel, now time.Time) bool {
	expr := strings.TrimSpace(cm.Model.Spec.Trigger.RefreshCron)
	if expr == "" {
		return false
	}
	sched, err := t.parser.Parse(expr)
	if err != nil {
		return false
	}
	fire, ok := latestFire(sched, now.UTC())
	return ok && cm.LastRefreshEnqueuedAt.Before(fire) && !fire.Before(cm.Model.CreatedAt)
}

// latestFire is sched's latest fire time in (now - cronGrace, now]; ok is false when there is none.
func latestFire(sched cron.Schedule, now time.Time) (fire time.Time, ok bool) {
	for at := sched.Next(now.Add(-cronGrace)); !at.IsZero() && !at.After(now); at = sched.Next(at) {
		fire, ok = at, true
	}
	return fire, ok
}

// triggerAll decides and enqueues each model's refresh, continuing past per-model errors.
func (t *triggers) triggerAll(ctx context.Context, models []domain.MentalModel) (int, error) {
	n := 0
	var errs []error
	for _, m := range models {
		inserted, err := t.trigger(ctx, m)
		if err != nil && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrBankDeleting) {
			errs = append(errs, fmt.Errorf("mentalmodel: trigger %s: %w", m.Spec.ID, err))
		}
		if inserted {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// trigger enqueues one automatic refresh of m unless the scheduler skips it.
func (t *triggers) trigger(ctx context.Context, m domain.MentalModel) (bool, error) {
	now := t.d.Clock.Now()
	dec, err := t.d.Scheduler.Decide(ctx, m, now)
	if err != nil {
		return false, fmt.Errorf("mentalmodel: decide: %w", err)
	}
	if dec.Kind == DecisionSkip {
		return false, nil
	}
	var joined bool
	err = t.d.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Seq(ctx, m.Bank); err != nil {
			return err
		}
		var err error
		_, joined, err = EnqueueRefresh(ctx, tx, RefreshRequest{Bank: m.Bank, ID: m.Spec.ID, RunAt: dec.Until}, now)
		return err
	})
	return err == nil && !joined, err
}

// list reads every page of q.
func (t *triggers) list(ctx context.Context, q ModelQuery) ([]domain.MentalModel, error) {
	return listAll(ctx, t.d.Models, q)
}

// listAll reads every page of q from l.
func listAll(ctx context.Context, l ModelLister, q ModelQuery) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	q.PageToken = ""
	for {
		page, next, err := l.ListMentalModels(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("mentalmodel: list models: %w", err)
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		q.PageToken = next
	}
}
