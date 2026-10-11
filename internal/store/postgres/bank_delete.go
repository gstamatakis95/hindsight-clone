package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"

	"github.com/jackc/pgx/v5"
)

// bankDeleteStep deletes up to lim rows of one bank-keyed table and returns how many it deleted.
type bankDeleteStep struct {
	table string
	run   func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error)
}

// bankDeleteSteps lists every bank-keyed table in deletion order: join and queue tables first, then units
// before the chunks, documents and entities they hang off, then the bank-level tables. A table's cascades
// therefore only ever reach tables already emptied, so DeleteBatch's count is exact and 0 means every table
// is empty.
var bankDeleteSteps = []bankDeleteStep{
	{"unit_entities", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteUnitEntities(ctx, db.BankDeleteUnitEntitiesParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"entity_cooccurrences", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteCooccurrences(ctx, db.BankDeleteCooccurrencesParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"memory_links", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteLinks(ctx, db.BankDeleteLinksParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"observation_evidence", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteEvidence(ctx, db.BankDeleteEvidenceParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"graph_dirty", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteGraphDirty(ctx, db.BankDeleteGraphDirtyParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"entity_maintenance", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteEntityMaintenance(ctx, db.BankDeleteEntityMaintenanceParams{
			TenantID: b.Tenant, BankID: b.Bank, Lim: lim,
		})
	}},
	{"observation_versions", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteObservationVersions(ctx, db.BankDeleteObservationVersionsParams{
			TenantID: b.Tenant, BankID: b.Bank, Lim: lim,
		})
	}},
	{"memory_audit", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteMemoryAudit(ctx, db.BankDeleteMemoryAuditParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"memory_units_archive", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteArchive(ctx, db.BankDeleteArchiveParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"memory_units", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteUnits(ctx, db.BankDeleteUnitsParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"chunks", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteChunks(ctx, db.BankDeleteChunksParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"documents", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteDocuments(ctx, db.BankDeleteDocumentsParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"entities", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteEntities(ctx, db.BankDeleteEntitiesParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"observation_scopes", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteObservationScopes(ctx, db.BankDeleteObservationScopesParams{
			TenantID: b.Tenant, BankID: b.Bank, Lim: lim,
		})
	}},
	{"mental_model_history", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteModelHistory(ctx, db.BankDeleteModelHistoryParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"mental_models", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteModels(ctx, db.BankDeleteModelsParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
	{"directives", func(ctx context.Context, q *db.Queries, b domain.BankRef, lim int32) (int64, error) {
		return q.BankDeleteDirectives(ctx, db.BankDeleteDirectivesParams{TenantID: b.Tenant, BankID: b.Bank, Lim: lim})
	}},
}

// DeleteBatch deletes up to limit rows of the tombstoned bank's content, table by table in bankDeleteSteps
// order, and returns how many it deleted; 0 means only the bank row, its events and its operations are left.
// A live bank is domain.ErrFailedPrecondition, an unknown one domain.ErrNotFound, and limit <= 0
// domain.ErrInvalidArgument (0 would falsely report "done").
func (r *bankRepo) DeleteBatch(ctx context.Context, b domain.BankRef, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("%w: delete batch limit %d", domain.ErrInvalidArgument, limit)
	}
	if err := r.lockTombstoned(ctx, b); err != nil {
		return 0, err
	}
	remaining, total := limit, 0
	for _, step := range bankDeleteSteps {
		n, err := step.run(ctx, r.q, b, toInt32Sat(remaining))
		if err != nil {
			return total, mapErr(fmt.Sprintf("delete %s of bank %s/%s", step.table, b.Tenant, b.Bank), err)
		}
		total += int(n)
		if remaining -= int(n); remaining <= 0 {
			break
		}
	}
	return total, nil
}

// Delete removes the tombstoned bank's row first (an Enqueue holding FOR KEY SHARE on it makes this wait,
// and an Enqueue after it finds no bank), then the bank's lifecycle events and operations, the running
// delete_bank job's own row included. Content with a foreign key to the bank cascades with the bank row;
// the tables without one (bankUnkeyedTables) are deleted explicitly. A live bank is
// domain.ErrFailedPrecondition, an unknown one domain.ErrNotFound.
func (r *bankRepo) Delete(ctx context.Context, b domain.BankRef) error {
	n, err := r.q.BankDeleteRow(ctx, db.BankDeleteRowParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return mapErr(fmt.Sprintf("delete bank %s/%s", b.Tenant, b.Bank), err)
	}
	if n == 0 {
		if err := r.requireTombstoned(ctx, b); err != nil {
			return err
		}
		return fmt.Errorf("%w: bank %s/%s changed while being deleted", domain.ErrConflict, b.Tenant, b.Bank)
	}
	if err := r.deleteUnkeyed(ctx, b); err != nil {
		return err
	}
	if err := r.q.BankDeleteEvents(ctx, db.BankDeleteEventsParams{TenantID: b.Tenant, BankID: b.Bank}); err != nil {
		return mapErr(fmt.Sprintf("delete events of bank %s/%s", b.Tenant, b.Bank), err)
	}
	err = r.q.BankDeleteOperations(ctx, db.BankDeleteOperationsParams{TenantID: b.Tenant, BankID: b.Bank})
	return mapErr(fmt.Sprintf("delete operations of bank %s/%s", b.Tenant, b.Bank), err)
}

// bankUnkeyedTables are the bank-keyed tables with no foreign key to the bank: the bank row's
// cascade misses them, so Delete removes their rows itself.
var bankUnkeyedTables = map[string]bool{
	"graph_dirty": true, "entity_maintenance": true, "observation_versions": true, "memory_audit": true,
	"memory_units_archive": true,
}

// deleteUnkeyed deletes every row of b in bankUnkeyedTables.
func (r *bankRepo) deleteUnkeyed(ctx context.Context, b domain.BankRef) error {
	for _, step := range bankDeleteSteps {
		if !bankUnkeyedTables[step.table] {
			continue
		}
		if _, err := step.run(ctx, r.q, b, math.MaxInt32); err != nil {
			return mapErr(fmt.Sprintf("delete %s of bank %s/%s", step.table, b.Tenant, b.Bank), err)
		}
	}
	return nil
}

// lockTombstoned takes FOR KEY SHARE on a tombstoned bank's row, so the bank row can't be deleted (and a new
// bank of the same id can't appear) while this transaction deletes its content. Errors as requireTombstoned.
func (r *bankRepo) lockTombstoned(ctx context.Context, b domain.BankRef) error {
	deletingAt, err := r.q.QueueLockBank(ctx, db.QueueLockBankParams{TenantID: b.Tenant, ID: b.Bank})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w %s/%s", domain.ErrBankNotFound, b.Tenant, b.Bank)
	}
	if err != nil {
		return bankErr(b, err)
	}
	if deletingAt == nil {
		return fmt.Errorf("%w: bank %s/%s is not being deleted", domain.ErrFailedPrecondition, b.Tenant, b.Bank)
	}
	return nil
}

// requireTombstoned returns nil for a tombstoned bank, domain.ErrNotFound for an unknown one and
// domain.ErrFailedPrecondition for a live one.
func (r *bankRepo) requireTombstoned(ctx context.Context, b domain.BankRef) error {
	bank, err := r.q.GetBank(ctx, db.GetBankParams{TenantID: b.Tenant, ID: b.Bank})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w %s/%s", domain.ErrBankNotFound, b.Tenant, b.Bank)
	}
	if err != nil {
		return bankErr(b, err)
	}
	if bank.DeletingAt == nil {
		return fmt.Errorf("%w: bank %s/%s is not being deleted", domain.ErrFailedPrecondition, b.Tenant, b.Bank)
	}
	return nil
}
