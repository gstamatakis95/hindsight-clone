package arms

import (
	"context"
	"memd/internal/recall"
	"memd/internal/store"
)

const (
	// temporalMinSimilarity is the temporal arm's relevance floor: in-window units less similar to the query are
	// dropped (Hindsight pg/recall.py's in-window pool).
	temporalMinSimilarity = 0.1
	// defaultTemporalBuckets is recall.temporal_buckets when unset.
	defaultTemporalBuckets = 4
)

// Temporal is the "temporal" arm: units whose occurrence overlaps the query's time window.
type Temporal struct{ s store.Search }

var _ recall.WindowArm = (*Temporal)(nil)

// NewTemporal returns the temporal arm over s.
func NewTemporal(s store.Search) *Temporal { return &Temporal{s: s} }

// Name implements recall.Arm.
func (*Temporal) Name() string { return "temporal" }

// Description implements recall.Arm.
func (*Temporal) Description() string {
	return "Time window: memories that happened within the query's explicit or derived time window."
}

// MatchesWindow implements recall.WindowArm: the arm's hits get the temporal-proximity boost.
func (*Temporal) MatchesWindow() bool { return true }

// Retrieve implements recall.Arm: SearchTemporal over the query window, spread over recall.temporal_buckets
// (default 4), keeping units with cosine >= 0.1 to the query. Without a window it returns recall.ErrSkip.
func (a *Temporal) Retrieve(ctx context.Context, q recall.Query) ([]recall.Hit, error) {
	if q.Window == nil {
		return nil, recall.ErrSkip
	}
	buckets := defaultTemporalBuckets
	if v := q.ArmSettings(a.Name()).Recall.TemporalBuckets; v != nil {
		buckets = *v
	}
	hs, err := a.s.SearchTemporal(ctx, store.TemporalQuery{
		Bank: q.Bank, Window: *q.Window, Buckets: buckets, Types: q.Types, Tags: q.Tags, Seq: q.Seq, Limit: q.Limit,
		Embedding: q.Embedding, MinSimilarity: temporalMinSimilarity,
	})
	return hits(hs), err
}
