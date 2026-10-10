package worker

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// Handlers are the job handlers: thin wrappers around the engines that decode a job's payload, call the
// engine, report progress through a Checkpoint and re-enqueue leftover work. They are stubs until WP-14
// wires the engines in; each stub fails its job permanently, so nothing is retried against a missing
// engine.
type Handlers struct{}

// Register registers every handler on r. retain_batch is never registered: it is a parent row whose status
// aggregates its children, not a job a worker runs.
func Register(r JobRunner, h Handlers) {
	r.Handle(domain.OperationKindRetainDocument, h.retain)
	r.Handle(domain.OperationKindConsolidate, h.consolidate)
	r.Handle(domain.OperationKindRefreshMentalModel, h.refresh)
	r.Handle(domain.OperationKindDeleteBank, h.deleteBank)
	r.Handle(domain.OperationKindRederiveMemory, h.rederive)
	r.Handle(domain.OperationKindRetagDocument, h.retag)
	r.Handle(domain.OperationKindGraphMaintenance, h.graph)
}

// notWired is the stub handlers' permanent error.
func notWired(j port.Job) error {
	return fmt.Errorf("%w: no %s handler wired yet (WP-14)", ErrPermanent, j.Kind)
}

// retain runs retain_document (WP-14: retain.Retainer.ProcessDocument).
func (h Handlers) retain(_ context.Context, j port.Job) error { return notWired(j) }

// consolidate runs consolidate and re-enqueues when More (WP-14: consolidate.Consolidator.Run).
func (h Handlers) consolidate(_ context.Context, j port.Job) error { return notWired(j) }

// refresh runs refresh_mental_model (WP-14: mentalmodel.RefreshRunner.Run).
func (h Handlers) refresh(_ context.Context, j port.Job) error { return notWired(j) }

// deleteBank runs delete_bank (WP-14: bank.BankDeleter).
func (h Handlers) deleteBank(_ context.Context, j port.Job) error { return notWired(j) }

// rederive runs rederive_memory (WP-14: curate.Rederiver).
func (h Handlers) rederive(_ context.Context, j port.Job) error { return notWired(j) }

// retag runs retag_document (WP-14: docs.Retagger).
func (h Handlers) retag(_ context.Context, j port.Job) error { return notWired(j) }

// graph runs graph_maintenance and re-enqueues when the budget runs out (WP-14:
// maintenance.GraphMaintainer.Run).
func (h Handlers) graph(_ context.Context, j port.Job) error { return notWired(j) }
