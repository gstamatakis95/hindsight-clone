package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// GetUnit returns one unit of b: live (State valid) when it is in memory_units, else archived (State
// invalidated); anything else, and an unknown or tombstoned bank, is domain.ErrNotFound.
func (r *reader) GetUnit(ctx context.Context, b domain.BankRef, id domain.UnitID) (domain.Unit, error) {
	if err := r.listedBank(ctx, b); err != nil {
		return domain.Unit{}, err
	}
	uid, err := parseID(id)
	if err != nil {
		return domain.Unit{}, err
	}
	p := unitListBase(b)
	p.IncludeLive, p.IncludeArchived = true, true
	p.UnitID = uuid.NullUUID{UUID: uid, Valid: true}
	p.Lim = 1 // the live row sorts first when an id is in both tables
	units, err := r.listUnits(ctx, p)
	if err != nil {
		return domain.Unit{}, err
	}
	if len(units) == 0 {
		return domain.Unit{}, fmt.Errorf("%w: unit %s", domain.ErrNotFound, id)
	}
	return units[0], nil
}

// ListUnits returns up to q.Limit (> 0) units matching q, newest first ((created_at, id) descending) and
// strictly after q.After, each filled like GetUnit. A malformed EntityID matches no unit.
func (r *reader) ListUnits(ctx context.Context, q store.UnitQuery) ([]domain.Unit, error) {
	if q.Limit <= 0 {
		return nil, fmt.Errorf("%w: limit %d must be positive", domain.ErrInvalidArgument, q.Limit)
	}
	p, err := unitListParams(q)
	if err != nil {
		return nil, err
	}
	if err := r.listedBank(ctx, q.Bank); err != nil {
		return nil, err
	}
	if q.EntityID != "" && !p.EntityID.Valid {
		return []domain.Unit{}, nil
	}
	return r.listUnits(ctx, p)
}

// unitListBase returns ReadsListUnits parameters for bank b with no filter set; the caller picks the tables and
// the limit.
func unitListBase(b domain.BankRef) db.ReadsListUnitsParams {
	return db.ReadsListUnitsParams{
		TenantID: b.Tenant, BankID: b.Bank, Types: []string{}, TimeField: string(domain.TimeFieldCreatedAt),
		TagsMatch: string(domain.TagsMatchAny), FilterTags: []string{},
	}
}

// unitListParams maps q onto ReadsListUnits parameters. An unknown State or TimeField and a malformed cursor id
// are domain.ErrInvalidArgument; a malformed EntityID leaves EntityID invalid (the caller returns no units).
func unitListParams(q store.UnitQuery) (db.ReadsListUnitsParams, error) {
	p := unitListBase(q.Bank)
	switch q.State {
	case domain.MemoryStateUnspecified:
		p.IncludeLive, p.IncludeArchived = true, true
	case domain.MemoryStateValid:
		p.IncludeLive = true
	case domain.MemoryStateInvalidated:
		p.IncludeArchived = true
	default:
		return p, fmt.Errorf("%w: unknown memory state %q", domain.ErrInvalidArgument, q.State)
	}
	switch q.TimeField {
	case domain.TimeFieldUnspecified:
	case domain.TimeFieldCreatedAt, domain.TimeFieldUpdatedAt, domain.TimeFieldMentionedAt,
		domain.TimeFieldOccurredStart, domain.TimeFieldOccurredEnd:
		p.TimeField = string(q.TimeField)
	default:
		return p, fmt.Errorf("%w: unknown time field %q", domain.ErrInvalidArgument, q.TimeField)
	}
	if q.After != nil {
		id, err := uuid.Parse(string(q.After.ID))
		if err != nil {
			return p, fmt.Errorf("%w: cursor id %q is not a UUID", domain.ErrInvalidArgument, q.After.ID)
		}
		p.AfterCreatedAt, p.AfterID = &q.After.CreatedAt, id
	}
	if q.EntityID != "" {
		if id, err := uuid.Parse(string(q.EntityID)); err == nil {
			p.EntityID = uuid.NullUUID{UUID: id, Valid: true}
		}
	}
	for _, t := range q.Types {
		p.Types = append(p.Types, string(t))
	}
	p.TextLike = containsPattern(q.Q)
	p.DocumentID = textPtr(q.DocumentID)
	p.StartAt, p.EndAt = q.Start, q.End
	p.TagsMatch, p.FilterTags = tagFilterArgs(q.Tags, q.TagsMatch)
	p.Lim = toInt32Sat(q.Limit)
	return p, nil
}

// listUnits runs ReadsListUnits and converts its rows.
func (r *reader) listUnits(ctx context.Context, p db.ReadsListUnitsParams) ([]domain.Unit, error) {
	rows, err := r.queries().ReadsListUnits(ctx, p)
	if err != nil {
		return nil, mapErr("list units", err)
	}
	out := make([]domain.Unit, len(rows))
	for i, row := range rows {
		if out[i], err = unitOfListRow(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// unitOfListRow builds a domain.Unit from a ReadsListUnits row: live or archived (the SQL already maps the
// archive's missing columns to zero values and its archived_at to updated_at), with Entities, SourceIDs and
// Stale as the row carries them.
func unitOfListRow(row db.ReadsListUnitsRow) (domain.Unit, error) {
	u, err := unitOf(db.MemoryUnit{
		ID: row.ID, TenantID: row.TenantID, BankID: row.BankID, DocumentID: row.DocumentID, ChunkID: row.ChunkID,
		FactType: row.FactType, Text: row.Text, Context: row.Context, Metadata: row.Metadata, Tags: row.Tags,
		Embedding: row.Embedding, OccurredStart: row.OccurredStart, OccurredEnd: row.OccurredEnd,
		MentionedAt: row.MentionedAt, Timeless: row.Timeless, ProofCount: row.ProofCount, Version: row.Version,
		Seq: row.Seq, ConsolidatedAt: row.ConsolidatedAt, ConsolidationFailedAt: row.ConsolidationFailedAt,
		EditedAt: row.EditedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, row.EntityNames, row.SourceIds)
	if err != nil {
		return domain.Unit{}, err
	}
	u.Stale = row.Stale
	if !row.Live {
		u.State = domain.MemoryStateInvalidated
	}
	return u, nil
}
