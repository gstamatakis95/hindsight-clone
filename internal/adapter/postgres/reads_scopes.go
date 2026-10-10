package postgres

import (
	"context"
	"fmt"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// Reader methods over chunks, entities, observation scopes and consolidation candidates (CCR WP-15-1,
// decision 1500). Each runs on the reader's connection, so on a Tx it sees the transaction's own writes.

// GetChunks returns b's chunks with the given ids in the order of ids; unknown, malformed and other-bank ids
// are skipped and a repeated id is returned once.
func (r *reader) GetChunks(ctx context.Context, b port.BankRef, ids []port.ChunkID) ([]domain.Chunk, error) {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return []domain.Chunk{}, nil
	}
	rows, err := r.queries().ChunkGetMany(ctx, db.ChunkGetManyParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return nil, mapErr("get chunks", err)
	}
	byID := make(map[port.ChunkID]domain.Chunk, len(rows))
	for _, row := range rows {
		c := domain.Chunk{
			ID: idOf[port.ChunkID](row.ID), Bank: port.BankRef{Tenant: row.TenantID, Bank: row.BankID},
			DocumentID: port.DocumentID(row.DocumentID), Index: int(row.ChunkIndex), Text: row.Text,
			ContentHash: row.ContentHash, TokenCount: int(row.TokenCount), CreatedAt: row.CreatedAt,
		}
		byID[c.ID] = c
	}
	out := make([]domain.Chunk, 0, len(rows))
	for _, id := range uuids {
		if c, ok := byID[idOf[port.ChunkID](id)]; ok {
			out = append(out, c)
			delete(byID, c.ID) // a repeated id is returned once
		}
	}
	return out, nil
}

// EntityStates returns the entities attached to any of b's live units in units, ordered by id, each with
// the sorted live observations attached to it or citing a live fact attached to it.
func (r *reader) EntityStates(ctx context.Context, b port.BankRef, units []port.UnitID) ([]domain.EntityState, error) {
	uuids := parseIDs(units)
	if len(uuids) == 0 {
		return []domain.EntityState{}, nil
	}
	rows, err := r.queries().EntityStatesOf(ctx, db.EntityStatesOfParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return nil, mapErr("entity states", err)
	}
	out := make([]domain.EntityState, len(rows))
	for i, row := range rows {
		out[i] = domain.EntityState{
			EntityID: idOf[port.EntityID](row.ID), CanonicalName: row.CanonicalName,
			ObservationIDs: idsOf[port.UnitID](row.ObservationIds),
		}
	}
	return out, nil
}

// unconsolidatedCounts returns, per scope and in scope order, how many of b's unconsolidated facts are in it
// (CCR §0: tags contain every tag of the scope). The scopes' own banks are ignored.
func (r *reader) unconsolidatedCounts(ctx context.Context, b port.BankRef, scopes [][]string) ([]int64, error) {
	out := make([]int64, len(scopes))
	if len(scopes) == 0 {
		return out, nil
	}
	sets := make([][]string, len(scopes))
	for i, tags := range scopes {
		sets[i] = tagsOrEmpty(domain.NormalizeTags(tags))
	}
	raw, err := marshal("scopes", sets)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries().ScopeUnconsolidated(ctx, db.ScopeUnconsolidatedParams{
		TenantID: b.Tenant, BankID: b.Bank, Scopes: raw,
	})
	if err != nil {
		return nil, mapErr("unconsolidated units in scopes", err)
	}
	for _, row := range rows {
		if i := int(row.Ord) - 1; i >= 0 && i < len(out) {
			out[i] = row.N
		}
	}
	return out, nil
}

// StaleScopes returns, in input order and with duplicates kept, the scopes holding an unconsolidated fact of
// b; each scope is evaluated against b, whatever its own BankRef.
func (r *reader) StaleScopes(ctx context.Context, b port.BankRef, scopes []port.ScopeKey) ([]port.ScopeKey, error) {
	tags := make([][]string, len(scopes))
	for i, s := range scopes {
		tags[i] = s.Tags
	}
	counts, err := r.unconsolidatedCounts(ctx, b, tags)
	if err != nil {
		return nil, err
	}
	out := make([]port.ScopeKey, 0, len(scopes))
	for i, s := range scopes {
		if counts[i] > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// CountObservations returns how many live observations of s's bank have exactly s's tag set; 0 for an
// unknown scope or bank.
func (r *reader) CountObservations(ctx context.Context, s port.ScopeKey) (int, error) {
	n, err := r.queries().ScopeObservationCount(ctx, db.ScopeObservationCountParams{
		TenantID: s.Tenant, BankID: s.Bank, ScopeKey: domain.NewScopeKey(s.BankRef, s.Tags).String(),
	})
	if err != nil {
		return 0, mapErr("count observations", err)
	}
	return int(n), nil
}

// ListScopes returns up to limit of b's observation_scopes rows with a key strictly after after ("" = from
// the start), in byte order, with their live observation and unconsolidated fact counts. limit <= 0 is
// domain.ErrInvalidArgument; an unknown or tombstoned bank is domain.ErrNotFound.
func (r *reader) ListScopes(ctx context.Context, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: list scopes limit %d, want > 0", domain.ErrInvalidArgument, limit)
	}
	if err := r.scopesBankListable(ctx, b); err != nil {
		return nil, err
	}
	rows, err := r.queries().ScopeListPage(ctx, db.ScopeListPageParams{
		TenantID: b.Tenant, BankID: b.Bank, After: after, Lim: toInt32Sat(limit),
	})
	if err != nil {
		return nil, mapErr("list scopes", err)
	}
	tags := make([][]string, len(rows))
	for i, row := range rows {
		tags[i] = tagsOrEmpty(row.Tags)
	}
	counts, err := r.unconsolidatedCounts(ctx, b, tags)
	if err != nil {
		return nil, err
	}
	out := make([]domain.ScopeInfo, len(rows))
	for i, row := range rows {
		out[i] = domain.ScopeInfo{
			Scope: domain.TagSet{Tags: tags[i]}, Observations: row.Observations, UnconsolidatedUnits: counts[i],
		}
	}
	return out, nil
}

// scopesBankListable returns domain.ErrNotFound for an unknown or tombstoned bank (decision 1500).
func (r *reader) scopesBankListable(ctx context.Context, b port.BankRef) error {
	bank, err := r.queries().GetBank(ctx, db.GetBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return mapErr(fmt.Sprintf("bank %s/%s", b.Tenant, b.Bank), err)
	}
	if bank.DeletingAt != nil {
		return fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
	}
	return nil
}

// NewestSeqInScope returns the highest seq of q.Bank's live units matching q's types, tag predicate and seq
// range, or 0 when none matches. The tag predicate is a variable tree, so the SQL is built at run time with
// the recall arms' renderer (predicate.go); every value is a bind parameter.
func (r *reader) NewestSeqInScope(ctx context.Context, q port.ScopeQuery) (int64, error) {
	var a sqlArgs
	where := unitFilter{bank: q.Bank, types: q.Types, tags: q.Tags, seq: q.Seq}.render("", &a)
	sql := "SELECT COALESCE(max(seq), 0)::bigint FROM memory_units WHERE " + where
	var seq int64
	if err := r.conn.QueryRow(ctx, sql, a.vals...).Scan(&seq); err != nil {
		return 0, mapErr("newest seq in scope", err)
	}
	return seq, nil
}

// ModelsCiting returns the ids of b's mental models whose source_ids include any of ids, distinct and in
// byte order.
func (r *reader) ModelsCiting(ctx context.Context, b port.BankRef, ids []port.UnitID) ([]port.MentalModelID, error) {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return []port.MentalModelID{}, nil
	}
	rows, err := r.queries().ModelsCitingUnits(ctx, db.ModelsCitingUnitsParams{
		TenantID: b.Tenant, BankID: b.Bank, Ids: uuids,
	})
	if err != nil {
		return nil, mapErr("models citing units", err)
	}
	out := make([]port.MentalModelID, len(rows))
	for i, id := range rows {
		out[i] = port.MentalModelID(id)
	}
	return out, nil
}

// BanksToConsolidate returns, ordered by (tenant, bank), every bank that is not tombstoned and holds an
// unconsolidated fact with an embedding (decision 1500).
func (r *reader) BanksToConsolidate(ctx context.Context) ([]port.BankRef, error) {
	rows, err := r.queries().BanksToConsolidate(ctx)
	if err != nil {
		return nil, mapErr("banks to consolidate", err)
	}
	out := make([]port.BankRef, len(rows))
	for i, row := range rows {
		out[i] = port.BankRef{Tenant: row.TenantID, Bank: row.ID}
	}
	return out, nil
}
