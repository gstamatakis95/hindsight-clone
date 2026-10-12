package postgres

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// receiptRepo is store.ReceiptRepo over one transaction.
type receiptRepo struct {
	q *db.Queries
}

var _ store.ReceiptRepo = (*receiptRepo)(nil)

// Create inserts r with a fresh UUIDv7 id (or r.ID when set) and returns the id.
func (r *receiptRepo) Create(ctx context.Context, rc domain.DeletionReceipt) (string, error) {
	id, err := newReceiptID(rc.ID)
	if err != nil {
		return "", err
	}
	err = r.q.ReceiptCreate(ctx, db.ReceiptCreateParams{
		TenantID: rc.Tenant, ID: id, OrgIDSha256: rc.OrgIDHash, Status: string(rc.Status), RequestedAt: rc.RequestedAt,
		Banks: toInt32Sat(rc.Banks), ServerVersion: rc.ServerVersion,
	})
	if err != nil {
		return "", mapErr("create deletion receipt", err)
	}
	return id.String(), nil
}

// newReceiptID returns id parsed when set, else a new UUIDv7.
func newReceiptID(id string) (uuid.UUID, error) {
	if id != "" {
		return parseNewID(id)
	}
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: new receipt id: %w", domain.ErrUnavailable, err)
	}
	return u, nil
}

// Get returns one receipt of tenant; unknown = domain.ErrNotFound.
func (r *receiptRepo) Get(ctx context.Context, tenant, id string) (domain.DeletionReceipt, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return domain.DeletionReceipt{}, fmt.Errorf("%w: deletion receipt %q", domain.ErrNotFound, id)
	}
	row, err := r.q.ReceiptGet(ctx, db.ReceiptGetParams{TenantID: tenant, ID: uid})
	if err != nil {
		return domain.DeletionReceipt{}, mapErr(fmt.Sprintf("deletion receipt %s", id), err)
	}
	return receiptOf(row)
}

// Latest returns org o's most recently requested receipt; none = domain.ErrNotFound.
func (r *receiptRepo) Latest(ctx context.Context, o domain.OrgRef) (domain.DeletionReceipt, error) {
	row, err := r.q.ReceiptLatest(ctx, db.ReceiptLatestParams{TenantID: o.Tenant, OrgIDSha256: domain.OrgIDHash(o)})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DeletionReceipt{}, fmt.Errorf("%w: no deletion receipt for org %s", domain.ErrNotFound, o.Org)
	}
	if err != nil {
		return domain.DeletionReceipt{}, mapErr("latest deletion receipt", err)
	}
	return receiptOf(row)
}

// Update replaces rc's status, completed_at, verification and server version; unknown = domain.ErrNotFound.
func (r *receiptRepo) Update(ctx context.Context, rc domain.DeletionReceipt) error {
	uid, err := parseID(rc.ID)
	if err != nil {
		return err
	}
	verification, err := marshalPtr("receipt verification", rc.Verification)
	if err != nil {
		return err
	}
	n, err := r.q.ReceiptUpdate(ctx, db.ReceiptUpdateParams{
		Status: string(rc.Status), CompletedAt: rc.CompletedAt, Verification: verification,
		ServerVersion: rc.ServerVersion, TenantID: rc.Tenant, ID: uid,
	})
	if err != nil {
		return mapErr(fmt.Sprintf("update deletion receipt %s", rc.ID), err)
	}
	if n == 0 {
		return fmt.Errorf("%w: deletion receipt %s", domain.ErrNotFound, rc.ID)
	}
	return nil
}

// Prune deletes receipts completed before before.
func (r *receiptRepo) Prune(ctx context.Context, before time.Time) (int, error) {
	n, err := r.q.ReceiptPrune(ctx, before)
	if err != nil {
		return 0, mapErr("prune deletion receipts", err)
	}
	return int(n), nil
}

// receiptOf builds a domain.DeletionReceipt from its row.
func receiptOf(row db.DeletionReceipt) (domain.DeletionReceipt, error) {
	rc := domain.DeletionReceipt{
		ID: row.ID.String(), Tenant: row.TenantID, OrgIDHash: row.OrgIDSha256, Status: domain.ReceiptStatus(row.Status),
		RequestedAt: row.RequestedAt, CompletedAt: row.CompletedAt, Banks: int(row.Banks),
		ServerVersion: row.ServerVersion,
	}
	var err error
	rc.Verification, err = unmarshalPtr[domain.ReceiptVerification]("receipt verification", row.Verification)
	return rc, err
}
