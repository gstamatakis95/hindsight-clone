package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
)

// chunkRepo is port.ChunkRepo over one transaction.
type chunkRepo struct {
	q  *db.Queries
	tx *tx
}

var _ port.ChunkRepo = (*chunkRepo)(nil)

// Committed is not implemented yet.
func (r *chunkRepo) Committed(ctx context.Context, b port.BankRef, doc port.DocumentID) (map[int]string, error) {
	return nil, errNotImplemented
}

// Insert is not implemented yet.
func (r *chunkRepo) Insert(ctx context.Context, cs []domain.Chunk) error {
	return errNotImplemented
}
