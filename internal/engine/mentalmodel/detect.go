package mentalmodel

import (
	"context"
	"memd/internal/domain"
)

// ChangeDetector decides whether a mental model is stale.
type ChangeDetector interface {
	// NeedsRefresh reports stale if an in-scope unit or observation has seq > m.LastSeqSeen, or a source
	// was retracted. upTo is the bank seq read first: the snapshot bound for the refresh.
	NeedsRefresh(ctx context.Context, m domain.MentalModel) (stale bool, upTo int64, err error)
}
