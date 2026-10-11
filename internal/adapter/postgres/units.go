package postgres

import (
	"context"
	"fmt"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"time"

	"github.com/google/uuid"
)

// unitRepo is port.UnitRepo over one transaction. Removal, archive and restore live in units_remove.go.
type unitRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.UnitRepo = (*unitRepo)(nil)

// Insert stores new units exactly as given, including their Seq stamp. A zero CreatedAt takes the
// database's now(); an observation's scope_key is the encoding of its tags, and its scope is recorded in
// observation_scopes.
func (r *unitRepo) Insert(ctx context.Context, us []domain.Unit) error {
	for i := range us {
		p, err := unitInsertParams(us[i])
		if err != nil {
			return err
		}
		if err := r.q.UnitInsert(ctx, p); err != nil {
			return mapErr("insert unit", err)
		}
		if us[i].Type == domain.FactTypeObservation {
			if err := upsertScope(ctx, r.q, us[i].Bank, us[i].Tags); err != nil {
				return err
			}
		}
	}
	return nil
}

// unitInsertParams maps a unit to its insert parameters.
func unitInsertParams(u domain.Unit) (db.UnitInsertParams, error) {
	if err := checkTags(u.Tags); err != nil {
		return db.UnitInsertParams{}, err
	}
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

// upsertScope records the observation scope of tags in bank b.
func upsertScope(ctx context.Context, q *db.Queries, b port.BankRef, tags []string) error {
	key := domain.NewScopeKey(b, tags)
	err := q.UnitScopeUpsert(ctx, db.UnitScopeUpsertParams{
		TenantID: b.Tenant, BankID: b.Bank, ScopeKey: key.String(), Tags: tagsOrEmpty(key.Tags),
	})
	return mapErr("upsert observation scope", err)
}

// UpsertObservation inserts the observation or replaces the text, proof count, embedding, version and seq
// of the bank's observation with the same id, and records its scope (its tags) in observation_scopes. An id
// held by a fact or by another bank's unit is domain.ErrAlreadyExists.
func (r *unitRepo) UpsertObservation(ctx context.Context, o domain.Unit) error {
	o.Type = domain.FactTypeObservation
	p, err := unitInsertParams(o)
	if err != nil {
		return err
	}
	n, err := r.q.UnitUpsertObservation(ctx, db.UnitUpsertObservationParams(p))
	if err != nil {
		return mapErr("upsert observation", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: unit %s is not an observation of bank %s/%s", domain.ErrAlreadyExists, o.ID,
			o.Bank.Tenant, o.Bank.Bank)
	}
	return upsertScope(ctx, r.q, o.Bank, o.Tags)
}

// RecordObservationVersion appends one observation_versions row; a zero At takes the database's now(). A
// second row for the same (observation, version) is domain.ErrAlreadyExists.
func (r *unitRepo) RecordObservationVersion(
	ctx context.Context, b port.BankRef, obs port.UnitID, v domain.ObservationVersion,
) error {
	id, err := parseID(obs)
	if err != nil {
		return err
	}
	err = r.q.UnitRecordObservationVersion(ctx, db.UnitRecordObservationVersionParams{
		TenantID: b.Tenant, BankID: b.Bank, ObservationID: id, Version: v.Version, Text: v.Text,
		SourceIds: parseIDs(v.SourceIDs), At: orNow(v.At),
	})
	return mapErr("record observation version", err)
}

// Update replaces a live fact's text, context, occurred dates, timeless flag, fact type, edited_at, seq and
// embedding (nil clears it), and clears both consolidation stamps. Unknown, archived and observation ids are
// domain.ErrNotFound.
func (r *unitRepo) Update(ctx context.Context, u domain.Unit) error {
	id, err := parseID(u.ID)
	if err != nil {
		return err
	}
	n, err := r.q.UnitUpdate(ctx, db.UnitUpdateParams{
		Text: u.Text, Context: u.Context, OccurredStart: u.OccurredStart, OccurredEnd: u.OccurredEnd,
		Timeless: u.Timeless, FactType: string(u.Type), EditedAt: u.EditedAt, Seq: u.Seq,
		Embedding: vec(u.Embedding), ID: id, TenantID: u.Bank.Tenant, BankID: u.Bank.Bank,
	})
	if err != nil {
		return mapErr("update unit", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: live fact %s", domain.ErrNotFound, u.ID)
	}
	return nil
}

// SetDocumentTags replaces the document-derived tags of the document's units, live and archived, with
// (tags − oldTags) ∪ newTags, so a unit's extra (label) tags survive; on the live units it also sets seq and
// clears both consolidation stamps, and it returns their ids. Archived units are retagged too, so a later
// Restore brings them back under the document's current tags.
func (r *unitRepo) SetDocumentTags(
	ctx context.Context, b port.BankRef, doc port.DocumentID, oldTags, newTags []string, seq int64,
) ([]port.UnitID, error) {
	if err := checkTags(oldTags); err != nil {
		return nil, err
	}
	if err := checkTags(newTags); err != nil {
		return nil, err
	}
	err := r.q.UnitSetArchivedDocumentTags(ctx, db.UnitSetArchivedDocumentTagsParams{
		OldTags: tagsOrEmpty(oldTags), NewTags: tagsOrEmpty(newTags),
		TenantID: b.Tenant, BankID: b.Bank, DocumentID: textPtr(doc),
	})
	if err != nil {
		return nil, mapErr("set archived document tags", err)
	}
	ids, err := r.q.UnitSetDocumentTags(ctx, db.UnitSetDocumentTagsParams{
		OldTags: tagsOrEmpty(oldTags), NewTags: tagsOrEmpty(newTags), Seq: seq,
		TenantID: b.Tenant, BankID: b.Bank, DocumentID: string(doc),
	})
	if err != nil {
		return nil, mapErr("set document tags", err)
	}
	return idsOf[port.UnitID](ids), nil
}

// AppendAudit writes one memory_audit row; a zero At takes the database's now() and empty Before / After
// are NULL.
func (r *unitRepo) AppendAudit(ctx context.Context, a domain.AuditEntry) error {
	unit, err := parseID(a.UnitID)
	if err != nil {
		return err
	}
	op, err := nullID(a.OperationID)
	if err != nil {
		return err
	}
	p := db.UnitAppendAuditParams{
		TenantID: a.Bank.Tenant, BankID: a.Bank.Bank, UnitID: unit, Action: string(a.Action), Reason: a.Reason,
		OperationID: op, At: orNow(a.At),
	}
	if len(a.Before) > 0 {
		p.Before = a.Before
	}
	if len(a.After) > 0 {
		p.After = a.After
	}
	return mapErr("append audit", r.q.UnitAppendAudit(ctx, p))
}

// IDsByDocument returns the ids of doc's live units in b, sorted; it writes and locks nothing.
func (r *unitRepo) IDsByDocument(ctx context.Context, b port.BankRef, doc port.DocumentID) ([]port.UnitID, error) {
	ids, err := r.q.UnitIDsByDocument(ctx, db.UnitIDsByDocumentParams{
		TenantID: b.Tenant, BankID: b.Bank, DocumentID: string(doc),
	})
	if err != nil {
		return nil, mapErr("unit ids of document", err)
	}
	return idsOf[port.UnitID](ids), nil
}

// Unconsolidated returns up to limit of the bank's facts with neither consolidation stamp and with an
// embedding (decision 1500: a unit awaiting rederive is no consolidation candidate), oldest
// created_at then id first, with their entity names. With scopes, only units whose tags contain every tag
// of at least one scope are returned (so the shared scope matches every unit); the scopes' banks are
// ignored in favour of b.
func (r *unitRepo) Unconsolidated(
	ctx context.Context, b port.BankRef, scopes []port.ScopeKey, limit int,
) ([]domain.Unit, error) {
	if limit <= 0 {
		return []domain.Unit{}, nil
	}
	sets := make([][]string, len(scopes))
	for i, s := range scopes {
		sets[i] = tagsOrEmpty(domain.NormalizeTags(s.Tags))
	}
	raw, err := marshal("scopes", sets)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.UnitUnconsolidated(ctx, db.UnitUnconsolidatedParams{
		TenantID: b.Tenant, BankID: b.Bank, FilterScopes: len(scopes) > 0, Scopes: raw,
		Lim: contentLimit(limit),
	})
	if err != nil {
		return nil, mapErr("unconsolidated units", err)
	}
	out := make([]domain.Unit, 0, len(rows))
	for _, row := range rows {
		u, err := unitOf(row.MemoryUnit, row.EntityNames, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// MarkConsolidated stamps consolidated_at = at on the units; unknown ids are skipped.
func (r *unitRepo) MarkConsolidated(ctx context.Context, ids []port.UnitID, at time.Time) error {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return nil
	}
	err := r.q.UnitMarkConsolidated(ctx, db.UnitMarkConsolidatedParams{At: at, Ids: uuids})
	return mapErr("mark consolidated", err)
}

// MarkConsolidationFailed stamps consolidation_failed_at = at on the units; unknown ids are skipped.
func (r *unitRepo) MarkConsolidationFailed(ctx context.Context, ids []port.UnitID, at time.Time) error {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return nil
	}
	err := r.q.UnitMarkConsolidationFailed(ctx, db.UnitMarkConsolidationFailedParams{At: at, Ids: uuids})
	return mapErr("mark consolidation failed", err)
}

// removeUnits deletes the bank's live units ids, which the caller has locked: their link neighbours (not
// among ids) go to graph_dirty, their entities to entity_maintenance (with the mentions taken off), then the
// units go, and with them by cascade their links, entity rows and evidence, and their own graph_dirty rows.
func removeUnits(ctx context.Context, q *db.Queries, b port.BankRef, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := q.UnitQueueNeighbours(ctx, ids); err != nil {
		return mapErr("queue graph neighbours", err)
	}
	if err := q.UnitQueueEntities(ctx, ids); err != nil {
		return mapErr("queue entity maintenance", err)
	}
	err := q.UnitDeleteIDs(ctx, db.UnitDeleteIDsParams{TenantID: b.Tenant, BankID: b.Bank, Ids: ids})
	return mapErr("delete units", err)
}
