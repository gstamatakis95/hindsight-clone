package timex

import (
	"context"
	"memd/internal/domain"
	"time"
)

// TimeNormalizer resolves relative time expressions against a reference time, for stored facts and for
// recall queries.
type TimeNormalizer interface {
	// Normalize resolves f.TimeExpression against ref and fills f.OccurredStart and f.OccurredEnd in
	// place; a fact with no expression, or one already carrying absolute dates, is left unchanged.
	Normalize(ctx context.Context, f *domain.ExtractedFact, ref time.Time) error
	// QueryWindow is the query side: "last spring" -> window. The LLM runs only when a temporal cue is
	// detected and the rules can't resolve it; no cue means no window (nil) and no call.
	QueryWindow(ctx context.Context, query string, ref time.Time) (*domain.TimeWindow, error)
}
