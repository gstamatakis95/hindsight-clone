package mentalmodel

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// cronGrace is how long after a fire time the cron sweep still serves it. The maintenance tick runs every
// minute, so a few minutes absorb a slow or skipped tick; an older fire time waits for the next one.
const cronGrace = 5 * time.Minute

// parseCron parses a schedule.cron expression: exactly 5 fields (minute hour dom month dow), evaluated in
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

// EnqueueDueCronRefreshes is the maintenance tick's cron sweep. It enqueues one automatic refresh per model whose
// schedule.cron has an unserved fire time F in (now - cronGrace, now]: no refresh operation of the model was
// created at or after F, and F is not before the model's creation. Each refresh goes through decide (paused and
// fresh models are skipped; the rest are parked for the debounce or interval floor) and is enqueued in its own
// short transaction, joining a queued one; tombstoned banks are skipped. It returns how many refreshes it
// inserted. A fire time missed by more than cronGrace (the service was down) is skipped, so a refresh never runs
// off-schedule. It is idempotent under concurrent callers: due-ness is keyed on the fire time, and a second
// enqueue joins the queued one. Consolidation enqueues its own refreshes in its apply transaction.
func (s *Service) EnqueueDueCronRefreshes(ctx context.Context) (int, error) {
	if s.d.Maintenance == nil {
		return 0, nil
	}
	now := s.d.Clock.Now()
	cms, err := s.d.Maintenance.CronModels(ctx)
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: cron models: %w", err)
	}
	var due []domain.MentalModel
	for _, cm := range cms {
		if !cm.Model.AutoRefreshPaused && cronDue(cm, now) {
			due = append(due, cm.Model)
		}
	}
	return s.triggerAll(ctx, due)
}

// cronDue reports whether cm's schedule has a fire time F in (now - cronGrace, now] that is not yet served
// (no refresh operation created at or after F) and not before the model's creation. An unparsable schedule
// never fires.
func cronDue(cm store.CronModel, now time.Time) bool {
	expr := strings.TrimSpace(cm.Model.Spec.Schedule.Cron)
	if expr == "" {
		return false
	}
	sched, err := parseCron(expr)
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
func (s *Service) triggerAll(ctx context.Context, models []domain.MentalModel) (int, error) {
	n := 0
	var errs []error
	for _, m := range models {
		inserted, err := s.trigger(ctx, m)
		if err != nil && !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrBankDeleting) {
			errs = append(errs, fmt.Errorf("mentalmodel: trigger %s: %w", m.Spec.ID, err))
		}
		if inserted {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// trigger enqueues one automatic refresh of m unless decide skips it.
func (s *Service) trigger(ctx context.Context, m domain.MentalModel) (bool, error) {
	now := s.d.Clock.Now()
	dec, err := s.decide(ctx, m, now)
	if err != nil {
		return false, fmt.Errorf("mentalmodel: decide: %w", err)
	}
	if dec.Kind == decisionSkip {
		return false, nil
	}
	var joined bool
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		if _, err := tx.Seq(ctx, m.Bank); err != nil {
			return err
		}
		var err error
		_, joined, err = jobs.EnqueueRefresh(ctx, tx, jobs.RefreshRequest{Bank: m.Bank, ID: m.Spec.ID, RunAt: dec.Until}, now)
		return err
	})
	return err == nil && !joined, err
}

// listAll reads every page of q from r.
func listAll(ctx context.Context, r store.Reader, q store.ModelQuery) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	q.PageToken = ""
	for {
		page, next, err := r.ListMentalModels(ctx, q)
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
