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

// ListEvents page bounds: the limit of a request that sets none, and the cap of any request.
const (
	// DefaultEventsLimit is the ListEvents limit when a request sets 0 or less.
	DefaultEventsLimit = 100
	// MaxEventsLimit caps a requested ListEvents limit.
	MaxEventsLimit = 1000
)

// GetOperation returns one operation of b; an unknown operation, or an unknown or tombstoned bank, is
// domain.ErrNotFound.
func (s *Service) GetOperation(ctx context.Context, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	var op domain.Operation
	err := s.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(ctx, b, id)
		return err
	})
	return op, err
}

// ListOperations pages b's operations, newest first, and returns the next page token; an unknown or tombstoned
// bank is domain.ErrNotFound.
func (s *Service) ListOperations(
	ctx context.Context, b domain.BankRef, f store.OperationFilter,
) ([]domain.Operation, string, error) {
	var ops []domain.Operation
	var next string
	err := s.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		ops, next, err = tx.Queue().List(ctx, b, f)
		return err
	})
	return ops, next, err
}

// ListEvents returns b's events with seq > afterSeq, of types (empty = all), and the last seq scanned. A limit of
// 0 or less is DefaultEventsLimit and one above MaxEventsLimit is capped. An unknown or tombstoned bank is
// domain.ErrNotFound.
func (s *Service) ListEvents(
	ctx context.Context, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64, error) {
	if limit <= 0 {
		limit = DefaultEventsLimit
	}
	limit = min(limit, MaxEventsLimit)
	var events []domain.LifecycleEvent
	var last int64
	err := s.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		events, last, err = tx.Events().List(ctx, b, afterSeq, limit, types)
		return err
	})
	return events, last, err
}

// CancelOperation marks a pending or processing operation CANCELLED (a running handler stops at its next
// checkpoint); a terminal one, and any delete_bank, is domain.ErrFailedPrecondition.
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
			return fmt.Errorf("%w: a delete_bank operation can't be cancelled", domain.ErrFailedPrecondition)
		}
		op, err = tx.Queue().Cancel(ctx, b, id)
		return err
	})
	return op, err
}

// RetryOperation re-queues a FAILED or CANCELLED operation as PENDING; any other status is
// domain.ErrFailedPrecondition, and so is an operation other than delete_bank on a bank being deleted
// (domain.ErrBankDeleting).
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
			return fmt.Errorf("%w: bank %s is being deleted", domain.ErrNotFound, b.Bank)
		}
		return fn(tx)
	})
}
