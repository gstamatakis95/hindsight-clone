package postgres

import (
	"context"
	"errors"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// entityRepo is port.EntityRepo over one transaction. The store maintains mention_count: Attach and Restore
// add one per new unit_entities row; Detach, DeleteByDocument and Archive take one off (decision 131).
type entityRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.EntityRepo = (*entityRepo)(nil)

// contentLimit clamps a caller's limit to a positive int32.
func contentLimit(limit int) int32 {
	return int32(min(max(limit, 0), 1<<30)) //nolint:gosec // clamped to [0, 2^30]
}

// entityOf builds a domain.Entity from its row.
func entityOf(e db.Entity) domain.Entity {
	return domain.Entity{
		ID: idOf[port.EntityID](e.ID), Bank: domain.BankRef{Tenant: e.TenantID, Bank: e.BankID},
		CanonicalName: e.CanonicalName, Kind: e.Kind, LabelValue: textOf[string](e.LabelValue),
		MentionCount: int(e.MentionCount), FirstSeen: e.FirstSeen, LastSeen: e.LastSeen,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

// Candidates returns up to limit of the bank's entities whose lower-cased names are trigram-similar to the
// lower-cased name (pg_trgm's % operator at its similarity threshold), most similar first, with the
// similarity in (0, 1].
func (r *entityRepo) Candidates(
	ctx context.Context, b port.BankRef, name string, limit int,
) ([]domain.EntityCandidate, error) {
	if limit <= 0 {
		return []domain.EntityCandidate{}, nil
	}
	rows, err := r.q.EntityCandidates(ctx, db.EntityCandidatesParams{
		Name: name, TenantID: b.Tenant, BankID: b.Bank, Lim: contentLimit(limit),
	})
	if err != nil {
		return nil, mapErr("entity candidates", err)
	}
	out := make([]domain.EntityCandidate, len(rows))
	for i, row := range rows {
		out[i] = domain.EntityCandidate{Entity: entityOf(row.Entity), NameSimilarity: row.Sim}
	}
	return out, nil
}

// Cooccurrence returns how many of others have co-occurred with entity (share or shared a unit with it,
// as recorded in entity_cooccurrences).
func (r *entityRepo) Cooccurrence(
	ctx context.Context, b port.BankRef, entity port.EntityID, others []port.EntityID,
) (int, error) {
	id, err := parseID(entity)
	if err != nil {
		return 0, err
	}
	ids := parseIDs(others)
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := r.q.EntityCooccurrence(ctx, db.EntityCooccurrenceParams{
		TenantID: b.Tenant, BankID: b.Bank, EntityID: id, Others: ids,
	})
	if err != nil {
		return 0, mapErr("entity cooccurrence", err)
	}
	return int(n), nil
}

// Upsert updates the bank's entity with e.ID (name, kind, label value; first and last seen only widen) or
// inserts e, with a new id when e.ID is empty, and returns the id. A label entity whose (kind, label value)
// already exists resolves to that row. MentionCount is ignored: Attach maintains it.
func (r *entityRepo) Upsert(ctx context.Context, e domain.Entity) (port.EntityID, error) {
	id, err := parseNewID(e.ID)
	if err != nil {
		return "", err
	}
	label := textPtr(e.LabelValue)
	if e.ID != "" {
		got, err := r.q.EntityUpdate(ctx, db.EntityUpdateParams{
			CanonicalName: e.CanonicalName, Kind: e.Kind, LabelValue: label, FirstSeen: e.FirstSeen,
			LastSeen: e.LastSeen, ID: id, TenantID: e.Bank.Tenant, BankID: e.Bank.Bank,
		})
		if err == nil {
			return idOf[port.EntityID](got), nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", mapErr("update entity", err)
		}
	}
	got, err := r.q.EntityInsert(ctx, db.EntityInsertParams{
		ID: id, TenantID: e.Bank.Tenant, BankID: e.Bank.Bank, CanonicalName: e.CanonicalName, Kind: e.Kind,
		LabelValue: label, FirstSeen: e.FirstSeen, LastSeen: e.LastSeen,
	})
	if err != nil {
		return "", mapErr("insert entity", err)
	}
	return idOf[port.EntityID](got), nil
}

// Attach links a live unit to those of ents that exist in its bank (others are skipped: graph maintenance
// may have pruned them), adds a mention to each newly linked entity and bumps the co-occurrence count of
// every new pair on the unit. Re-attaching is a no-op. The entities are locked in id order first, so a
// concurrent PruneOrphans either sees the new rows or deletes the entities before they are attached. An
// unknown unit is domain.ErrNotFound.
func (r *entityRepo) Attach(ctx context.Context, unit port.UnitID, ents []port.EntityID) error {
	uid, err := parseID(unit)
	if err != nil {
		return err
	}
	bank, err := r.q.EntityUnitBank(ctx, uid)
	if err != nil {
		return mapErr("unit "+string(unit), err)
	}
	ids := parseIDs(ents)
	if len(ids) == 0 {
		return nil
	}
	locked, err := r.q.EntityLock(ctx, db.EntityLockParams{TenantID: bank.TenantID, BankID: bank.BankID, Ids: ids})
	if err != nil || len(locked) == 0 {
		return mapErr("lock entities", err)
	}
	added, err := r.q.EntityAttach(ctx, db.EntityAttachParams{
		TenantID: bank.TenantID, BankID: bank.BankID, UnitID: uid, Ids: locked,
	})
	if err != nil || len(added) == 0 {
		return mapErr("attach entities", err)
	}
	return r.countAttached(ctx, bank, uid, added)
}

// countAttached adds a mention to each of the newly attached entities and bumps the co-occurrence count of
// each of them with every other entity of the unit.
func (r *entityRepo) countAttached(
	ctx context.Context, bank db.EntityUnitBankRow, unit uuid.UUID, added []uuid.UUID,
) error {
	if err := r.q.EntityBumpMentions(ctx, added); err != nil {
		return mapErr("count entity mentions", err)
	}
	err := r.q.EntityBumpCooccurrences(ctx, db.EntityBumpCooccurrencesParams{
		TenantID: bank.TenantID, BankID: bank.BankID, NewIds: added, UnitID: unit,
	})
	return mapErr("count entity cooccurrences", err)
}

// PendingMaintenance returns up to limit entity ids queued in the bank's entity_maintenance, oldest first.
func (r *entityRepo) PendingMaintenance(ctx context.Context, b port.BankRef, limit int) ([]port.EntityID, error) {
	if limit <= 0 {
		return []port.EntityID{}, nil
	}
	ids, err := r.q.EntityPendingMaintenance(ctx, db.EntityPendingMaintenanceParams{
		TenantID: b.Tenant, BankID: b.Bank, Lim: contentLimit(limit),
	})
	if err != nil {
		return nil, mapErr("pending entity maintenance", err)
	}
	return idsOf[port.EntityID](ids), nil
}

// Detach removes every unit_entities row of unit, takes a mention off each detached entity and queues them
// in entity_maintenance. Co-occurrence counts are not decremented. An unknown unit is a no-op.
func (r *entityRepo) Detach(ctx context.Context, unit port.UnitID) error {
	uid, err := parseID(unit)
	if err != nil {
		return err
	}
	return mapErr("detach entities", r.q.EntityDetach(ctx, uid))
}

// PruneOrphans deletes those of ids (in bank b) that no live unit references, with their co-occurrences,
// removes all of ids from entity_maintenance and returns how many entities it deleted. It locks the
// entities before checking references, so an Attach running concurrently can't lose its rows.
func (r *entityRepo) PruneOrphans(ctx context.Context, b port.BankRef, ids []port.EntityID) (int, error) {
	uuids := parseIDs(ids)
	if len(uuids) == 0 {
		return 0, nil
	}
	_, err := r.q.EntityLockForDelete(ctx, db.EntityLockForDeleteParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return 0, mapErr("lock entities", err)
	}
	n, err := r.q.EntityDeleteOrphans(ctx, db.EntityDeleteOrphansParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return 0, mapErr("prune orphan entities", err)
	}
	err = r.q.EntityClearMaintenance(ctx, db.EntityClearMaintenanceParams{TenantID: b.Tenant, BankID: b.Bank, Ids: uuids})
	if err != nil {
		return 0, mapErr("clear entity maintenance", err)
	}
	return int(n), nil
}
