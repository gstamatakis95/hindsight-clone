package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// unitRepo is port.UnitRepo over one transaction.
type unitRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.UnitRepo = (*unitRepo)(nil)

// Insert stores new units exactly as given, including their Seq stamp. A zero CreatedAt takes the
// database's now(); an observation's scope_key is the encoding of its tags.
func (r *unitRepo) Insert(ctx context.Context, us []domain.Unit) error {
	for i := range us {
		p, err := unitInsertParams(us[i])
		if err != nil {
			return err
		}
		if err := r.q.UnitInsert(ctx, p); err != nil {
			return mapErr("insert unit", err)
		}
	}
	return nil
}

// unitInsertParams maps a unit to its insert parameters.
func unitInsertParams(u domain.Unit) (db.UnitInsertParams, error) {
	id, err := parseNewID(u.ID)
	if err != nil {
		return db.UnitInsertParams{}, err
	}
	chunk, err := nullID(u.ChunkID)
	if err != nil {
		return db.UnitInsertParams{}, err
	}
	meta := u.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	rawMeta, err := marshal("unit metadata", meta)
	if err != nil {
		return db.UnitInsertParams{}, err
	}
	var scope *string
	if u.Type == domain.FactTypeObservation {
		key := domain.NewScopeKey(u.Bank, u.Tags).String()
		scope = &key
	}
	version := u.Version
	if version < 1 {
		version = 1
	}
	return db.UnitInsertParams{
		ID: id, TenantID: u.Bank.Tenant, BankID: u.Bank.Bank, DocumentID: textPtr(u.DocumentID), ChunkID: chunk,
		FactType: string(u.Type), Text: u.Text, Context: u.Context, Metadata: rawMeta, Tags: tagsOrEmpty(u.Tags),
		Embedding: vec(u.Embedding), OccurredStart: u.OccurredStart, OccurredEnd: u.OccurredEnd,
		MentionedAt: u.MentionedAt, Timeless: u.Timeless, ProofCount: int32(u.ProofCount), //nolint:gosec // counts are small
		ScopeKey: scope, Version: version, Seq: u.Seq, ConsolidatedAt: u.ConsolidatedAt,
		ConsolidationFailedAt: u.ConsolidationFailedAt, EditedAt: u.EditedAt, CreatedAt: orNow(u.CreatedAt),
	}, nil
}

// UpsertObservation is not implemented yet.
func (r *unitRepo) UpsertObservation(ctx context.Context, o domain.Unit) error {
	return errNotImplemented
}

// RecordObservationVersion is not implemented yet.
func (r *unitRepo) RecordObservationVersion(ctx context.Context, b port.BankRef, obs port.UnitID, v domain.ObservationVersion) error {
	return errNotImplemented
}

// Update is not implemented yet.
func (r *unitRepo) Update(ctx context.Context, u domain.Unit) error {
	return errNotImplemented
}

// SetDocumentTags is not implemented yet.
func (r *unitRepo) SetDocumentTags(ctx context.Context, b port.BankRef, doc port.DocumentID, tags []string, seq int64) ([]port.UnitID, error) {
	return nil, errNotImplemented
}

// AppendAudit is not implemented yet.
func (r *unitRepo) AppendAudit(ctx context.Context, a domain.AuditEntry) error {
	return errNotImplemented
}

// Unconsolidated is not implemented yet.
func (r *unitRepo) Unconsolidated(ctx context.Context, b port.BankRef, scopes []port.ScopeKey, limit int) ([]domain.Unit, error) {
	return nil, errNotImplemented
}

// MarkConsolidated is not implemented yet.
func (r *unitRepo) MarkConsolidated(ctx context.Context, ids []port.UnitID, at time.Time) error {
	return errNotImplemented
}

// MarkConsolidationFailed is not implemented yet.
func (r *unitRepo) MarkConsolidationFailed(ctx context.Context, ids []port.UnitID, at time.Time) error {
	return errNotImplemented
}

// DeleteByDocument is not implemented yet.
func (r *unitRepo) DeleteByDocument(ctx context.Context, b port.BankRef, doc port.DocumentID) ([]port.UnitID, error) {
	return nil, errNotImplemented
}

// Archive is not implemented yet.
func (r *unitRepo) Archive(ctx context.Context, b port.BankRef, id port.UnitID, reason string) error {
	return errNotImplemented
}

// Restore is not implemented yet.
func (r *unitRepo) Restore(ctx context.Context, b port.BankRef, id port.UnitID) error {
	return errNotImplemented
}
