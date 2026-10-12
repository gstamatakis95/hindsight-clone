package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// evidenceRepo is store.EvidenceRepo over one transaction.
type evidenceRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.EvidenceRepo = (*evidenceRepo)(nil)

// Add records evidence for a live observation; an existing (observation, unit) pair is kept as is. An
// unknown observation or cited unit is domain.ErrNotFound.
func (r *evidenceRepo) Add(ctx context.Context, obs domain.UnitID, ev []domain.Evidence) error {
	if len(ev) == 0 {
		return nil
	}
	oid, err := parseID(obs)
	if err != nil {
		return err
	}
	bank, err := r.q.EvidenceObservationBank(ctx, oid)
	if err != nil {
		return mapErr("observation "+string(obs), err)
	}
	units := make([]uuid.UUID, len(ev))
	quotes := make([]string, len(ev))
	for i, e := range ev {
		if units[i], err = parseID(e.Unit); err != nil {
			return err
		}
		quotes[i] = e.Quote
	}
	err = r.q.EvidenceAdd(ctx, db.EvidenceAddParams{
		TenantID: bank.TenantID, OrgID: bank.OrgID, BankID: bank.BankID, ObservationID: oid, Quotes: quotes, UnitIds: units,
	})
	return mapErr("add evidence", err)
}

// Cites reports whether an observation of s's bank whose scope key is exactly s cites unit.
func (r *evidenceRepo) Cites(ctx context.Context, s domain.ScopeKey, unit domain.UnitID) (bool, error) {
	uid, err := parseID(unit)
	if err != nil {
		return false, err
	}
	ok, err := r.q.EvidenceCites(ctx, db.EvidenceCitesParams{
		UnitID: uid, TenantID: s.Tenant, OrgID: s.Org, BankID: s.Bank, ScopeKey: domain.NewScopeKey(s.BankRef, s.Tags).String(),
	})
	if err != nil {
		return false, mapErr("cites", err)
	}
	return ok, nil
}

// DeleteCiting locks the bank's observations citing any of ids FOR UPDATE in id order, then deletes them
// (their links, entities and evidence go as in UnitRepo.DeleteByDocument) and clears consolidated_at on
// their other sources, the cited ids excluded, so those are consolidated again. It returns the deleted
// observations and the reset sources, each sorted by id. consolidation_failed_at is left alone: a parked
// unit stays parked.
func (r *evidenceRepo) DeleteCiting(
	ctx context.Context, b domain.BankRef, ids []domain.UnitID,
) (deleted, reset []domain.UnitID, err error) {
	cited := parseIDs(ids)
	if len(cited) == 0 {
		return []domain.UnitID{}, []domain.UnitID{}, nil
	}
	obs, err := r.q.EvidenceCiting(ctx, db.EvidenceCitingParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Ids: cited})
	if err != nil {
		return nil, nil, mapErr("observations citing units", err)
	}
	return r.deleteObservations(ctx, b, obs, cited)
}

// deleteObservations deletes the bank's observations obs, which the caller has locked FOR UPDATE in id order
// (their links, entities and evidence go as in UnitRepo.DeleteByDocument), after reading their other sources
// (excluded left out); it clears consolidated_at on those sources so they are consolidated again, and
// returns obs and the reset sources, each sorted by id. consolidation_failed_at is left alone.
func (r *evidenceRepo) deleteObservations(
	ctx context.Context, b domain.BankRef, obs, excluded []uuid.UUID,
) (deleted, reset []domain.UnitID, err error) {
	if len(obs) == 0 {
		return []domain.UnitID{}, []domain.UnitID{}, nil
	}
	if excluded == nil {
		excluded = []uuid.UUID{} // pgx sends nil as NULL, and "<> ALL(NULL)" matches nothing
	}
	others, err := r.q.EvidenceOtherSources(ctx, db.EvidenceOtherSourcesParams{ObservationIds: obs, Excluded: excluded})
	if err != nil {
		return nil, nil, mapErr("other sources", err)
	}
	if err := removeUnits(ctx, r.q, b, obs); err != nil {
		return nil, nil, err
	}
	if len(others) > 0 {
		err = r.q.EvidenceResetSources(ctx, db.EvidenceResetSourcesParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Ids: others})
		if err != nil {
			return nil, nil, mapErr("reset sources", err)
		}
	}
	return idsOf[domain.UnitID](obs), idsOf[domain.UnitID](others), nil
}

// CitingScopes returns the distinct scopes of b's live observations citing any of ids, ordered by
// ScopeKey.String(); each scope's tags are decoded from its stored key, so they are normalized.
func (r *evidenceRepo) CitingScopes(
	ctx context.Context, b domain.BankRef, ids []domain.UnitID,
) ([]domain.ScopeKey, error) {
	cited := parseIDs(ids)
	if len(cited) == 0 {
		return []domain.ScopeKey{}, nil
	}
	keys, err := r.q.EvidenceCitingScopes(ctx, db.EvidenceCitingScopesParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Ids: cited,
	})
	if err != nil {
		return nil, mapErr("scopes citing units", err)
	}
	out := make([]domain.ScopeKey, len(keys))
	for i, k := range keys {
		if out[i], err = domain.ParseScopeKey(b, k); err != nil {
			return nil, fmt.Errorf("stored scope key: %w", err)
		}
	}
	return out, nil
}

// DeleteScopes deletes b's live observations whose scope is one of scopes (each matched under b by its
// normalized key, its own BankRef ignored). It locks them FOR UPDATE in id order before reading their
// sources, then deletes them and resets their sources as DeleteCiting does, with none excluded.
// observation_scopes rows are kept. An empty scopes deletes nothing.
func (r *evidenceRepo) DeleteScopes(
	ctx context.Context, b domain.BankRef, scopes []domain.ScopeKey,
) (deleted, reset []domain.UnitID, err error) {
	if len(scopes) == 0 {
		return []domain.UnitID{}, []domain.UnitID{}, nil
	}
	keys := make([]string, len(scopes))
	for i, s := range scopes {
		keys[i] = domain.NewScopeKey(b, s.Tags).String()
	}
	obs, err := r.q.EvidenceLockScopes(ctx, db.EvidenceLockScopesParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, Keys: keys})
	if err != nil {
		return nil, nil, mapErr("observations in scopes", err)
	}
	return r.deleteObservations(ctx, b, obs, nil)
}

// DeleteAllObservations is DeleteScopes over every live observation of b.
func (r *evidenceRepo) DeleteAllObservations(
	ctx context.Context, b domain.BankRef,
) (deleted, reset []domain.UnitID, err error) {
	obs, err := r.q.EvidenceLockAll(ctx, db.EvidenceLockAllParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank})
	if err != nil {
		return nil, nil, mapErr("observations of bank", err)
	}
	return r.deleteObservations(ctx, b, obs, nil)
}
