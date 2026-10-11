package curate

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
)

// observationAdmin implements ObservationAdmin.
type observationAdmin struct {
	d ObservationAdminDeps
}

var _ ObservationAdmin = (*observationAdmin)(nil)

// NewObservationAdmin returns the ObservationAdmin.
func NewObservationAdmin(d ObservationAdminDeps) ObservationAdmin {
	return &observationAdmin{d: d}
}

// normalizeScopes returns scopes with normalized tags.
func normalizeScopes(scopes []domain.TagSet) []domain.TagSet {
	out := make([]domain.TagSet, len(scopes))
	for i, s := range scopes {
		out[i] = domain.TagSet{Tags: domain.NormalizeTags(s.Tags)}
	}
	return out
}

// enqueueConsolidation enqueues a consolidate in tx (jobs.EnqueueScopedConsolidate: {} with DedupePending when
// automatic, the scopes payload with DedupeNone when it carries scopes or overrides) and returns its
// operation row.
func enqueueConsolidation(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (domain.Operation, error) {
	id, _, err := jobs.EnqueueScopedConsolidate(ctx, tx, b, scopes, overrides)
	if err != nil {
		return domain.Operation{}, err
	}
	return tx.Queue().Get(ctx, b, id)
}

// Consolidate queues a manual consolidation; it is a write, so an unknown bank is created.
func (a *observationAdmin) Consolidate(
	ctx context.Context, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (domain.Operation, error) {
	scopes = normalizeScopes(scopes)
	var op domain.Operation
	err := a.d.Store.InTx(ctx, func(tx store.Tx) error {
		op = domain.Operation{}
		if _, err := tx.Banks().Ensure(ctx, b); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		var err error
		op, err = enqueueConsolidation(ctx, tx, b, scopes, overrides)
		return err
	})
	if err != nil {
		return domain.Operation{}, fmt.Errorf("consolidate: %w", err)
	}
	return op, nil
}

// ListScopes returns one page of scopes, keyed by the last scope's canonical string.
func (a *observationAdmin) ListScopes(
	ctx context.Context, b domain.BankRef, size int, token string,
) (ScopePage, error) {
	var after string
	if token != "" {
		if err := decodeToken(token, &after); err != nil {
			return ScopePage{}, err
		}
	}
	size = pageSize(size)
	items, err := a.d.Scopes.ListScopes(ctx, b, after, size+1)
	if err != nil {
		return ScopePage{}, fmt.Errorf("list scopes: %w", err)
	}
	page := ScopePage{Items: items}
	if len(items) > size {
		page.Items = items[:size]
		if page.NextPageToken, err = encodeToken(scopeCursor(b, page.Items[size-1])); err != nil {
			return ScopePage{}, err
		}
	}
	return page, nil
}

// scopeCursor is the keyset cursor after scope s: its canonical key, except that the shared scope's key ""
// would restart the listing, so it maps to "0", which sorts before every tagged key (decision 1560).
func scopeCursor(b domain.BankRef, s domain.ScopeInfo) string {
	if key := domain.NewScopeKey(b, s.Scope.Tags).String(); key != "" {
		return key
	}
	return "0"
}

// ClearObservations deletes the observations of scopes (all when empty), resets their sources and, when
// reconsolidate, queues consolidation of the same scopes.
func (a *observationAdmin) ClearObservations(
	ctx context.Context, b domain.BankRef, scopes []domain.TagSet, reconsolidate bool,
) (ClearResult, error) {
	scopes = normalizeScopes(scopes)
	keys := make([]domain.ScopeKey, len(scopes))
	for i, s := range scopes {
		keys[i] = domain.NewScopeKey(b, s.Tags)
	}
	var res ClearResult
	err := a.d.Store.InTx(ctx, func(tx store.Tx) error {
		res = ClearResult{}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		deleted, err := a.d.Clearer.ClearScopes(ctx, tx, b, keys)
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

// ClearMemoryObservations deletes the observations citing one live memory and resets their co-sources.
func (a *observationAdmin) ClearMemoryObservations(
	ctx context.Context, b domain.BankRef, id domain.UnitID, reconsolidate bool,
) (ClearResult, error) {
	var res ClearResult
	err := a.d.Store.InTx(ctx, func(tx store.Tx) error {
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

// resetOwnConsolidation makes a cleared fact consolidate again: UnitRepo.Update rewrites it unchanged
// (embedding kept, seq = the tx's) and clears consolidated_at and consolidation_failed_at. Observations
// have no consolidation stamps and are left alone. A fact without an embedding is waiting for its rederive,
// which clears the stamps when it writes the embedding, so it is left alone too: resetting it now would let
// consolidation decide on a NULL embedding (decision 1305).
func resetOwnConsolidation(ctx context.Context, tx store.Tx, u domain.Unit, seq int64) error {
	if u.Type == domain.FactTypeObservation || u.Embedding == nil {
		return nil
	}
	u.Seq = seq
	return tx.Units().Update(ctx, u)
}
