package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
)

// PendingChecker tells the runner whether a bank already has a due automatic consolidate, so a failed
// consolidate skips its retry when another consolidation will pick up the same units.
type PendingChecker interface {
	// HasPending reports whether b has a PENDING, joinable (automatic: no overrides, no scopes) operation
	// of kind that is due now. Only consolidate is supported; other kinds are domain.ErrInvalidArgument.
	HasPending(ctx context.Context, b domain.BankRef, kind domain.OperationKind) (bool, error)
}

// pendingChecker implements PendingChecker with a probe transaction that is always rolled back.
type pendingChecker struct {
	s store.DB
	c clock.Clock
}

var _ PendingChecker = (*pendingChecker)(nil)

// errProbeRollback rolls the probe transaction back.
var errProbeRollback = errors.New("pending probe: roll back")

// NewPendingChecker returns a PendingChecker reading the operations table through s, judging due-ness
// with c.
func NewPendingChecker(s store.DB, c clock.Clock) PendingChecker {
	return &pendingChecker{s: s, c: c}
}

// HasPending probes with a DedupePending Enqueue of an automatic consolidate (payload {}, no overrides,
// serial key "consolidate"): it joins exactly the joinable pending rows, which domain.Operation can't tell
// apart (it carries neither joinability nor scopes). A joined row counts when it is due (Queue.Get's
// NextRunAt not after now). The transaction is always rolled back, so a probe that inserted leaves nothing
// behind. A tombstoned bank has nothing pending.
func (p *pendingChecker) HasPending(ctx context.Context, b domain.BankRef, kind domain.OperationKind) (bool, error) {
	if kind != domain.OperationKindConsolidate {
		return false, fmt.Errorf("%w: pending probe supports consolidate only, not %s", domain.ErrInvalidArgument, kind)
	}
	var due bool
	var probeErr error
	err := p.s.InTx(ctx, func(tx store.Tx) error {
		id, joined, err := tx.Queue().Enqueue(ctx, store.Job{
			Bank: b, Kind: kind, SerialKey: consolidateSerialKey, Payload: json.RawMessage(`{}`),
			Dedupe: store.DedupePending,
		})
		if err != nil || !joined {
			probeErr = err
			return errProbeRollback
		}
		op, err := tx.Queue().Get(ctx, b, id)
		probeErr = err
		due = err == nil && (op.NextRunAt == nil || !op.NextRunAt.After(p.c.Now()))
		return errProbeRollback
	})
	switch {
	case errors.Is(probeErr, domain.ErrBankDeleting):
		return false, nil
	case probeErr != nil:
		return false, fmt.Errorf("pending %s probe: %w", kind, probeErr)
	case err != nil && !errors.Is(err, errProbeRollback):
		return false, fmt.Errorf("pending %s probe: %w", kind, err)
	}
	return due, nil
}
