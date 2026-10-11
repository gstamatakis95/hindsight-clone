package fake

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// The CCR WP-15-1 Reader methods. Each one is a package-level function over a *state; the lock-free reader
// passes the latest committed snapshot and a transaction passes its private copy, so inside InTx they see
// the transaction's own uncommitted writes. Semantics are pinned by decision 1500.

// GetChunks returns b's chunks with the given ids, in the order of ids.
func (r reader) GetChunks(ctx context.Context, b port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error) {
	return getChunks(ctx, r.s.snapshot(), b, ids)
}

// EntityStates returns the entities of b attached to any of units, ordered by entity id.
func (r reader) EntityStates(
	ctx context.Context, b port.BankRef, units []port.UnitID,
) ([]domain.EntityState, error) {
	return entityStates(ctx, r.s.snapshot(), b, units)
}

// StaleScopes returns, in input order, those of scopes holding an unconsolidated fact of b.
func (r reader) StaleScopes(ctx context.Context, b port.BankRef, scopes []port.ScopeKey) ([]port.ScopeKey, error) {
	return staleScopes(ctx, r.s.snapshot(), b, scopes)
}

// TagCounts returns b's tags of source with their item counts, ordered by tag.
func (r reader) TagCounts(
	ctx context.Context, b port.BankRef, source domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	return tagCounts(ctx, r.s.snapshot(), b, source, prefix, after, limit)
}

// CountObservations returns how many live observations scope s holds.
func (r reader) CountObservations(ctx context.Context, s port.ScopeKey) (int, error) {
	return countObservations(ctx, r.s.snapshot(), s)
}

// ListScopes returns one page of b's observation scopes, ordered by scope key.
func (r reader) ListScopes(ctx context.Context, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error) {
	return listScopes(ctx, r.s.snapshot(), b, after, limit)
}

// ListMentalModels returns one page of q.Bank's models matching q and the next page token.
func (r reader) ListMentalModels(ctx context.Context, q port.ModelQuery) ([]domain.MentalModel, string, error) {
	return listMentalModels(ctx, r.s.snapshot(), q)
}

// NewestSeqInScope returns the highest seq of a live unit matching q, or 0.
func (r reader) NewestSeqInScope(ctx context.Context, q port.ScopeQuery) (int64, error) {
	return newestSeqInScope(ctx, r.s.snapshot(), q)
}

// CronModels returns every model with refresh_cron set on a bank that is not tombstoned.
func (r reader) CronModels(ctx context.Context) ([]port.CronModel, error) {
	return cronModels(ctx, r.s.snapshot())
}

// FailedRefreshes returns the models whose latest terminal refresh operation failed.
func (r reader) FailedRefreshes(ctx context.Context) ([]port.FailedRefresh, error) {
	return failedRefreshes(ctx, r.s.snapshot())
}

// GetUnit returns one live or archived unit of b.
func (r reader) GetUnit(ctx context.Context, b port.BankRef, id port.UnitID) (domain.Unit, error) {
	return getUnit(ctx, r.s.snapshot(), b, id)
}

// ListUnits returns one page of units matching q, newest first.
func (r reader) ListUnits(ctx context.Context, q port.UnitQuery) ([]domain.Unit, error) {
	return listUnits(ctx, r.s.snapshot(), q)
}

// ObservationHistory returns the versions of observation id, newest first.
func (r reader) ObservationHistory(
	ctx context.Context, b port.BankRef, id port.UnitID,
) ([]domain.ObservationVersion, error) {
	return observationHistory(ctx, r.s.snapshot(), b, id)
}

// BankStats returns b's counts.
func (r reader) BankStats(ctx context.Context, b port.BankRef) (domain.BankStats, error) {
	return bankStats(ctx, r.s.snapshot(), b)
}

// ListDocuments returns one page of documents matching q, newest first.
func (r reader) ListDocuments(ctx context.Context, q port.DocumentQuery) ([]domain.Document, error) {
	return listDocuments(ctx, r.s.snapshot(), q)
}

// ModelsCiting returns the ids of b's models whose source ids include any of ids, sorted.
func (r reader) ModelsCiting(
	ctx context.Context, b port.BankRef, ids []port.UnitID,
) ([]port.MentalModelID, error) {
	return modelsCiting(ctx, r.s.snapshot(), b, ids)
}

// BanksToConsolidate returns every bank that is not tombstoned and has a consolidation candidate.
func (r reader) BanksToConsolidate(ctx context.Context) ([]port.BankRef, error) {
	return banksToConsolidate(ctx, r.s.snapshot())
}

// GetChunks is Reader.GetChunks over this transaction's view.
func (t *tx) GetChunks(ctx context.Context, b port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error) {
	return getChunks(ctx, t.st, b, ids)
}

// EntityStates is Reader.EntityStates over this transaction's view.
func (t *tx) EntityStates(ctx context.Context, b port.BankRef, units []port.UnitID) ([]domain.EntityState, error) {
	return entityStates(ctx, t.st, b, units)
}

// StaleScopes is Reader.StaleScopes over this transaction's view.
func (t *tx) StaleScopes(ctx context.Context, b port.BankRef, scopes []port.ScopeKey) ([]port.ScopeKey, error) {
	return staleScopes(ctx, t.st, b, scopes)
}

// TagCounts is Reader.TagCounts over this transaction's view.
func (t *tx) TagCounts(
	ctx context.Context, b port.BankRef, source domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	return tagCounts(ctx, t.st, b, source, prefix, after, limit)
}

// CountObservations is Reader.CountObservations over this transaction's view.
func (t *tx) CountObservations(ctx context.Context, s port.ScopeKey) (int, error) {
	return countObservations(ctx, t.st, s)
}

// ListScopes is Reader.ListScopes over this transaction's view.
func (t *tx) ListScopes(ctx context.Context, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error) {
	return listScopes(ctx, t.st, b, after, limit)
}

// ListMentalModels is Reader.ListMentalModels over this transaction's view.
func (t *tx) ListMentalModels(ctx context.Context, q port.ModelQuery) ([]domain.MentalModel, string, error) {
	return listMentalModels(ctx, t.st, q)
}

// NewestSeqInScope is Reader.NewestSeqInScope over this transaction's view.
func (t *tx) NewestSeqInScope(ctx context.Context, q port.ScopeQuery) (int64, error) {
	return newestSeqInScope(ctx, t.st, q)
}

// CronModels is Reader.CronModels over this transaction's view.
func (t *tx) CronModels(ctx context.Context) ([]port.CronModel, error) {
	return cronModels(ctx, t.st)
}

// FailedRefreshes is Reader.FailedRefreshes over this transaction's view.
func (t *tx) FailedRefreshes(ctx context.Context) ([]port.FailedRefresh, error) {
	return failedRefreshes(ctx, t.st)
}

// GetUnit is Reader.GetUnit over this transaction's view.
func (t *tx) GetUnit(ctx context.Context, b port.BankRef, id port.UnitID) (domain.Unit, error) {
	return getUnit(ctx, t.st, b, id)
}

// ListUnits is Reader.ListUnits over this transaction's view.
func (t *tx) ListUnits(ctx context.Context, q port.UnitQuery) ([]domain.Unit, error) {
	return listUnits(ctx, t.st, q)
}

// ObservationHistory is Reader.ObservationHistory over this transaction's view.
func (t *tx) ObservationHistory(
	ctx context.Context, b port.BankRef, id port.UnitID,
) ([]domain.ObservationVersion, error) {
	return observationHistory(ctx, t.st, b, id)
}

// BankStats is Reader.BankStats over this transaction's view.
func (t *tx) BankStats(ctx context.Context, b port.BankRef) (domain.BankStats, error) {
	return bankStats(ctx, t.st, b)
}

// ListDocuments is Reader.ListDocuments over this transaction's view.
func (t *tx) ListDocuments(ctx context.Context, q port.DocumentQuery) ([]domain.Document, error) {
	return listDocuments(ctx, t.st, q)
}

// ModelsCiting is Reader.ModelsCiting over this transaction's view.
func (t *tx) ModelsCiting(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]port.MentalModelID, error) {
	return modelsCiting(ctx, t.st, b, ids)
}

// BanksToConsolidate is Reader.BanksToConsolidate over this transaction's view.
func (t *tx) BanksToConsolidate(ctx context.Context) ([]port.BankRef, error) {
	return banksToConsolidate(ctx, t.st)
}

// liveBank fails with domain.ErrNotFound unless b exists and is not tombstoned (the listings and stats).
func liveBank(ctx context.Context, st *state, b port.BankRef) error {
	if err := ctx.Err(); err != nil {
		return ctxErr(err)
	}
	row, ok := st.banks[b]
	if !ok || row.bank.Deleting() {
		return notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	return nil
}

// checkLimit fails with domain.ErrInvalidArgument unless limit > 0.
func checkLimit(what string, limit int) error {
	if limit <= 0 {
		return fmt.Errorf("%w: %s limit %d must be > 0", domain.ErrInvalidArgument, what, limit)
	}
	return nil
}

// bankLive reports whether b exists and is not tombstoned.
func (s *state) bankLive(b port.BankRef) bool {
	row, ok := s.banks[b]
	return ok && !row.bank.Deleting()
}

// inScope reports whether tags contain every tag of scope (normalized); the shared scope holds everything.
func inScope(tags, scope []string) bool {
	return containsAll(domain.NormalizeTags(tags), domain.NormalizeTags(scope))
}

// countUnconsolidated counts b's unconsolidated facts in the scope of tags.
func (s *state) countUnconsolidated(b port.BankRef, tags []string) int64 {
	var n int64
	for _, row := range s.units {
		if row.u.Bank == b && unconsolidated(row.u) && inScope(row.u.Tags, tags) {
			n++
		}
	}
	return n
}
