package postgres

import (
	"context"
	"memd/internal/domain"
	"memd/internal/store/postgres/db"
	"time"
)

// StrandedFacts returns up to limit live facts whose embedding is NULL because their rederive_memory
// operation ended FAILED or CANCELLED, on banks that are not tombstoned, skipping units with a rederive
// that is pending, processing or ended less than retryAfter ago. Each unit carries only Bank, ID and
// DocumentID. It implements store.Maintenance.
func (s *DB) StrandedFacts(ctx context.Context, retryAfter time.Duration, limit int) ([]domain.Unit, error) {
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
			ID:   idOf[domain.UnitID](row.ID),
			Bank: domain.BankRef{Tenant: row.TenantID, Bank: row.BankID},
		}
		if row.DocumentID != nil {
			out[i].DocumentID = domain.DocumentID(*row.DocumentID)
		}
	}
	return out, nil
}
