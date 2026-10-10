package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"

	"github.com/google/uuid"
)

// evidenceRepo is port.EvidenceRepo over one transaction.
type evidenceRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.EvidenceRepo = (*evidenceRepo)(nil)

// Add records evidence for a live observation; an existing (observation, unit) pair is kept as is. An
// unknown observation or cited unit is domain.ErrNotFound.
func (r *evidenceRepo) Add(ctx context.Context, obs port.UnitID, ev []domain.Evidence) error {
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
		TenantID: bank.TenantID, BankID: bank.BankID, ObservationID: oid, Quotes: quotes, UnitIds: units,
	})
	return mapErr("add evidence", err)
}

// Cites reports whether an observation of s's bank whose scope key is exactly s cites unit.
func (r *evidenceRepo) Cites(ctx context.Context, s port.ScopeKey, unit port.UnitID) (bool, error) {
	uid, err := parseID(unit)
	if err != nil {
		return false, err
	}
	ok, err := r.q.EvidenceCites(ctx, db.EvidenceCitesParams{
		UnitID: uid, TenantID: s.Tenant, BankID: s.Bank, ScopeKey: domain.NewScopeKey(s.BankRef, s.Tags).String(),
	})
	if err != nil {
		return false, mapErr("cites", err)
	}
	return ok, nil
}

// DeleteCiting deletes the bank's observations citing any of ids (their links, entities and evidence go as
// in UnitRepo.DeleteByDocument) and clears consolidated_at on their other sources, the cited ids excluded,
// so those are consolidated again. It returns the deleted observations and the reset sources, each sorted
// by id. consolidation_failed_at is left alone: a parked unit stays parked.
func (r *evidenceRepo) DeleteCiting(
	ctx context.Context, b port.BankRef, ids []port.UnitID,
) (deleted, reset []port.UnitID, err error) {
	cited := parseIDs(ids)
	if len(cited) == 0 {
		return []port.UnitID{}, []port.UnitID{}, nil
	}
	obs, err := r.q.EvidenceCiting(ctx, db.EvidenceCitingParams{TenantID: b.Tenant, BankID: b.Bank, Ids: cited})
	if err != nil {
		return nil, nil, mapErr("observations citing units", err)
	}
	if len(obs) == 0 {
		return []port.UnitID{}, []port.UnitID{}, nil
	}
	others, err := r.q.EvidenceOtherSources(ctx, db.EvidenceOtherSourcesParams{ObservationIds: obs, Excluded: cited})
	if err != nil {
		return nil, nil, mapErr("other sources", err)
	}
	if err := removeUnits(ctx, r.q, b, obs); err != nil {
		return nil, nil, err
	}
	if len(others) > 0 {
		err = r.q.EvidenceResetSources(ctx, db.EvidenceResetSourcesParams{TenantID: b.Tenant, BankID: b.Bank, Ids: others})
		if err != nil {
			return nil, nil, mapErr("reset sources", err)
		}
	}
	return idsOf[port.UnitID](obs), idsOf[port.UnitID](others), nil
}
