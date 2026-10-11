package worker

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// OperationControl implements cancel and retry on top of the operations table.
type OperationControl interface {
	// Cancel marks a pending or processing operation CANCELLED (a running handler stops at its next
	// checkpoint); a terminal one, and any delete_bank, is domain.ErrFailedPrecondition.
	Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
	// Retry re-queues a FAILED or CANCELLED operation as PENDING; any other status is
	// domain.ErrFailedPrecondition, and so is an operation other than delete_bank on a bank being deleted
	// (domain.ErrBankDeleting).
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
// the request and becomes CANCELLED when its handler stops (decision 012, R8'). A delete_bank can't be
// cancelled: the bank is already tombstoned, and a stopped delete would leave it so (decision 2001).
func (o *operationControl) Cancel(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := o.s.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.Queue().Get(ctx, b, id)
		if err != nil {
			return err
		}
		if cur.Kind == domain.OperationKindDeleteBank {
			return fmt.Errorf("%w: a delete_bank operation can't be cancelled", domain.ErrFailedPrecondition)
		}
		op, err = tx.Queue().Cancel(ctx, b, id)
		return err
	})
	return op, err
}

// Retry re-queues through Queue.Retry (PENDING, run_at = now, attempt reset). On a bank being deleted only
// delete_bank may be retried (that resumes the delete); any other job would only be skipped by claims and
// removed with the bank, so it is refused like every other write to a deleting bank.
func (o *operationControl) Retry(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := o.s.InTx(ctx, func(tx port.Tx) error {
		cur, err := tx.Queue().Get(ctx, b, id)
		if err != nil {
			return err
		}
		if cur.Kind != domain.OperationKindDeleteBank {
			bank, err := tx.Banks().Get(ctx, b)
			if err != nil {
				return err
			}
			if bank.Deleting() {
				return fmt.Errorf("%w: bank %s/%s: can't retry %s", domain.ErrBankDeleting, b.Tenant, b.Bank, cur.Kind)
			}
		}
		op, err = tx.Queue().Retry(ctx, b, id)
		return err
	})
	return op, err
}
