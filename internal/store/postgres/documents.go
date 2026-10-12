package postgres

import (
	"context"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
)

// documentRepo is store.DocumentRepo over one transaction.
type documentRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.DocumentRepo = (*documentRepo)(nil)

// Get returns the document with its live unit counts; an unknown id is domain.ErrNotFound.
func (r *documentRepo) Get(ctx context.Context, b domain.BankRef, id domain.DocumentID) (domain.Document, error) {
	row, err := r.q.DocumentGet(ctx, db.DocumentGetParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id),
	})
	if err != nil {
		return domain.Document{}, mapErr(fmt.Sprintf("document %s", id), err)
	}
	d, err := documentOf(row)
	if err != nil {
		return domain.Document{}, err
	}
	counts, err := r.q.DocumentUnitCounts(ctx, db.DocumentUnitCountsParams{
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, DocumentID: textPtr(id),
	})
	if err != nil {
		return domain.Document{}, mapErr("document unit counts", err)
	}
	for _, c := range counts {
		d.MemoriesByFactType[domain.FactType(c.FactType)] = c.N
		d.MemoryCount += c.N
	}
	return d, nil
}

// documentOf builds a domain.Document from its row, without the unit counts (MemoriesByFactType is empty).
func documentOf(row db.Document) (domain.Document, error) {
	d := domain.Document{
		ID: domain.DocumentID(row.ID), Bank: domain.NewBankRef(row.TenantID, row.OrgID, row.BankID),
		ContentHash: row.ContentHash, OverridesHash: row.OverridesHash, Tags: row.Tags, BlobKey: row.BlobKey,
		ExtractedAt: row.ExtractedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Revision: row.Revision,
		MemoriesByFactType: map[domain.FactType]int64{},
	}
	var err error
	if d.Overrides, err = unmarshalPtr[domain.BankConfig]("document overrides", row.Overrides); err != nil {
		return domain.Document{}, err
	}
	d.ObservationScopeModes, err = unmarshalPtr[domain.ObservationScopeModes]("document observation scopes",
		row.ObservationScopeModes)
	if err != nil {
		return domain.Document{}, err
	}
	return d, nil
}

// Upsert inserts the document or replaces its tags, overrides, observation scopes and blob key, and clears
// extracted_at; the hashes of the last completed retain stay until Complete. A nil ObservationScopeModes is
// stored as NULL; a nil Revision keeps the stored revision. An unknown bank is domain.ErrNotFound.
func (r *documentRepo) Upsert(ctx context.Context, d domain.Document) error {
	if err := checkTags(d.Tags); err != nil {
		return err
	}
	overrides, err := marshalPtr("document overrides", d.Overrides)
	if err != nil {
		return err
	}
	scopes, err := marshalPtr("document observation scopes", d.ObservationScopeModes)
	if err != nil {
		return err
	}
	err = r.q.DocumentUpsert(ctx, db.DocumentUpsertParams{
		TenantID: d.Bank.Tenant, OrgID: d.Bank.Org, BankID: d.Bank.Bank, ID: string(d.ID), Overrides: overrides,
		Tags: tagsOrEmpty(d.Tags), BlobKey: d.BlobKey, ObservationScopeModes: scopes, Revision: d.Revision,
	})
	return mapErr(fmt.Sprintf("upsert document %s", d.ID), err)
}

// Complete stamps content_hash, overrides_hash and extracted_at; an unknown id is domain.ErrNotFound.
func (r *documentRepo) Complete(
	ctx context.Context, b domain.BankRef, id domain.DocumentID, contentHash, overridesHash string,
) error {
	n, err := r.q.DocumentComplete(ctx, db.DocumentCompleteParams{
		ContentHash: contentHash, OverridesHash: overridesHash,
		TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id),
	})
	return docRowsErr(fmt.Sprintf("complete document %s", id), n, err)
}

// SetTags replaces the document's tag set; an unknown id is domain.ErrNotFound.
func (r *documentRepo) SetTags(ctx context.Context, b domain.BankRef, id domain.DocumentID, tags []string) error {
	if err := checkTags(tags); err != nil {
		return err
	}
	n, err := r.q.DocumentSetTags(ctx, db.DocumentSetTagsParams{
		Tags: tagsOrEmpty(tags), TenantID: b.Tenant, OrgID: b.Org, BankID: b.Bank, ID: string(id),
	})
	return docRowsErr(fmt.Sprintf("set tags of document %s", id), n, err)
}

// docRowsErr maps the result of a single-row update or delete: a driver error through mapErr, no row
// affected to domain.ErrNotFound.
func docRowsErr(what string, n int64, err error) error {
	if err != nil {
		return mapErr(what, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	return nil
}

// toInt32Sat converts n to int32, saturating at the int32 range.
func toInt32Sat(n int) int32 {
	switch {
	case n > math.MaxInt32:
		return math.MaxInt32
	case n < math.MinInt32:
		return math.MinInt32
	default:
		return int32(n)
	}
}
