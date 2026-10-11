package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
)

// DeleteByDocument removes the document's live units and returns their ids. Their link neighbours go to
// graph_dirty and their entities to entity_maintenance; their links, entity rows and evidence go by cascade.
// Archived units of the document are kept. Callers delete the observations citing the units with
// EvidenceRepo.DeleteCiting first, in the same transaction: the cascade removes the evidence rows it reads.
func (r *unitRepo) DeleteByDocument(
	ctx context.Context, b domain.BankRef, doc domain.DocumentID,
) ([]domain.UnitID, error) {
	ids, err := r.q.UnitLockDocument(ctx, db.UnitLockDocumentParams{
		TenantID: b.Tenant, BankID: b.Bank, DocumentID: string(doc),
	})
	if err != nil {
		return nil, mapErr("lock document units", err)
	}
	if err := removeUnits(ctx, r.q, b, ids); err != nil {
		return nil, err
	}
	return idsOf[domain.UnitID](ids), nil
}

// Archive moves a live unit to memory_units_archive, stamped with the transaction's change seq, with JSON
// snapshots of its entities and causal links (decision 130); its links and entity rows are deleted, its link
// neighbours go to graph_dirty and its entities to entity_maintenance. Unknown and already archived ids are
// domain.ErrNotFound. As with DeleteByDocument, callers run EvidenceRepo.DeleteCiting first.
func (r *unitRepo) Archive(ctx context.Context, b domain.BankRef, id domain.UnitID, reason string) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	seq, err := r.tx.Seq(ctx, b) // the bank row lock comes before the unit's
	if err != nil {
		return err
	}
	locked, err := r.q.UnitLockIDs(ctx, db.UnitLockIDsParams{TenantID: b.Tenant, BankID: b.Bank, Ids: []uuid.UUID{uid}})
	if err != nil {
		return mapErr("lock unit", err)
	}
	if len(locked) == 0 {
		return fmt.Errorf("%w: live unit %s", domain.ErrNotFound, id)
	}
	n, err := r.q.UnitArchive(ctx, db.UnitArchiveParams{
		Seq: seq, Reason: reason, ID: uid, TenantID: b.Tenant, BankID: b.Bank,
	})
	if err != nil {
		return mapErr("archive unit", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: live unit %s", domain.ErrNotFound, id)
	}
	return removeUnits(ctx, r.q, b, locked)
}

// Restore moves an archived unit back as a live unit stamped with the transaction's change seq, without
// embedding and without consolidation stamps, and reattaches the snapshot's entities and causal links whose
// entity or partner still exists live in the bank. Causal links whose partner is itself archived are handed
// over to the partner's snapshot, so restoring the partner later brings them back. A chunk that no longer
// exists is dropped; a document that no longer exists is domain.ErrFailedPrecondition. An id that is not
// archived in b is domain.ErrNotFound.
func (r *unitRepo) Restore(ctx context.Context, b domain.BankRef, id domain.UnitID) error {
	uid, err := parseID(id)
	if err != nil {
		return err
	}
	seq, err := r.tx.Seq(ctx, b) // the bank row lock comes before the archive row's
	if err != nil {
		return err
	}
	a, err := r.q.UnitTakeArchived(ctx, db.UnitTakeArchivedParams{ID: uid, TenantID: b.Tenant, BankID: b.Bank})
	if err != nil {
		return mapErr(fmt.Sprintf("archived unit %s", id), err)
	}
	if err := r.restoreRow(ctx, b, a, seq); err != nil {
		return err
	}
	err = r.q.UnitRestoreEntities(ctx, db.UnitRestoreEntitiesParams{
		UnitID: uid, TenantID: b.Tenant, BankID: b.Bank, Snapshot: snapshotOrEmpty(a.EntitiesSnapshot),
	})
	if err != nil {
		return mapErr("restore unit entities", err)
	}
	links := snapshotOrEmpty(a.LinksSnapshot)
	err = r.q.UnitRestoreLinks(ctx, db.UnitRestoreLinksParams{
		Snapshot: links, UnitID: uid, TenantID: b.Tenant, BankID: b.Bank,
	})
	if err != nil {
		return mapErr("restore unit links", err)
	}
	err = r.q.UnitHandOverLinks(ctx, db.UnitHandOverLinksParams{
		TenantID: b.Tenant, BankID: b.Bank, UnitID: uid, Snapshot: links,
	})
	return mapErr("hand over archived partners' links", err)
}

// restoreRow inserts the archived row a back into memory_units with seq; an observation gets its scope key
// back and its scope recorded.
func (r *unitRepo) restoreRow(ctx context.Context, b domain.BankRef, a db.MemoryUnitsArchive, seq int64) error {
	var scope *string
	if domain.FactType(a.FactType) == domain.FactTypeObservation {
		key := domain.NewScopeKey(b, a.Tags).String()
		scope = &key
		if err := upsertScope(ctx, r.q, b, a.Tags); err != nil {
			return err
		}
	}
	err := r.q.UnitRestore(ctx, db.UnitRestoreParams{
		ID: a.ID, TenantID: a.TenantID, BankID: a.BankID, DocumentID: a.DocumentID, ChunkID: a.ChunkID,
		FactType: a.FactType, Text: a.Text, Context: a.Context, Metadata: snapshotOrObject(a.Metadata),
		Tags: tagsOrEmpty(a.Tags), OccurredStart: a.OccurredStart, OccurredEnd: a.OccurredEnd,
		MentionedAt: a.MentionedAt, Timeless: a.Timeless, ScopeKey: scope, Seq: seq, EditedAt: a.EditedAt,
		CreatedAt: a.CreatedAt,
	})
	if isPgCode(err, codeForeignKeyViolation) {
		return fmt.Errorf("%w: restore unit %s: its document %q no longer exists: %w", domain.ErrFailedPrecondition,
			a.ID, textOf[domain.DocumentID](a.DocumentID), err)
	}
	return mapErr("restore unit", err)
}

// snapshotOrEmpty returns a JSON array column value, or an empty array for NULL.
func snapshotOrEmpty(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("[]")
	}
	return raw
}

// snapshotOrObject returns a JSON object column value, or an empty object for NULL.
func snapshotOrObject(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
