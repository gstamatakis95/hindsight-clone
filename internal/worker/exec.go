package worker

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"runtime/debug"
	"time"
)

// execute runs one job and settles its row. The deferred release runs on every exit, after the terminal
// Complete or Fail, so the job is heartbeated until its handler has returned and been settled.
func (r *Runner) execute(hctx, base context.Context, j port.Job) {
	defer r.wg.Done()
	defer r.release(j.ID)
	hctx, rec := withRecorder(hctx)
	err := r.invoke(hctx, j)
	r.settle(base, j, err, context.Cause(hctx), rec)
}

// invoke calls j's handler, turning a panic into an error (retried like any transient failure).
func (r *Runner) invoke(ctx context.Context, j port.Job) (err error) {
	h := r.handlers[j.Kind]
	if h == nil {
		return fmt.Errorf("%w: no handler for kind %q", ErrPermanent, j.Kind)
	}
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("job handler panicked", "op", j.ID, "kind", j.Kind, "panic", p,
				"stack", string(debug.Stack()))
			err = fmt.Errorf("handler panic: %v", p)
		}
	}()
	return h(ctx, j)
}

// settle writes the job's terminal call. cause is why the runner cancelled the handler, if it did:
//   - success: Complete with the reported result and usage (on a cancel-requested row it ends CANCELLED);
//   - lost job: see settleLost;
//   - shutdown (unless the handler failed permanently): Fail with retry at now, outside the attempt cap,
//     whatever error the interrupted handler returned (an interrupted run is not a failure);
//   - otherwise: Fail with the retry policy's time, or nil once the policy gives up.
func (r *Runner) settle(base context.Context, j port.Job, err, cause error, rec *recorder) {
	ctx, cancel := context.WithTimeout(base, settleTimeout)
	defer cancel()
	switch {
	case err == nil:
		result, usage := rec.get()
		r.report(j, "complete", r.c.Complete(ctx, j.ID, result, usage))
	case errors.Is(cause, errLost):
		r.settleLost(ctx, j, err)
	case errors.Is(cause, errShutdown) && !errors.Is(err, ErrPermanent):
		now := r.cfg.Clock.Now()
		r.report(j, "release (shutdown)", r.c.Fail(ctx, j.ID, fmt.Errorf("%w: %w", errShutdown, err), &now))
	default:
		at := r.retryAt(ctx, j, err)
		if at == nil {
			r.log.Warn("job failed", "op", j.ID, "kind", j.Kind, "attempt", j.Attempt, "err", err)
		} else {
			r.log.Info("job failed, retrying", "op", j.ID, "kind", j.Kind, "attempt", j.Attempt, "at", *at,
				"err", err)
		}
		r.report(j, "fail", r.c.Fail(ctx, j.ID, err, at))
	}
}

// settleLost settles a job Heartbeat reported lost. Alive is true only when the row is PROCESSING under
// another worker (re-claimed after a reclaim): that run is not ours, so nothing is written. Otherwise
// (cancel requested, reclaimed and still pending, finished, bank gone) it calls Fail with no retry, which
// ends a cancel-requested row CANCELLED and is fenced off the rest. A lost run is never requeued, so a
// job can't get a second concurrent run.
func (r *Runner) settleLost(ctx context.Context, j port.Job, err error) {
	if alive, aerr := r.c.Alive(ctx, j.ID); aerr == nil && alive {
		r.log.Warn("job re-claimed by another worker; leaving its row alone", "op", j.ID, "kind", j.Kind)
		return
	}
	r.report(j, "fail (lost)", r.c.Fail(ctx, j.ID, fmt.Errorf("%w: %w", errLost, err), nil))
}

// retryAt applies the retry policy; nil = fail for good. A consolidate that would retry fails instead
// when another consolidate is already pending for its bank (that one picks up the same units).
func (r *Runner) retryAt(ctx context.Context, j port.Job, err error) *time.Time {
	at, ok := nextRun(j, err, r.cfg.Clock.Now())
	if !ok {
		return nil
	}
	if j.Kind == domain.OperationKindConsolidate && r.cfg.Pending != nil {
		pending, perr := r.cfg.Pending.HasPending(ctx, j.Bank, j.Kind)
		switch {
		case perr != nil:
			r.log.Warn("pending consolidate lookup failed; retrying anyway", "op", j.ID, "err", perr)
		case pending:
			r.log.Info("another consolidate is pending; skipping the retry", "op", j.ID, "bank", j.Bank)
			return nil
		}
	}
	return &at
}

// report logs the outcome of a terminal Complete or Fail. Fencing outcomes are expected: ErrCancelled (the
// row was cancel-requested and is now CANCELLED), ErrNotFound for delete_bank (its own row went with the
// bank) and ErrFailedPrecondition/ErrNotFound (the worker no longer held the row). Any other error leaves
// the row PROCESSING; it stops being heartbeated, so ReclaimStale re-queues it.
func (r *Runner) report(j port.Job, what string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrCancelled):
		r.log.Info("operation cancelled", "op", j.ID, "kind", j.Kind)
	case errors.Is(err, domain.ErrNotFound) && j.Kind == domain.OperationKindDeleteBank:
		r.log.Debug("delete_bank finished with its bank", "op", j.ID)
	case errors.Is(err, domain.ErrFailedPrecondition), errors.Is(err, domain.ErrNotFound):
		r.log.Warn("operation no longer held by this worker", "op", j.ID, "kind", j.Kind, "call", what,
			"err", err)
	default:
		r.log.Error("could not settle operation; reclaim will retry it", "op", j.ID, "kind", j.Kind,
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

// heartbeat refreshes every running job and cancels the handlers of the jobs the Claimer reports lost.
func (r *Runner) heartbeat(ctx context.Context) {
	r.mu.Lock()
	ids := make([]port.OperationID, 0, len(r.running))
	for id := range r.running {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	err := r.c.Heartbeat(ctx, r.cfg.WorkerID, ids)
	lost := port.LostJobs(err)
	if err != nil && lost == nil {
		r.log.Warn("heartbeat failed", "jobs", len(ids), "err", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range lost {
		if a := r.running[id]; a != nil {
			r.log.Info("job lost; stopping its handler", "op", id)
			a.cancel(errLost)
		}
	}
}
