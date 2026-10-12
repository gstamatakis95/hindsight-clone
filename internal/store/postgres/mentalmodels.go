package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
)

// mentalModelRepo is store.MentalModelRepo over one transaction. Row conversions live in
// mentalmodels_convert.go.
type mentalModelRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.MentalModelRepo = (*mentalModelRepo)(nil)

// Get returns the model; unknown = domain.ErrNotFound. Stale is left false: readers compute it.
func (r *mentalModelRepo) Get(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID,
) (domain.MentalModel, error) {
	row, err := r.q.MentalModelGet(ctx, db.MentalModelGetParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id)})
	if err != nil {
		return domain.MentalModel{}, mapErr(fmt.Sprintf("mental model %s", id), err)
	}
	return modelOf(row)
}

// Create inserts a new model; an existing id is domain.ErrAlreadyExists, an unknown bank domain.ErrNotFound.
func (r *mentalModelRepo) Create(ctx context.Context, m domain.MentalModel) error {
	p, err := modelParams(m)
	if err != nil {
		return err
	}
	return mapErr(fmt.Sprintf("create mental model %s", m.Spec.ID), r.q.MentalModelCreate(ctx, p))
}

// Update replaces the model's spec, content and refresh state; unknown = domain.ErrNotFound.
func (r *mentalModelRepo) Update(ctx context.Context, m domain.MentalModel) error {
	p, err := modelParams(m)
	if err != nil {
		return err
	}
	n, err := r.q.MentalModelUpdate(ctx, db.MentalModelUpdateParams{
		Name: p.Name, SourceQuery: p.SourceQuery, FolderPath: p.FolderPath, Tags: p.Tags, MaxTokens: p.MaxTokens,
		RefreshSettings: p.RefreshSettings, Overrides: p.Overrides, ContentMd: p.ContentMd,
		StructuredOutputJson: p.StructuredOutputJson,
		Version:              p.Version, LastRefreshedAt: p.LastRefreshedAt, LastSeqSeen: p.LastSeqSeen,
		LastRefreshFailedAt: p.LastRefreshFailedAt, AutoRefreshPaused: p.AutoRefreshPaused, SourceIds: p.SourceIds,
		LastTrace: p.LastTrace, Embedding: p.Embedding, RefreshCron: p.RefreshCron,
		TenantID: p.TenantID, OrgID: p.OrgID, BankID: p.BankID, ID: p.ID,
	})
	return docRowsErr(fmt.Sprintf("update mental model %s", m.Spec.ID), n, err)
}

// Delete removes the model and (by cascade) its history; unknown = domain.ErrNotFound.
func (r *mentalModelRepo) Delete(ctx context.Context, b domain.BankRef, id domain.MentalModelID) error {
	n, err := r.q.MentalModelDelete(ctx, db.MentalModelDeleteParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id)})
	return docRowsErr(fmt.Sprintf("delete mental model %s", id), n, err)
}

// AppendHistory adds an entry (a zero CreatedAt takes the database's now()), then keeps only the newest keep
// entries of that kind (keep <= 0 = all). An unknown model is domain.ErrNotFound.
func (r *mentalModelRepo) AppendHistory(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID, e domain.MentalModelHistoryEntry, keep int,
) error {
	var version *int64
	if e.Version != 0 {
		version = &e.Version
	}
	err := r.q.MentalModelAppendHistory(ctx, db.MentalModelAppendHistoryParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, MentalModelID: string(id), Kind: string(e.Kind), Version: version,
		PreviousContent: e.PreviousContent, Outcome: string(e.Outcome), FailureReason: e.FailureReason,
		ErrorMessage: e.ErrorMessage, ChangedAt: orNow(e.CreatedAt),
	})
	if err != nil {
		return mapErr(fmt.Sprintf("append history of mental model %s", id), err)
	}
	if keep <= 0 {
		return nil
	}
	err = r.q.MentalModelTrimHistory(ctx, db.MentalModelTrimHistoryParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, MentalModelID: string(id), Kind: string(e.Kind), Keep: toInt32Sat(keep),
	})
	return mapErr(fmt.Sprintf("trim history of mental model %s", id), err)
}

// History returns up to limit entries (limit <= 0 = all), newest first; kind unspecified = every kind. An
// unknown model has no entries.
func (r *mentalModelRepo) History(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID, kind domain.HistoryKind, limit int,
) ([]domain.MentalModelHistoryEntry, error) {
	rows, err := r.q.MentalModelHistory(ctx, db.MentalModelHistoryParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, MentalModelID: string(id), Kind: string(kind), Lim: toInt32Sat(limit),
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("history of mental model %s", id), err)
	}
	out := make([]domain.MentalModelHistoryEntry, len(rows))
	for i, row := range rows {
		out[i] = domain.MentalModelHistoryEntry{
			Kind: domain.HistoryKind(row.Kind), PreviousContent: row.PreviousContent, CreatedAt: row.ChangedAt,
			Outcome: domain.RefreshOutcome(row.Outcome), FailureReason: row.FailureReason, ErrorMessage: row.ErrorMessage,
		}
		if row.Version != nil {
			out[i].Version = *row.Version
		}
	}
	return out, nil
}
