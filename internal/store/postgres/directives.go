package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// directiveRepo is store.DirectiveRepo over one transaction.
type directiveRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.DirectiveRepo = (*directiveRepo)(nil)

// directiveNameIndex is the unique index on a directive's name within its bank.
const directiveNameIndex = "directives_bank_name_key"

// Create inserts the directive (a new UUID string id when d.ID is empty) and returns its id. A taken id or name
// in the bank is domain.ErrAlreadyExists; an unknown bank is domain.ErrNotFound.
func (r *directiveRepo) Create(ctx context.Context, d domain.Directive) (domain.DirectiveID, error) {
	if err := checkTags(d.Tags); err != nil {
		return "", err
	}
	if d.ID == "" {
		d.ID = domain.DirectiveID(uuid.NewString())
	}
	err := r.q.DirectiveCreate(ctx, db.DirectiveCreateParams{
		TenantID: d.Bank.Tenant, OrgID: d.Bank.Org, BankID: d.Bank.Bank, ID: string(d.ID), Name: d.Name,
		Rules: tagsOrEmpty(d.Rules), Tags: tagsOrEmpty(d.Tags), Active: d.Active,
	})
	if err != nil {
		return "", directiveErr(d, "create", err)
	}
	return d.ID, nil
}

// Get returns one directive, locked until the transaction ends; unknown = domain.ErrNotFound.
func (r *directiveRepo) Get(ctx context.Context, b domain.BankRef, id domain.DirectiveID) (domain.Directive, error) {
	row, err := r.q.DirectiveGet(ctx, db.DirectiveGetParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id)})
	if err != nil {
		return domain.Directive{}, mapErr(fmt.Sprintf("directive %s", id), err)
	}
	return directivesOf(b, []db.Directive{row})[0], nil
}

// Update replaces the name, rules, tags and active flag of directive d.ID, keeping created_at. Unknown =
// domain.ErrNotFound; a name taken by another directive of the bank is domain.ErrAlreadyExists.
func (r *directiveRepo) Update(ctx context.Context, d domain.Directive) error {
	if err := checkTags(d.Tags); err != nil {
		return err
	}
	n, err := r.q.DirectiveUpdate(ctx, db.DirectiveUpdateParams{
		TenantID: d.Bank.Tenant, OrgID: d.Bank.Org, BankID: d.Bank.Bank, ID: string(d.ID), Name: d.Name,
		Rules: tagsOrEmpty(d.Rules), Tags: tagsOrEmpty(d.Tags), Active: d.Active,
	})
	if err != nil {
		return directiveErr(d, "update", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: directive %s", domain.ErrNotFound, d.ID)
	}
	return nil
}

// directiveErr maps the error of a directive write: a unique violation is domain.ErrAlreadyExists with a message
// of its own (a taken name, or a taken id), never the driver's; anything else goes through mapErr.
func directiveErr(d domain.Directive, verb string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != codeUniqueViolation {
		return mapErr(fmt.Sprintf("%s directive %s", verb, d.ID), err)
	}
	if pgErr.ConstraintName == directiveNameIndex {
		return fmt.Errorf("%w: bank %s already has a directive named %q", domain.ErrAlreadyExists, d.Bank.Bank, d.Name)
	}
	return fmt.Errorf("%w: directive %s already exists", domain.ErrAlreadyExists, d.ID)
}

// List returns the bank's directives (only active ones when activeOnly), oldest first.
func (r *directiveRepo) List(ctx context.Context, b domain.BankRef, activeOnly bool) ([]domain.Directive, error) {
	rows, err := r.q.DirectiveList(ctx, db.DirectiveListParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ActiveOnly: activeOnly})
	if err != nil {
		return nil, mapErr("list directives", err)
	}
	return directivesOf(b, rows), nil
}

// Page returns one keyset page of the bank's directives, oldest first (created_at, then id in byte order). An
// empty q.Tags is no tag filter.
func (r *directiveRepo) Page(ctx context.Context, q store.DirectiveQuery) ([]domain.Directive, error) {
	match, tags := tagFilterArgs(q.Tags, q.TagsMatch)
	arg := db.DirectivePageParams{
		TenantID: q.Bank.Tenant, OrgID: q.Bank.Org, BankID: q.Bank.Bank, ActiveOnly: q.ActiveOnly, TagsMatch: match, FilterTags: tags,
		Lim: int32(min(q.Limit, math.MaxInt32)), //nolint:gosec // clamped to the int32 range
	}
	if q.After != nil {
		arg.AfterCreatedAt, arg.AfterID = &q.After.CreatedAt, string(q.After.ID)
	}
	rows, err := r.q.DirectivePage(ctx, arg)
	if err != nil {
		return nil, mapErr("page directives", err)
	}
	return directivesOf(q.Bank, rows), nil
}

// directivesOf converts directive rows of bank b.
func directivesOf(b domain.BankRef, rows []db.Directive) []domain.Directive {
	out := make([]domain.Directive, len(rows))
	for i, row := range rows {
		out[i] = domain.Directive{
			ID: domain.DirectiveID(row.ID), Bank: b, Name: row.Name, Rules: row.Rules, Tags: row.Tags,
			Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out
}

// Delete removes the directive; unknown = domain.ErrNotFound.
func (r *directiveRepo) Delete(ctx context.Context, b domain.BankRef, id domain.DirectiveID) error {
	n, err := r.q.DirectiveDelete(ctx, db.DirectiveDeleteParams{TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id)})
	return docRowsErr(fmt.Sprintf("delete directive %s", id), n, err)
}
