package postgres

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// modelParams maps a mental model onto its columns: the spec's trigger and overrides become JSON,
// refresh_cron mirrors trigger.refresh_cron, and empty optional values become NULL. IsStale is not stored
// (readers compute it). A malformed source id is domain.ErrInvalidArgument.
func modelParams(m domain.MentalModel) (db.MentalModelCreateParams, error) {
	trigger, err := marshal("mental model trigger", m.Spec.Trigger)
	if err != nil {
		return db.MentalModelCreateParams{}, err
	}
	overrides, err := marshalPtr("mental model overrides", m.Spec.Overrides)
	if err != nil {
		return db.MentalModelCreateParams{}, err
	}
	trace, err := marshalPtr("mental model trace", m.LastTrace)
	if err != nil {
		return db.MentalModelCreateParams{}, err
	}
	sources, err := modelSourceIDs(m.SourceIDs)
	if err != nil {
		return db.MentalModelCreateParams{}, err
	}
	var maxTokens *int32
	if m.Spec.MaxTokens != nil {
		v := toInt32Sat(*m.Spec.MaxTokens)
		maxTokens = &v
	}
	var structured []byte
	if len(m.StructuredOutputJSON) > 0 {
		structured = m.StructuredOutputJSON
	}
	return db.MentalModelCreateParams{
		TenantID: m.Bank.Tenant, BankID: m.Bank.Bank, ID: string(m.Spec.ID), Name: m.Spec.Name,
		SourceQuery: m.Spec.SourceQuery, FolderPath: m.Spec.FolderPath, Tags: tagsOrEmpty(m.Spec.Tags),
		MaxTokens: maxTokens, Trigger: trigger, Overrides: overrides, ContentMd: m.ContentMD,
		StructuredOutputJson: structured, Version: m.Version, LastRefreshedAt: m.LastRefreshedAt,
		LastSeqSeen: m.LastSeqSeen, LastRefreshFailedAt: m.LastRefreshFailedAt, AutoRefreshPaused: m.AutoRefreshPaused,
		SourceIds: sources, LastTrace: trace, Embedding: vec(m.Embedding), RefreshCron: textPtr(m.Spec.Trigger.RefreshCron),
	}, nil
}

// modelSourceIDs parses a model's source ids strictly: a malformed one is domain.ErrInvalidArgument rather
// than silently dropped.
func modelSourceIDs(ids []domain.UnitID) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, len(ids))
	for i, id := range ids {
		u, err := uuid.Parse(string(id))
		if err != nil {
			return nil, fmt.Errorf("%w: source id %q is not a UUID", domain.ErrInvalidArgument, id)
		}
		out[i] = u
	}
	return out, nil
}

// modelOf builds a domain.MentalModel from its row.
func modelOf(r db.MentalModel) (domain.MentalModel, error) {
	m := domain.MentalModel{
		Spec: domain.MentalModelSpec{
			ID: domain.MentalModelID(r.ID), Name: r.Name, SourceQuery: r.SourceQuery, FolderPath: r.FolderPath,
			Tags: r.Tags,
		},
		Bank: domain.BankRef{Tenant: r.TenantID, Bank: r.BankID}, ContentMD: r.ContentMd,
		StructuredOutputJSON: r.StructuredOutputJson, Version: r.Version, LastRefreshedAt: r.LastRefreshedAt,
		LastSeqSeen: r.LastSeqSeen, LastRefreshFailedAt: r.LastRefreshFailedAt, AutoRefreshPaused: r.AutoRefreshPaused,
		SourceIDs: idsOf[domain.UnitID](r.SourceIds), Embedding: vecOf(r.Embedding),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.MaxTokens != nil {
		v := int(*r.MaxTokens)
		m.Spec.MaxTokens = &v
	}
	if err := unmarshal("mental model trigger", r.Trigger, &m.Spec.Trigger); err != nil {
		return domain.MentalModel{}, err
	}
	var err error
	if m.Spec.Overrides, err = unmarshalPtr[domain.BankConfig]("mental model overrides", r.Overrides); err != nil {
		return domain.MentalModel{}, err
	}
	if m.LastTrace, err = unmarshalPtr[domain.RefreshTrace]("mental model trace", r.LastTrace); err != nil {
		return domain.MentalModel{}, err
	}
	return m, nil
}
