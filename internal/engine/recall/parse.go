package recall

import (
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// ParsedQuery is a recall request after parsing: what every Retriever and the Scorer read.
type ParsedQuery struct {
	// Bank is the bank searched.
	Bank port.BankRef
	// Text is the query text.
	Text string
	// Embedding is the query vector from EmbedQuery, 768 dims.
	Embedding []float32
	// Window is the explicit or timex-resolved time window; nil = none (the temporal arm is skipped).
	Window *domain.TimeWindow
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags is the request's tag filter.
	Tags domain.TagFilter
	// Limit is the per-arm candidate limit from the budget: 100 / 300 / 1000.
	Limit int
	// At is the reference time for recency and temporal boosts.
	At time.Time
}
