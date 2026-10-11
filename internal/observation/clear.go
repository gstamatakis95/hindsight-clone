package observation

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
)

// ClearRequest selects the observations ClearObservations deletes: exactly one of Scopes, MemoryID and All.
type ClearRequest struct {
	// Bank holds the observations.
	Bank domain.BankRef
	// Scopes clears every observation of these scopes (tag sets, normalized here).
	Scopes []domain.TagSet
	// MemoryID clears the observations citing this live memory.
	MemoryID domain.UnitID
	// All clears every observation of the bank.
	All bool
	// Reconsolidate queues a consolidation of what was cleared: the same scopes, or the automatic job for All
	// and MemoryID.
	Reconsolidate bool
}

// ClearResult reports what a clear removed and the consolidation it queued.
type ClearResult struct {
	// Deleted counts the observations removed.
	Deleted int64
	// Operation is the queued consolidation; nil when Reconsolidate was false.
	Operation *domain.Operation
}

// ClearObservations deletes observations and resets their sources for re-consolidation, in one transaction
// that takes tx.Seq first and enqueues graph_maintenance and, with Reconsolidate, one consolidate. Scopes and
// All delete every observation of the scopes or the bank; MemoryID deletes the observations citing one live
// memory (absent = domain.ErrNotFound) and makes that memory consolidate again. Setting none or more than one
// of Scopes, MemoryID and All is domain.ErrInvalidArgument. No mental-model refresh is queued: affected models
// go stale through the change seq.
func (s *Service) ClearObservations(ctx context.Context, req ClearRequest) (ClearResult, error) {
	set := 0
	for _, ok := range []bool{len(req.Scopes) > 0, req.MemoryID != "", req.All} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return ClearResult{}, fmt.Errorf("%w: clear observations: set exactly one of scopes, memory id and all",
			domain.ErrInvalidArgument)
	}
	if req.MemoryID != "" {
		return s.clearMemory(ctx, req.Bank, req.MemoryID, req.Reconsolidate)
	}
	return s.clearScopes(ctx, req.Bank, req.Scopes, req.Reconsolidate)
}

// clearScopes deletes the observations of scopes (every scope of b when empty), resets their sources and,
// when reconsolidate, queues consolidation of the same scopes.
func (s *Service) clearScopes(
	ctx context.Context, b domain.BankRef, scopes []domain.TagSet, reconsolidate bool,
) (ClearResult, error) {
	scopes = normalizeScopes(scopes)
	keys := make([]domain.ScopeKey, len(scopes))
	for i, sc := range scopes {
		keys[i] = domain.NewScopeKey(b, sc.Tags)
	}
	var res ClearResult
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		res = ClearResult{}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		deleted, err := deleteScopes(ctx, tx, b, keys)
		if err != nil {
			return fmt.Errorf("clear scopes: %w", err)
		}
		res.Deleted = int64(len(deleted))
		if _, _, err := jobs.EnqueueGraph(ctx, tx, b); err != nil {
			return err
		}
		return reconsolidateInto(ctx, tx, b, scopes, reconsolidate, &res)
	})
	if err != nil {
		return ClearResult{}, fmt.Errorf("clear observations: %w", err)
	}
	return res, nil
}

// deleteScopes deletes every observation in scopes in the caller's transaction, resets their sources and
// returns the deleted observations. It takes tx.Seq (a clear changes memory content) before it deletes. An
// empty scopes list is every scope and maps explicitly to EvidenceRepo.DeleteAllObservations (DeleteScopes
// itself deletes nothing for an empty list), so callers must pass only scopes the client named, never a list
// derived from data, which could be empty. It enqueues nothing: the caller owns the consolidate job (decision
// 1005).
func deleteScopes(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.ScopeKey,
) ([]domain.UnitID, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return nil, fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	var deleted []domain.UnitID
	var err error
	if len(scopes) == 0 {
		deleted, _, err = tx.Evidence().DeleteAllObservations(ctx, b)
	} else {
		deleted, _, err = tx.Evidence().DeleteScopes(ctx, b, scopes)
	}
	if err != nil {
		return nil, fmt.Errorf("consolidate: clear scopes: %w", err)
	}
	return deleted, nil
}

// clearMemory deletes the observations citing one live memory and resets their co-sources.
func (s *Service) clearMemory(
	ctx context.Context, b domain.BankRef, id domain.UnitID, reconsolidate bool,
) (ClearResult, error) {
	var res ClearResult
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		res = ClearResult{}
		seq, err := tx.Seq(ctx, b)
		if err != nil {
			return err
		}
		us, err := tx.GetUnits(ctx, b, []domain.UnitID{id})
		if err != nil {
			return err
		}
		if len(us) == 0 {
			return fmt.Errorf("%w: memory %s", domain.ErrNotFound, id)
		}
		deleted, _, err := tx.Evidence().DeleteCiting(ctx, b, []domain.UnitID{id})
		if err != nil {
			return err
		}
		res.Deleted = int64(len(deleted))
		if err := resetOwnConsolidation(ctx, tx, us[0], seq); err != nil {
			return err
		}
		if _, _, err := jobs.EnqueueGraph(ctx, tx, b); err != nil {
			return err
		}
		return reconsolidateInto(ctx, tx, b, nil, reconsolidate, &res)
	})
	if err != nil {
		return ClearResult{}, fmt.Errorf("clear observations of memory %s: %w", id, err)
	}
	return res, nil
}

// reconsolidateInto enqueues the consolidation of scopes (every scope when empty) and records its operation on
// res when reconsolidate is set.
func reconsolidateInto(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.TagSet, reconsolidate bool, res *ClearResult,
) error {
	if !reconsolidate {
		return nil
	}
	op, err := enqueueConsolidation(ctx, tx, b, scopes, nil)
	if err != nil {
		return err
	}
	res.Operation = &op
	return nil
}

// resetOwnConsolidation makes a cleared fact consolidate again: UnitRepo.Update rewrites it unchanged
// (embedding kept, seq = the tx's) and clears consolidated_at and consolidation_failed_at. Observations
// have no consolidation stamps and are left alone. A fact without an embedding is waiting for its rederive,
// which clears the stamps when it writes the embedding, so it is left alone too: resetting it now would let
// consolidation decide on a NULL embedding.
func resetOwnConsolidation(ctx context.Context, tx store.Tx, u domain.Unit, seq int64) error {
	if u.Type == domain.FactTypeObservation || u.Embedding == nil {
		return nil
	}
	u.Seq = seq
	return tx.Units().Update(ctx, u)
}
