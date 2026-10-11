package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"runtime/debug"
	"time"
)

// Settle retry backoff: 50 ms doubling to 2 s, within settleTimeout.
const (
	settleRetryBase = 50 * time.Millisecond
	settleRetryMax  = 2 * time.Second
)

// execute runs one job and settles its row. The deferred release runs on every exit, after the terminal
// Complete or Fail, so the run is heartbeated until its handler has returned and been settled.
//
// With a TraceJob hook the run is wrapped in its span: the handler runs under the span's context and the
// settle under the same values without the run's cancellation, so their Postgres and gateway spans nest
// under the job's and their logs carry its trace id.
func (r *Runner) execute(hctx, base context.Context, j store.Job, a *active) {
	defer r.wg.Done()
	defer r.release(j.ID, a)
	var finish func(string, error)
	if r.cfg.TraceJob != nil {
		hctx, finish = r.cfg.TraceJob(hctx, j)
		base = context.WithoutCancel(hctx)
	}
	run := NewRun(r.c, j)
	err := r.invoke(hctx, run)
	outcome := r.settle(base, j, err, context.Cause(hctx), run)
	if finish != nil {
		finish(outcome, err)
	}
}

// invoke calls the handler of run's job, turning a panic into an error (retried like any transient failure).
func (r *Runner) invoke(ctx context.Context, run *Run) (err error) {
	j := run.Job
	h := r.handlers[j.Kind]
	if h == nil {
		return fmt.Errorf("%w: no handler for kind %q", ErrPermanent, j.Kind)
	}
	defer func() {
		if p := recover(); p != nil {
			r.log.ErrorContext(ctx, "job handler panicked", "op", j.ID, "kind", j.Kind, "panic", p,
				"stack", string(debug.Stack()))
			err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	return h(ctx, run)
}

// settle writes the job's terminal call. cause is why the runner cancelled the handler, if it did:
//   - lost run (checked first, whatever the handler returned): see settleLost;
//   - success: Complete with the reported result and usage (on a cancel-requested row it ends CANCELLED);
//   - shutdown (unless the handler failed permanently): Fail with retry at now, whatever error the
//     interrupted handler returned. Fail with a retry time always charges an attempt, so the release is
//     not outside the attempt cap; the shutdown grace keeps such releases rare;
//   - otherwise: Fail with the retry policy's time, or nil once the policy gives up.
//
// It returns the run's outcome (one of the Outcome* values) for the job's trace span.
func (r *Runner) settle(base context.Context, j store.Job, err, cause error, run *Run) string {
	ctx, cancel := context.WithTimeout(base, settleTimeout)
	defer cancel()
	switch {
	case errors.Is(cause, errLost):
		r.settleLost(ctx, j, err, run)
		return OutcomeLost
	case err == nil:
		r.complete(ctx, j, run)
		return OutcomeCompleted
	case errors.Is(cause, errShutdown) && !deterministic(err):
		now := r.cfg.Clock.Now()
		cause := fmt.Errorf("%w: %w", errShutdown, err)
		r.fail(ctx, j, "release (shutdown)", cause, &now)
		return OutcomeReleased
	default:
		at := r.retryAt(ctx, j, err)
		outcome := OutcomeRetry
		if at == nil {
			outcome = OutcomeFailed
			r.log.WarnContext(ctx, "job failed", "op", j.ID, "kind", j.Kind, "attempt", j.Attempt, "err", err)
		} else {
			r.log.InfoContext(ctx, "job failed, retrying", "op", j.ID, "kind", j.Kind, "attempt", j.Attempt,
				"at", *at, "err", err)
		}
		r.fail(ctx, j, "fail", err, at)
		return outcome
	}
}

// fail writes Fail(cause, at). A Fail whose retry time falls within the settle timeout is tried once: if
// it committed but its ack was lost, the row may already be due and re-claimed by another worker, and a
// repeated Fail would reset that worker's PROCESSING row to PENDING (a double run). Left unwritten, the row
// stays PROCESSING without heartbeats and ReclaimStale re-queues it. Later retry times (and terminal
// fails, which a repeat can only hit fenced) are retried through persist.
func (r *Runner) fail(ctx context.Context, j store.Job, what string, cause error, at *time.Time) {
	call := func(c context.Context) error { return r.c.Fail(c, j.ID, cause, at) }
	if at != nil && !at.After(r.cfg.Clock.Now().Add(settleTimeout)) {
		r.report(ctx, j, what, call(ctx))
		return
	}
	r.persist(ctx, j, what, call)
}

// complete writes Complete with the run's reported result and usage.
func (r *Runner) complete(ctx context.Context, j store.Job, run *Run) {
	result, usage := run.Result()
	r.persist(ctx, j, "complete", func(c context.Context) error { return r.c.Complete(c, j.ID, result, usage) })
}

// settleLost settles a run the runner stopped as lost (Heartbeat named it, it went too long without a
// heartbeat, or this runner re-claimed the id). Alive is true only when the row is PROCESSING under another
// run (another worker, or a newer run here): that run is not this one's, so nothing is written. If Alive
// fails, nothing is written either; ReclaimStale covers the row. Otherwise (cancel requested, reclaimed
// and pending, finished, bank gone) a successful run is completed and a failed one failed with no retry:
// a cancel-requested row ends CANCELLED and the rest are fenced. A lost run is never requeued, so a job
// can't get a second concurrent run.
func (r *Runner) settleLost(ctx context.Context, j store.Job, err error, run *Run) {
	alive, aerr := r.c.Alive(ctx, j.ID)
	switch {
	case aerr != nil:
		r.log.WarnContext(ctx, "lost job: Alive failed; leaving the row to reclaim", "op", j.ID, "err", aerr)
	case alive:
		r.log.WarnContext(ctx, "lost job is running elsewhere; leaving its row alone", "op", j.ID, "kind", j.Kind)
	case err == nil:
		r.complete(ctx, j, run)
	default:
		cause := fmt.Errorf("%w: %w", errLost, err)
		r.fail(ctx, j, "fail (lost)", cause, nil)
	}
}

// retryAt applies the retry policy; nil = fail for good. An automatic consolidate that would retry fails
// instead when another automatic consolidate is already due for its bank (that one picks up the same units
// with the same settings). A manual one (scopes or overrides) always keeps its retry: the automatic run
// would use bank defaults, so its overrides would never be applied (decision 2003).
func (r *Runner) retryAt(ctx context.Context, j store.Job, err error) *time.Time {
	at, ok := nextRun(j, err, r.cfg.Clock.Now())
	if !ok {
		return nil
	}
	if j.Kind == domain.OperationKindConsolidate && r.pending != nil && automaticConsolidate(j) {
		pending, perr := r.pending.hasPending(ctx, j.Bank)
		switch {
		case perr != nil:
			r.log.Warn("pending consolidate lookup failed; retrying anyway", "op", j.ID, "err", perr)
		case pending:
			r.log.Info("another consolidate is due; skipping the retry", "op", j.ID, "bank", j.Bank)
			return nil
		}
	}
	return &at
}

// automaticConsolidate reports whether consolidate job j is the automatic kind: no overrides and no scopes in
// its payload. An undecodable payload counts as manual, so its retry is never skipped.
func automaticConsolidate(j store.Job) bool {
	if j.Overrides != nil {
		return false
	}
	var p ConsolidatePayload
	if raw := bytes.TrimSpace(j.Payload); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &p); err != nil {
			return false
		}
	}
	return len(p.Scopes) == 0
}

// persist runs a terminal Complete or Fail, retrying transient errors with backoff until ctx (the settle
// timeout) runs out. Fencing outcomes end it at once (see report).
func (r *Runner) persist(ctx context.Context, j store.Job, what string, call func(context.Context) error) {
	wait := settleRetryBase
	for {
		err := call(ctx)
		if err == nil || fenced(err) {
			r.report(ctx, j, what, err)
			return
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			r.report(ctx, j, what, err)
			return
		case <-t.C:
		}
		r.log.Debug("retrying terminal call", "op", j.ID, "call", what, "err", err)
		wait = min(2*wait, settleRetryMax)
	}
}

// fenced reports whether a Complete/Fail error is a final fencing outcome rather than a transient failure.
func fenced(err error) bool {
	return errors.Is(err, domain.ErrCancelled) || errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrFailedPrecondition)
}

// report logs the outcome of a terminal Complete or Fail. Fencing outcomes are expected: ErrCancelled (the
// row was cancel-requested and is now CANCELLED), ErrNotFound for delete_bank (its own row went with the
// bank) and ErrFailedPrecondition/ErrNotFound (the worker no longer held the row). Any other error leaves
// the row PROCESSING; it stops being heartbeated, so ReclaimStale re-queues it.
func (r *Runner) report(ctx context.Context, j store.Job, what string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrCancelled):
		r.log.InfoContext(ctx, "operation cancelled", "op", j.ID, "kind", j.Kind)
	case errors.Is(err, domain.ErrNotFound) && j.Kind == domain.OperationKindDeleteBank:
		r.log.DebugContext(ctx, "delete_bank finished with its bank", "op", j.ID)
	case errors.Is(err, domain.ErrFailedPrecondition), errors.Is(err, domain.ErrNotFound):
		r.log.WarnContext(ctx, "operation no longer held by this worker", "op", j.ID, "kind", j.Kind, "call", what,
			"err", err)
	default:
		r.log.ErrorContext(ctx, "could not settle operation; reclaim will retry it", "op", j.ID, "kind", j.Kind,
			"call", what, "err", err)
	}
}

// heartbeatLoop heartbeats the running jobs every HeartbeatInterval until ctx is done.
func (r *Runner) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.heartbeat(ctx)
		}
	}
}

// heartbeat refreshes every running job (one call, bounded by HeartbeatInterval). It stops the handlers of
// the jobs the Claimer reports lost, and of every run whose last successful heartbeat is older than 3/4 of
// StaleAfter: by then another worker may reclaim it, so the runner fences itself first.
func (r *Runner) heartbeat(ctx context.Context) {
	r.mu.Lock()
	ids := make([]domain.OperationID, 0, len(r.running))
	for id := range r.running {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	hctx, cancel := context.WithTimeout(ctx, r.cfg.HeartbeatInterval)
	err := r.c.Heartbeat(hctx, r.cfg.WorkerID, ids)
	cancel()
	lost := store.LostJobs(err)
	ok := err == nil || lost != nil
	if !ok {
		r.log.Warn("heartbeat failed", "jobs", len(ids), "err", err)
	}
	now := r.cfg.Clock.Now()
	fenceAfter := r.cfg.StaleAfter * 3 / 4
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range lost {
		if a := r.running[id]; a != nil {
			r.log.Info("job lost; stopping its handler", "op", id)
			a.cancel(errLost)
		}
	}
	for _, id := range ids {
		a := r.running[id]
		if a == nil {
			continue
		}
		if ok {
			a.lastBeat = now
		} else if now.Sub(a.lastBeat) > fenceAfter {
			r.log.Warn("no heartbeat for too long; stopping the handler before it is reclaimed", "op", id)
			a.cancel(errLost)
		}
	}
}
