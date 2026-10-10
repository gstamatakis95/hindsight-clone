package worker

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// PendingChecker tells the runner whether a bank already has a pending job of a kind, so a failed
// consolidate skips its retry when another consolidation is queued for the bank.
type PendingChecker interface {
	// HasPending reports whether b has at least one PENDING operation of kind (due or parked).
	HasPending(ctx context.Context, b port.BankRef, kind port.JobKind) (bool, error)
}

// pendingChecker implements PendingChecker over Queue.List in a short read transaction.
type pendingChecker struct {
	s port.Store
}

var _ PendingChecker = (*pendingChecker)(nil)

// NewPendingChecker returns a PendingChecker reading the operations table through s.
func NewPendingChecker(s port.Store) PendingChecker {
	return &pendingChecker{s: s}
}

// HasPending lists at most one pending operation of kind in b.
func (p *pendingChecker) HasPending(ctx context.Context, b port.BankRef, kind port.JobKind) (bool, error) {
	var found bool
	err := p.s.InTx(ctx, func(tx port.Tx) error {
		ops, _, err := tx.Queue().List(ctx, b, port.OperationFilter{
			Status: domain.OperationStatusPending, Kind: kind, PageSize: 1,
		})
		found = len(ops) > 0
		return err
	})
	if err != nil {
		return false, fmt.Errorf("pending %s lookup: %w", kind, err)
	}
	return found, nil
}
