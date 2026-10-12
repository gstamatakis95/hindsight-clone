package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"

	"github.com/jackc/pgx/v5"
)

// orgRepo is store.OrgRepo over one transaction.
type orgRepo struct {
	q *db.Queries
}

var _ store.OrgRepo = (*orgRepo)(nil)

// Get returns the org, including a tombstoned one; unknown = domain.ErrOrgNotFound.
func (r *orgRepo) Get(ctx context.Context, o domain.OrgRef) (domain.Org, error) {
	if err := checkOrgRef(o); err != nil {
		return domain.Org{}, err
	}
	row, err := r.q.OrgGet(ctx, db.OrgGetParams{TenantID: o.Tenant, ID: o.Org})
	if err != nil {
		return domain.Org{}, orgErr(o, err)
	}
	return orgOf(row)
}

// Create inserts a new org; an existing live id is domain.ErrAlreadyExists, a tombstoned one
// domain.ErrOrgDeleting.
func (r *orgRepo) Create(ctx context.Context, org domain.Org) error {
	if err := checkOrgRef(org.Ref); err != nil {
		return err
	}
	cfg, err := marshal("org config", org.Config)
	if err != nil {
		return err
	}
	existing, err := r.q.OrgGet(ctx, db.OrgGetParams{TenantID: org.Ref.Tenant, ID: org.Ref.Org})
	switch {
	case err == nil && existing.DeletingAt != nil:
		return fmt.Errorf("%w: org %s", domain.ErrOrgDeleting, org.Ref)
	case err == nil:
		return fmt.Errorf("%w: org %s already exists", domain.ErrAlreadyExists, org.Ref)
	case !errors.Is(err, pgx.ErrNoRows):
		return mapErr("create org", err)
	}
	err = r.q.OrgCreate(ctx, db.OrgCreateParams{TenantID: org.Ref.Tenant, ID: org.Ref.Org, Name: org.Name, Config: cfg})
	if isPgCode(err, codeUniqueViolation) { // a concurrent CreateOrg won
		return fmt.Errorf("%w: org %s already exists", domain.ErrAlreadyExists, org.Ref)
	}
	return mapErr("create org", err)
}

// SetConfig replaces the org's config level; unknown = domain.ErrOrgNotFound, tombstoned = domain.ErrOrgDeleting.
func (r *orgRepo) SetConfig(ctx context.Context, o domain.OrgRef, c domain.BankConfig) error {
	if err := checkOrgRef(o); err != nil {
		return err
	}
	cfg, err := marshal("org config", c)
	if err != nil {
		return err
	}
	n, err := r.q.OrgSetConfig(ctx, db.OrgSetConfigParams{TenantID: o.Tenant, ID: o.Org, Config: cfg})
	if err != nil {
		return mapErr("set org config", err)
	}
	if n == 0 {
		return lockLiveOrg(ctx, r.q, o)
	}
	return nil
}

// LockConfig takes the org config's transaction-scoped advisory lock.
func (r *orgRepo) LockConfig(ctx context.Context, o domain.OrgRef) error {
	if err := checkOrgRef(o); err != nil {
		return err
	}
	return mapErr("lock org config", r.q.OrgConfigLock(ctx, db.OrgConfigLockParams{TenantID: o.Tenant, OrgID: o.Org}))
}

// lockLiveOrg share-locks the org row (FOR KEY SHARE) until the transaction ends and checks that it is live: an
// unknown org is domain.ErrOrgNotFound and a tombstoned one domain.ErrOrgDeleting. Every path that creates a bank
// runs it first. An org delete locks the row FOR UPDATE before it tombstones the org's banks, so a bank creation
// either commits before that lock (and is tombstoned with the others) or waits for it and then sees the tombstone.
func lockLiveOrg(ctx context.Context, q *db.Queries, o domain.OrgRef) error {
	deletingAt, err := q.OrgLockLive(ctx, db.OrgLockLiveParams{TenantID: o.Tenant, ID: o.Org})
	if err != nil {
		return orgErr(o, err)
	}
	if deletingAt != nil {
		return fmt.Errorf("%w: org %s", domain.ErrOrgDeleting, o)
	}
	return nil
}

// orgErr maps the error of an org-row lookup of o: no row is domain.ErrOrgNotFound, anything else goes through
// mapErr.
func orgErr(o domain.OrgRef, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w %s", domain.ErrOrgNotFound, o)
	}
	return mapErr(fmt.Sprintf("org %s", o), err)
}

// orgOf builds a domain.Org from its row.
func orgOf(r db.Org) (domain.Org, error) {
	o := domain.Org{
		Ref:  domain.OrgRef{Tenant: r.TenantID, Org: r.ID},
		Name: r.Name, DeletingAt: r.DeletingAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return o, unmarshal("org config", r.Config, &o.Config)
}

// ListOrgs returns up to q.Limit of q.Tenant's live orgs whose id starts with q.IDPrefix, in byte order of id,
// after q.After.
func (r *reader) ListOrgs(ctx context.Context, q store.OrgQuery) ([]domain.Org, error) {
	rows, err := r.queries().OrgList(ctx, db.OrgListParams{
		TenantID: q.Tenant, IDPrefix: q.IDPrefix, AfterID: q.After, Lim: toInt32Sat(q.Limit),
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("list orgs of tenant %s", q.Tenant), err)
	}
	out := make([]domain.Org, len(rows))
	for i, row := range rows {
		if out[i], err = orgOf(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}
