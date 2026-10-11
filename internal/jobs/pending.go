package jobs

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
)

// pendingChecker tells the runner whether a bank already has a due automatic consolidate, so a failed
// consolidate skips its retry when another consolidation will pick up the same units.
type pendingChecker interface {
	// hasPending reports whether b has a PENDING, joinable (automatic: no overrides, no scopes) consolidate
	// that is due now.
	hasPending(ctx context.Context, b domain.BankRef) (bool, error)
}

// consolidateProbe implements pendingChecker with a probe transaction that is always rolled back.
type consolidateProbe struct {
	s store.DB
	c clock.Clock
}

// errProbeRollback rolls the probe transaction back.
var errProbeRollback = errors.New("pending probe: roll back")

// hasPending probes with EnqueueConsolidate (the automatic consolidate: payload {}, no overrides,
// DedupePending): it joins exactly the joinable pending rows, which domain.Operation can't tell apart (it
// carries neither joinability nor scopes). A joined row counts when it is due (Queue.Get's NextRunAt not
// after now). The transaction is always rolled back, so a probe that inserted leaves nothing behind. A
// tombstoned bank has nothing pending.
func (p *consolidateProbe) hasPending(ctx context.Context, b domain.BankRef) (bool, error) {
	var due bool
	var probeErr error
	err := p.s.InTx(ctx, func(tx store.Tx) error {
		due, probeErr = false, nil
		id, joined, err := EnqueueConsolidate(ctx, tx, b)
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
		return false, fmt.Errorf("pending consolidate probe: %w", probeErr)
	case err != nil && !errors.Is(err, errProbeRollback):
		return false, fmt.Errorf("pending consolidate probe: %w", err)
	}
	return due, nil
}
