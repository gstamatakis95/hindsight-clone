package worker

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// OperationControl implements cancel and retry on top of the operations table.
type OperationControl interface {
	// Cancel marks a pending or processing operation CANCELLED (a running handler stops at its next
	// checkpoint); a terminal one is domain.ErrFailedPrecondition.
	Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
	// Retry re-queues a FAILED or CANCELLED operation as PENDING; any other status is
	// domain.ErrFailedPrecondition.
	Retry(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
}

// operationControl implements OperationControl with one short transaction per call.
type operationControl struct {
	s port.Store
}

var _ OperationControl = (*operationControl)(nil)

// NewOperationControl returns an OperationControl over s.
func NewOperationControl(s port.Store) OperationControl {
	return &operationControl{s: s}
}

// Cancel cancels through Queue.Cancel: a pending row becomes CANCELLED at once; a processing row records
// the request and becomes CANCELLED when its handler stops (decision 012, R8').
func (o *operationControl) Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := o.s.InTx(ctx, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Cancel(ctx, b, id)
		return err
	})
	return op, err
}

// Retry re-queues through Queue.Retry (PENDING, run_at = now, attempt reset).
func (o *operationControl) Retry(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := o.s.InTx(ctx, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Retry(ctx, b, id)
		return err
	})
	return op, err
}
