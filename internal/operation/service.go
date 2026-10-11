package operation

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Deps are the Service's dependencies.
type Deps struct {
	// DB reads the operations and events and runs cancel and retry, each in one short transaction.
	DB store.DB
}

// Service serves OperationService.
type Service struct {
	d Deps
}

// New returns the Service over d.
func New(d Deps) *Service {
	return &Service{d: d}
}

// GetOperation returns one operation of b; an unknown operation or bank is domain.ErrNotFound. A tombstoned bank
// answers only for its delete_bank operations (any other id is domain.ErrBankNotFound, like every read of a bank
// being deleted), so a client can poll the operation DeleteBank returned until the delete's last step removes it
// with the bank.
func (s *Service) GetOperation(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		op = domain.Operation{}
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		got, err := tx.Queue().Get(ctx, b, id)
		switch {
		case bank.Deleting() && (err != nil || got.Kind != domain.OperationKindDeleteBank):
			return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b.Bank)
		case err != nil:
			return err
		}
		op = got
		return nil
	})
	return op, err
}

// ListOperations pages b's operations, newest first, and returns the next page token; the page size follows
// domain.ListPage. An unknown or tombstoned bank is domain.ErrNotFound.
func (s *Service) ListOperations(
	ctx context.Context, b domain.BankRef, f store.OperationFilter,
) ([]domain.Operation, string, error) {
	size, err := domain.ListPage.Size(f.PageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list operations: %w", err)
	}
	f.PageSize = size
	var ops []domain.Operation
	var next string
	err = s.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		ops, next, err = tx.Queue().List(ctx, b, f)
		return err
	})
	return ops, next, err
}

// ListEvents returns b's events with seq > afterSeq, of types (empty = all), and the last seq scanned (the next
// after_seq). pageSize is a soft limit that follows domain.ListPage (0 = 100, capped at 1000, negative =
// domain.ErrInvalidArgument): a page is extended to finish its last seq. An unknown or tombstoned bank is
// domain.ErrNotFound.
func (s *Service) ListEvents(
	ctx context.Context, b domain.BankRef, afterSeq int64, pageSize int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64, error) {
	size, err := domain.ListPage.Size(pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	var events []domain.LifecycleEvent
	var last int64
	err = s.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		events, last, err = tx.Events().List(ctx, b, afterSeq, size, types)
		return err
	})
	return events, last, err
}

// CancelOperation marks a pending or processing operation CANCELLED (a running handler stops at its next
// checkpoint); a terminal one, and any delete_bank, is domain.ErrOperationNotCancellable.
func (s *Service) CancelOperation(
	ctx context.Context, b domain.BankRef, id domain.OperationID,
) (domain.Operation, error) {
	var op domain.Operation
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		cur, err := tx.Queue().Get(ctx, b, id)
		if err != nil {
			return err
		}
		if cur.Kind == domain.OperationKindDeleteBank {
			return fmt.Errorf("%w: a delete_bank operation can't be cancelled", domain.ErrOperationNotCancellable)
		}
		op, err = tx.Queue().Cancel(ctx, b, id)
		return err
	})
	return op, err
}

// RetryOperation re-queues a FAILED or CANCELLED operation as PENDING; any other status is
// domain.ErrOperationNotRetryable, and an operation other than delete_bank on a bank being deleted is
// domain.ErrBankDeleting.
func (s *Service) RetryOperation(
	ctx context.Context, b domain.BankRef, id domain.OperationID,
) (domain.Operation, error) {
	var op domain.Operation
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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

// readBank runs fn in a transaction after checking b is a live bank (unknown or tombstoned =
// domain.ErrNotFound).
func (s *Service) readBank(ctx context.Context, b domain.BankRef, fn func(tx store.Tx) error) error {
	return s.d.DB.InTx(ctx, func(tx store.Tx) error {
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.DeletingAt != nil {
			return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b.Bank)
		}
		return fn(tx)
	})
}
