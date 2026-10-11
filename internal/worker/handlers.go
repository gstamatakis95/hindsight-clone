package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"
	"memd/internal/engine/docs"
	"memd/internal/engine/maintenance"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/retain"
	"memd/internal/port"
	"time"
)

// Job defaults the handlers own (SPEC "Jobs").
const (
	// DefaultGraphBudget bounds one graph_maintenance run; leftover work re-enqueues the job.
	DefaultGraphBudget = 240 * time.Second
	// graphSerialKey is the graph_maintenance serial key: one per bank.
	graphSerialKey = "graph"
)

// Handlers are the job handlers: thin wrappers around the engines. Each engine reads its own payload; the
// handler reports the result and usage on the operation row and re-enqueues the leftover work of a bounded
// run. Every handler is idempotent because the engines are: they re-read state from Postgres, so a job
// that runs twice after a reclaim repeats no committed work. A nil engine fails its jobs permanently, so
// nothing is retried against a missing dependency.
type Handlers struct {
	// Retainer runs retain_document.
	Retainer retain.Retainer
	// Consolidator runs consolidate. Run enqueues its own continuation when units remain (decision 1000),
	// so the handler never re-enqueues on More.
	Consolidator consolidate.Consolidator
	// Refresher runs refresh_mental_model.
	Refresher mentalmodel.RefreshRunner
	// Deleter runs delete_bank.
	Deleter bank.BankDeleter
	// Rederiver runs rederive_memory.
	Rederiver curate.Rederiver
	// Retagger runs retag_document (Retagger.Apply).
	Retagger docs.Retagger
	// Graph runs graph_maintenance.
	Graph maintenance.GraphMaintainer
	// Store enqueues the continuations of delete_bank and graph_maintenance and re-checks graph work.
	Store port.Store
	// Clock dates the continuations; nil = the system clock.
	Clock port.Clock
	// GraphBudget bounds one graph_maintenance run; 0 = DefaultGraphBudget.
	GraphBudget time.Duration
	// Checkpoint returns a job's checkpoint; refresh, rederive and retag report progress through it before
	// their engine runs (decision 2003). nil = no progress.
	Checkpoint func(j port.Job) Checkpoint
}

// Register registers every handler on r. retain_batch is never registered: it is a parent row whose status
// aggregates its children, not a job a worker runs.
func Register(r JobRunner, h Handlers) {
	if h.Clock == nil {
		h.Clock = systemClock{}
	}
	if h.GraphBudget <= 0 {
		h.GraphBudget = DefaultGraphBudget
	}
	r.Handle(domain.OperationKindRetainDocument, h.retain)
	r.Handle(domain.OperationKindConsolidate, h.consolidate)
	r.Handle(domain.OperationKindRefreshMentalModel, h.refresh)
	r.Handle(domain.OperationKindDeleteBank, h.deleteBank)
	r.Handle(domain.OperationKindRederiveMemory, h.rederive)
	r.Handle(domain.OperationKindRetagDocument, h.retag)
	r.Handle(domain.OperationKindGraphMaintenance, h.graph)
}

// notWired is the permanent error of a job whose engine is nil.
func notWired(j port.Job) error {
	return fmt.Errorf("%w: no %s handler wired", ErrPermanent, j.Kind)
}

// reportJSON records v as the job's result summary, with usage.
func reportJSON(ctx context.Context, v any, usage ...port.Usage) {
	raw, err := json.Marshal(v)
	if err != nil {
		raw = nil
	}
	ReportResult(ctx, raw, usage...)
}

// retain runs retain_document through retain.Retainer.ProcessDocument. The retainer enqueues the bank's
// consolidation in its completing transaction.
func (h Handlers) retain(ctx context.Context, j port.Job) error {
	if h.Retainer == nil {
		return notWired(j)
	}
	res, err := h.Retainer.ProcessDocument(ctx, j)
	if err != nil {
		return fmt.Errorf("retain_document: %w", err)
	}
	reportJSON(ctx, map[string]any{
		"document_id": res.DocumentID, "units_created": res.UnitsCreated, "unchanged": res.Unchanged,
		"superseded": res.Superseded, "chunks_total": res.ChunksTotal, "chunks_skipped": res.ChunksSkipped,
	}, res.Usage...)
	return nil
}

// consolidate runs consolidate through consolidate.Consolidator.Run. It does NOT re-enqueue on More: Run
// already enqueued its continuation (decision 1000).
func (h Handlers) consolidate(ctx context.Context, j port.Job) error {
	if h.Consolidator == nil {
		return notWired(j)
	}
	res, err := h.Consolidator.Run(ctx, j)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}
	reportJSON(ctx, map[string]any{
		"processed": res.Processed, "changed_scopes": len(res.Changed), "more": res.More,
	}, res.Usage...)
	return nil
}

// refresh runs refresh_mental_model through mentalmodel.RunJob (payload decode, then RefreshRunner.Run).
func (h Handlers) refresh(ctx context.Context, j port.Job) error {
	if h.Refresher == nil {
		return notWired(j)
	}
	if err := h.started(ctx, j); err != nil {
		return err
	}
	out, err := mentalmodel.RunJob(ctx, h.Refresher, j)
	if err != nil {
		return fmt.Errorf("refresh_mental_model: %w", err)
	}
	reportJSON(ctx, map[string]any{
		"outcome": out.Trace.Outcome, "version": out.VersionWritten, "effective_mode": out.Trace.EffectiveMode,
		"effective_strategy": out.Trace.EffectiveStrategy, "facts_used": out.FactsUsed,
	}, out.Trace.Usage...)
	return nil
}

// rederive runs rederive_memory through curate.Rederiver.Rederive.
func (h Handlers) rederive(ctx context.Context, j port.Job) error {
	if h.Rederiver == nil {
		return notWired(j)
	}
	if err := h.started(ctx, j); err != nil {
		return err
	}
	if err := h.Rederiver.Rederive(ctx, j); err != nil {
		return fmt.Errorf("rederive_memory: %w", err)
	}
	return nil
}

// retag runs retag_document through docs.Retagger.Apply.
func (h Handlers) retag(ctx context.Context, j port.Job) error {
	if h.Retagger == nil {
		return notWired(j)
	}
	if err := h.started(ctx, j); err != nil {
		return err
	}
	if err := h.Retagger.Apply(ctx, j); err != nil {
		return fmt.Errorf("retag_document: %w", err)
	}
	return nil
}

// started reports j's checkpoint before its engine runs: progress stage = the job kind, 0 of 1 done. The
// engines of these kinds take no checkpoint of their own, so this is the job's one progress write and cancel
// check: a cancelled job, or one whose bank is being deleted, stops here (domain.ErrCancelled) before any
// model call. There is no checkpoint after the engine returns: a late cancel would turn committed work into
// a failure, and Complete settles a cancel-requested row as CANCELLED anyway.
func (h Handlers) started(ctx context.Context, j port.Job) error {
	if h.Checkpoint == nil {
		return nil
	}
	cp := h.Checkpoint(j)
	if cp == nil {
		return nil
	}
	if err := cp.Report(ctx, domain.Progress{Stage: string(j.Kind), At: h.Clock.Now(), Total: 1}); err != nil {
		return fmt.Errorf("%s: checkpoint: %w", j.Kind, err)
	}
	return nil
}

// deleteBank runs delete_bank through bank.BankDeleter.Run and re-enqueues itself (DedupePending) while the
// run reports work left. delete_bank retries without an attempt cap, like consolidate: a transient failure
// on the kind's last attempt enqueues a continuation with backoff before the error fails this row, so a
// half-deleted bank is never abandoned (WP-13 ruling; decision 1451).
func (h Handlers) deleteBank(ctx context.Context, j port.Job) error {
	if h.Deleter == nil || h.Store == nil {
		return notWired(j)
	}
	done, err := h.Deleter.Run(ctx, j)
	if err != nil {
		err = fmt.Errorf("delete_bank: %w", err)
		if permanent(err) || ctx.Err() != nil {
			return err
		}
		if limit := j.Kind.MaxAttempts(); limit > 0 && j.Attempt+1 >= limit {
			at := h.Clock.Now().Add(expBackoff(j.Attempt))
			if eerr := h.continueJob(ctx, j, bank.DeleteBankSerialKey, at); eerr != nil {
				return errors.Join(err, eerr)
			}
		}
		return err
	}
	if done {
		return nil
	}
	return h.continueJob(ctx, j, bank.DeleteBankSerialKey, time.Time{})
}

// graph runs graph_maintenance through maintenance.GraphMaintainer.Run within the budget and re-enqueues
// itself (DedupePending) when work is left. When the run reports done it re-checks graph_dirty and
// entity_maintenance as its last step: a removal that committed after the maintainer's final empty batch
// enqueued graph_maintenance with DedupePendingOrRunning and so joined this running row, and without the
// re-check its rows would wait for the next removal (decision 1451).
func (h Handlers) graph(ctx context.Context, j port.Job) error {
	if h.Graph == nil || h.Store == nil {
		return notWired(j)
	}
	done, err := h.Graph.Run(ctx, j.Bank, h.GraphBudget)
	if err != nil {
		return fmt.Errorf("graph_maintenance: %w", err)
	}
	if done {
		left, err := h.graphWorkLeft(ctx, j.Bank)
		if err != nil {
			return err
		}
		if !left {
			return nil
		}
	}
	return h.continueJob(ctx, j, graphSerialKey, time.Time{})
}

// graphWorkLeft reports whether b has graph_dirty or entity_maintenance rows queued.
func (h Handlers) graphWorkLeft(ctx context.Context, b port.BankRef) (bool, error) {
	left := false
	err := h.Store.InTx(ctx, func(tx port.Tx) error {
		dirty, err := tx.Links().Dirty(ctx, b, 1)
		if err != nil {
			return err
		}
		pending, err := tx.Entities().PendingMaintenance(ctx, b, 1)
		if err != nil {
			return err
		}
		left = len(dirty) > 0 || len(pending) > 0
		return nil
	})
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrBankDeleting) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph re-check: %w", err)
	}
	return left, nil
}

// continueJob enqueues a continuation of j (same kind, serial key and payload, DedupePending), due at at
// (zero = now). DedupePending joins a continuation the engine already queued (the deleter parks one while
// another job of the bank is still running). A continuation for a bank that is gone is not needed.
func (h Handlers) continueJob(ctx context.Context, j port.Job, serialKey string, at time.Time) error {
	payload := j.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	err := h.Store.InTx(ctx, func(tx port.Tx) error {
		_, _, err := tx.Queue().Enqueue(ctx, port.Job{
			Bank: j.Bank, Kind: j.Kind, SerialKey: serialKey, Payload: payload, RunAt: at,
			Dedupe: port.DedupePending,
		})
		return err
	})
	gone := errors.Is(err, domain.ErrNotFound) ||
		(errors.Is(err, domain.ErrBankDeleting) && j.Kind != domain.OperationKindDeleteBank)
	if gone {
		return nil
	}
	if err != nil {
		return fmt.Errorf("re-enqueue %s: %w", j.Kind, err)
	}
	return nil
}
