package postgres

import (
	"context"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// StrandedFacts returns up to limit live facts whose embedding is NULL because their rederive_memory
// operation ended FAILED or CANCELLED, on banks that are not tombstoned, skipping units with a rederive
// that is pending, processing or ended less than retryAfter ago. Each unit carries only Bank, ID and
// DocumentID. It is not a port method: the worker's maintenance tick consumes it through its own
// interface (worker.StrandedFactSource), narrowed in cmd/memd/wire.go (decision 1452).
func (s *Store) StrandedFacts(ctx context.Context, retryAfter time.Duration, limit int) ([]domain.Unit, error) {
	if limit <= 0 {
		return []domain.Unit{}, nil
	}
	rows, err := db.New(s.pool).StrandedFacts(ctx, db.StrandedFactsParams{
		RetryAfterSeconds: retryAfter.Seconds(), Lim: int32(min(limit, 1<<20)), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, mapErr("stranded facts", err)
	}
	out := make([]domain.Unit, len(rows))
	for i, row := range rows {
		out[i] = domain.Unit{
			ID:   idOf[port.UnitID](row.ID),
			Bank: port.BankRef{Tenant: row.TenantID, Bank: row.BankID},
		}
		if row.DocumentID != nil {
			out[i].DocumentID = port.DocumentID(*row.DocumentID)
		}
	}
	return out, nil
}
