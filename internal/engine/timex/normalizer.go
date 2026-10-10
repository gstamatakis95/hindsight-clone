package timex

import (
	"context"
	"memd/internal/domain"
	"time"
)

// TimeNormalizer resolves relative time expressions against a reference time, for stored facts and for
// recall queries. cfg is the caller's effective (already merged) BankConfig; it is consulted only for the
// TIME_PARSE binding, and only when the rules cannot resolve a cue, so per-request TIME_PARSE overrides
// reach the fallback call and an unbound slot never fails a call the rules can answer.
type TimeNormalizer interface {
	// Normalize resolves f.TimeExpression against ref and fills f.OccurredStart and f.OccurredEnd in
	// place; a fact with no expression, or one already carrying absolute dates, is left unchanged.
	Normalize(ctx context.Context, cfg domain.BankConfig, f *domain.ExtractedFact, ref time.Time) error
	// QueryWindow is the query side: "last spring" -> window. The LLM runs only when a temporal cue is
	// detected and the rules can't resolve it; no cue means no window (nil) and no call.
	QueryWindow(ctx context.Context, cfg domain.BankConfig, query string, ref time.Time) (*domain.TimeWindow, error)
}
