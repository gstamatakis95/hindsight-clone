package postgres

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/db"
)

// chunkRepo is store.ChunkRepo over one transaction.
type chunkRepo struct {
	q  *db.Queries
	tx *tx
}

var _ store.ChunkRepo = (*chunkRepo)(nil)

// Committed returns the document's committed chunks as index -> content hash.
func (r *chunkRepo) Committed(ctx context.Context, b domain.BankRef, doc domain.DocumentID) (map[int]string, error) {
	rows, err := r.q.ChunkCommitted(ctx, db.ChunkCommittedParams{
		TenantID: b.Tenant, BankID: b.Bank, DocumentID: string(doc),
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("committed chunks of %s", doc), err)
	}
	out := make(map[int]string, len(rows))
	for _, row := range rows {
		out[int(row.ChunkIndex)] = row.ContentHash
	}
	return out, nil
}

// Insert stores chunks. A chunk at an already committed (document, index) replaces it: the old row is
// deleted (its units keep living with chunk_id NULL) and the new one is inserted under its own id, so units
// written afterwards can reference it. Re-inserting the same chunk id rewrites it in place. An unknown
// document is domain.ErrNotFound.
func (r *chunkRepo) Insert(ctx context.Context, cs []domain.Chunk) error {
	for _, c := range cs {
		id, err := parseNewID(c.ID)
		if err != nil {
			return err
		}
		idx := toInt32Sat(c.Index)
		err = r.q.ChunkDeleteReplaced(ctx, db.ChunkDeleteReplacedParams{
			TenantID: c.Bank.Tenant, BankID: c.Bank.Bank, DocumentID: string(c.DocumentID), ChunkIndex: idx, ID: id,
		})
		if err != nil {
			return mapErr("replace chunk", err)
		}
		err = r.q.ChunkInsert(ctx, db.ChunkInsertParams{
			ID: id, TenantID: c.Bank.Tenant, BankID: c.Bank.Bank, DocumentID: string(c.DocumentID), ChunkIndex: idx,
			Text: c.Text, ContentHash: c.ContentHash, TokenCount: toInt32Sat(c.TokenCount), CreatedAt: orNow(c.CreatedAt),
		})
		if err != nil {
			return mapErr(fmt.Sprintf("insert chunk %d of %s", c.Index, c.DocumentID), err)
		}
	}
	return nil
}
