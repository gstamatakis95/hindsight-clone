package main

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/jobs"
	"memd/internal/maintenance"
	"memd/internal/store"
	"time"
)

// handlers holds the worker's job handlers: delete_bank (an adapter over the bank engine that re-enqueues
// its leftover work), graph_maintenance, and every service's own RunXJob, which each wire_<area>.go adds with
// add. Each handler reads its own payload and re-reads its state from Postgres, so a job that runs twice after
// a reclaim repeats no committed work.
type handlers struct {
	deleter bank.BankDeleter
	graph   *maintenance.Service
	db      store.DB    // enqueues delete_bank's continuations
	clock   clock.Clock // dates the continuations
	// services are the handlers services run themselves (Service.RunXJob), by kind.
	services map[domain.OperationKind]jobs.Handler
}

// add registers a service's own handler for kind.
func (h *handlers) add(kind domain.OperationKind, fn jobs.Handler) {
	if h.services == nil {
		h.services = map[domain.OperationKind]jobs.Handler{}
	}
	h.services[kind] = fn
}

// register registers every handler on r. retain_batch is never registered: it is a parent row whose status
// aggregates its children, not a job a worker runs.
func (h handlers) register(r *jobs.Runner) {
	r.Handle(domain.OperationKindDeleteBank, h.deleteBank)
	r.Handle(domain.OperationKindGraphMaintenance, h.graph.RunGraphJob)
	for kind, fn := range h.services {
		r.Handle(kind, fn)
	}
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
