package main

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/docs"
	"memd/internal/engine/mentalmodel"
	"memd/internal/jobs"
	"memd/internal/maintenance"
	"memd/internal/store"
	"time"
)

// handlers adapts the engines to jobs.Handler, one method per job kind. Each engine reads its own payload
// and re-reads its state from Postgres, so a job that runs twice after a reclaim repeats no committed work;
// the adapter records the result and usage on the run and re-enqueues delete_bank's leftover work. Each
// wire_<area>.go sets its engines.
type handlers struct {
	consolidator consolidate.Consolidator
	refresher    mentalmodel.RefreshRunner
	deleter      bank.BankDeleter
	retagger     docs.Retagger
	graph        *maintenance.Service
	db           store.DB    // enqueues delete_bank's continuations
	clock        clock.Clock // dates the continuations and progress
}

// register registers every handler on r. retain_batch is never registered: it is a parent row whose status
// aggregates its children, not a job a worker runs.
func (h handlers) register(r *jobs.Runner) {
	r.Handle(domain.OperationKindConsolidate, h.consolidate)
	r.Handle(domain.OperationKindRefreshMentalModel, h.refresh)
	r.Handle(domain.OperationKindDeleteBank, h.deleteBank)
	r.Handle(domain.OperationKindRetagDocument, h.retag)
	r.Handle(domain.OperationKindGraphMaintenance, h.graph.RunGraphJob)
}

// consolidate runs consolidate through consolidate.Consolidator.Run. It does not re-enqueue on More: Run
// already enqueued its continuation in its final transaction.
func (h handlers) consolidate(ctx context.Context, run *jobs.Run) error {
	res, err := h.consolidator.Run(ctx, run)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}
	run.SetResult(map[string]any{
		"processed": res.Processed, "changed_scopes": len(res.Changed), "more": res.More,
	}, res.Usage...)
	return nil
}

// refresh runs refresh_mental_model through mentalmodel.RunJob (payload decode, then RefreshRunner.Run).
func (h handlers) refresh(ctx context.Context, run *jobs.Run) error {
	if err := h.started(ctx, run); err != nil {
		return err
	}
	out, err := mentalmodel.RunJob(ctx, h.refresher, run.Job)
	if err != nil {
		return fmt.Errorf("refresh_mental_model: %w", err)
	}
	run.SetResult(map[string]any{
		"outcome": out.Trace.Outcome, "version": out.VersionWritten, "effective_mode": out.Trace.EffectiveMode,
		"effective_strategy": out.Trace.EffectiveStrategy, "facts_used": out.FactsUsed,
	}, out.Trace.Usage...)
	return nil
}

// retag runs retag_document through docs.Retagger.Apply.
func (h handlers) retag(ctx context.Context, run *jobs.Run) error {
	if err := h.started(ctx, run); err != nil {
		return err
	}
	if err := h.retagger.Apply(ctx, run.Job); err != nil {
		return fmt.Errorf("retag_document: %w", err)
	}
	return nil
}

// started reports the run's progress before its engine runs: stage = the job kind, 0 of 1 done. The
// engines of these kinds take no progress reports of their own, so this is the job's one progress write and
// cancel check: a cancelled job, or one whose bank is being deleted, stops here (domain.ErrCancelled) before
// any model call. There is no report after the engine returns: a late cancel would turn committed work into
// a failure, and Complete settles a cancel-requested row as CANCELLED anyway.
func (h handlers) started(ctx context.Context, run *jobs.Run) error {
	p := domain.Progress{Stage: string(run.Job.Kind), At: h.clock.Now(), Total: 1}
	if err := run.Report(ctx, p); err != nil {
		return fmt.Errorf("%s: checkpoint: %w", run.Job.Kind, err)
	}
	return nil
}

// deleteBank runs delete_bank through bank.BankDeleter.Run and re-enqueues itself (jobs.Requeue,
// DedupePending) while the run reports work left. delete_bank retries without an attempt cap, like
// consolidate: a transient failure on the kind's last attempt enqueues a continuation with jobs.Backoff
// before the error fails this row, so a half-deleted bank is never abandoned (decision 1451).
func (h handlers) deleteBank(ctx context.Context, run *jobs.Run) error {
	j := run.Job
	done, err := h.deleter.Run(ctx, run)
	if err != nil {
		err = fmt.Errorf("delete_bank: %w", err)
		if jobs.IsPermanent(err) || ctx.Err() != nil {
			return err
		}
		if limit := j.Kind.MaxAttempts(); limit > 0 && j.Attempt+1 >= limit {
			at := h.clock.Now().Add(jobs.Backoff(j.Attempt))
			if eerr := jobs.Requeue(ctx, h.db, j, jobs.SerialKeyDeleteBank, at); eerr != nil {
				return errors.Join(err, eerr)
			}
		}
		return err
	}
	if done {
		return nil
	}
	return jobs.Requeue(ctx, h.db, j, jobs.SerialKeyDeleteBank, time.Time{})
}
