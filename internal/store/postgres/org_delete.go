package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"
	"time"

	"github.com/jackc/pgx/v5"
)

// LockForDelete locks the org row FOR UPDATE and returns it, tombstoned or not; unknown = domain.ErrOrgNotFound.
func (r *orgRepo) LockForDelete(ctx context.Context, o domain.OrgRef) (domain.Org, error) {
	if err := checkOrgRef(o); err != nil {
		return domain.Org{}, err
	}
	row, err := r.q.OrgLockForDelete(ctx, db.OrgLockForDeleteParams{TenantID: o.Tenant, ID: o.Org})
	if err != nil {
		return domain.Org{}, orgErr(o, err)
	}
	return orgOf(row)
}

// MarkDeleting tombstones the org, then (a later statement, so it sees banks committed while LockForDelete waited)
// every live bank of it, and returns how many banks it tombstoned.
func (r *orgRepo) MarkDeleting(ctx context.Context, o domain.OrgRef, at time.Time) (int, error) {
	if err := checkOrgRef(o); err != nil {
		return 0, err
	}
	n, err := r.q.OrgMarkDeleting(ctx, db.OrgMarkDeletingParams{At: at, TenantID: o.Tenant, ID: o.Org})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("mark org %s deleting", o), err)
	}
	if n == 0 {
		return 0, fmt.Errorf("%w %s", domain.ErrOrgNotFound, o)
	}
	banks, err := r.q.OrgMarkBanksDeleting(ctx, db.OrgMarkBanksDeletingParams{At: at, TenantID: o.Tenant, OrgID: o.Org})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("mark banks of org %s deleting", o), err)
	}
	return int(banks), nil
}

// BanksAwaitingDelete returns up to limit tombstoned banks of o without a pending or processing delete_bank.
func (r *orgRepo) BanksAwaitingDelete(ctx context.Context, o domain.OrgRef, limit int) ([]domain.BankRef, error) {
	if err := checkOrgRef(o); err != nil {
		return nil, err
	}
	ids, err := r.q.OrgBanksAwaitingDelete(ctx, db.OrgBanksAwaitingDeleteParams{
		TenantID: o.Tenant, OrgID: o.Org, Lim: toInt32Sat(limit),
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("banks awaiting delete in org %s", o), err)
	}
	out := make([]domain.BankRef, len(ids))
	for i, id := range ids {
		out[i] = domain.NewBankRef(o.Tenant, o.Org, id)
	}
	return out, nil
}

// CountBanks returns how many bank rows of o are left.
func (r *orgRepo) CountBanks(ctx context.Context, o domain.OrgRef) (int, error) {
	if err := checkOrgRef(o); err != nil {
		return 0, err
	}
	n, err := r.q.OrgCountBanks(ctx, db.OrgCountBanksParams{TenantID: o.Tenant, OrgID: o.Org})
	if err != nil {
		return 0, mapErr(fmt.Sprintf("count banks of org %s", o), err)
	}
	return int(n), nil
}

// orgSweepStep is one table of the org-wide sweep. Its rows are matched by their own (tenant_id, org_id), or, for a
// join table without an org-prefixed index, through the parent row they hang off (parent.id = table.fk), whose
// (tenant_id, org_id, ...) index the lookup then uses. Table and column names are constants.
type orgSweepStep struct {
	table, parent, fk string
}

// orgSweepSteps lists every content table in BankRepo.DeleteBatch's order (join and queue tables first, units
// before the chunks, documents and entities they hang off, then the bank-level tables), so a table's cascades only
// reach tables already emptied and a sweep that deletes 0 rows has emptied them all. The banks, operations and
// lifecycle_events tables are not swept: banks go with their own delete, and Delete removes the other two.
var orgSweepSteps = []orgSweepStep{
	{"unit_entities", "memory_units", "unit_id"},
	{"entity_cooccurrences", "entities", "entity_a"},
	{"memory_links", "memory_units", "from_unit_id"},
	{"observation_evidence", "memory_units", "observation_id"},
	{table: "graph_dirty"},
	{table: "entity_maintenance"},
	{table: "observation_versions"},
	{table: "memory_audit"},
	{table: "memory_units_archive"},
	{table: "memory_units"},
	{table: "chunks"},
	{table: "documents"},
	{table: "entities"},
	{table: "observation_scopes"},
	{table: "mental_model_history"},
	{table: "mental_models"},
	{table: "directives"},
}

// sql is the step's statement: delete up to $3 rows of org ($1, $2).
func (s orgSweepStep) sql() string {
	t := pgx.Identifier{s.table}.Sanitize()
	if s.parent == "" {
		return "DELETE FROM " + t + " WHERE ctid = ANY(ARRAY(SELECT d.ctid FROM " + t +
			" d WHERE d.tenant_id = $1 AND d.org_id = $2 LIMIT $3))"
	}
	p, fk := pgx.Identifier{s.parent}.Sanitize(), pgx.Identifier{s.fk}.Sanitize()
	return "DELETE FROM " + t + " WHERE ctid = ANY(ARRAY(SELECT c.ctid FROM " + t + " c JOIN " + p +
		" p ON p.id = c." + fk + " WHERE p.tenant_id = $1 AND p.org_id = $2 LIMIT $3))"
}

// DeleteBatch deletes up to limit content rows of the tombstoned org, table by table in orgSweepSteps order, and
// returns how many it deleted; 0 means none is left. It share-locks the org row first, so the org can't be deleted
// (or recreated) under it.
func (r *orgRepo) DeleteBatch(ctx context.Context, o domain.OrgRef, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("%w: delete batch limit %d", domain.ErrInvalidArgument, limit)
	}
	if err := r.lockTombstoned(ctx, o); err != nil {
		return 0, err
	}
	remaining, total := limit, 0
	for _, step := range orgSweepSteps {
		tag, err := r.conn.Exec(ctx, step.sql(), o.Tenant, o.Org, remaining)
		if err != nil {
			return total, mapErr(fmt.Sprintf("sweep %s of org %s", step.table, o), err)
		}
		total += int(tag.RowsAffected())
		if remaining -= int(tag.RowsAffected()); remaining <= 0 {
			break
		}
	}
	return total, nil
}

// CountRows counts o's rows in every base table with an org_id column (orgs excluded), listed from the schema at
// call time.
func (r *orgRepo) CountRows(ctx context.Context, o domain.OrgRef) (int, int64, error) {
	if err := checkOrgRef(o); err != nil {
		return 0, 0, err
	}
	tables, err := r.q.OrgTablesWithOrgID(ctx)
	if err != nil {
		return 0, 0, mapErr("list tables with org_id", err)
	}
	var rows int64
	for _, t := range tables {
		var n int64
		sql := "SELECT count(*) FROM " + pgx.Identifier{t}.Sanitize() + " WHERE tenant_id = $1 AND org_id = $2"
		if err := r.conn.QueryRow(ctx, sql, o.Tenant, o.Org).Scan(&n); err != nil {
			return 0, 0, mapErr(fmt.Sprintf("count %s rows of org %s", t, o), err)
		}
		rows += n
	}
	return len(tables), rows, nil
}

// Delete removes the tombstoned org's operations and lifecycle events, then its row (the banks foreign key,
// RESTRICT, refuses while a bank row is left).
func (r *orgRepo) Delete(ctx context.Context, o domain.OrgRef) error {
	if err := r.lockTombstoned(ctx, o); err != nil {
		return err
	}
	if err := r.q.OrgDeleteOperations(ctx, db.OrgDeleteOperationsParams{TenantID: o.Tenant, OrgID: o.Org}); err != nil {
		return mapErr(fmt.Sprintf("delete operations of org %s", o), err)
	}
	if err := r.q.OrgDeleteEvents(ctx, db.OrgDeleteEventsParams{TenantID: o.Tenant, OrgID: o.Org}); err != nil {
		return mapErr(fmt.Sprintf("delete events of org %s", o), err)
	}
	_, err := r.q.OrgDeleteRow(ctx, db.OrgDeleteRowParams{TenantID: o.Tenant, ID: o.Org})
	if isPgCode(err, codeForeignKeyViolation) {
		return fmt.Errorf("%w: org %s still has banks: %w", domain.ErrFailedPrecondition, o, err)
	}
	return mapErr(fmt.Sprintf("delete org %s", o), err)
}

// lockTombstoned share-locks a tombstoned org's row (FOR KEY SHARE) until the transaction ends: unknown =
// domain.ErrOrgNotFound, live = domain.ErrFailedPrecondition.
func (r *orgRepo) lockTombstoned(ctx context.Context, o domain.OrgRef) error {
	if err := checkOrgRef(o); err != nil {
		return err
	}
	deletingAt, err := r.q.OrgLockLive(ctx, db.OrgLockLiveParams{TenantID: o.Tenant, ID: o.Org})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w %s", domain.ErrOrgNotFound, o)
	}
	if err != nil {
		return mapErr(fmt.Sprintf("org %s", o), err)
	}
	if deletingAt == nil {
		return fmt.Errorf("%w: org %s is not being deleted", domain.ErrFailedPrecondition, o)
	}
	return nil
}
