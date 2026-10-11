package recall

import (
	"memd/internal/domain"
	"time"
)

// candidate is one unit moving through the recall pipeline with every score it has collected.
type candidate struct {
	// ID is the unit.
	ID domain.UnitID
	// Unit is the hydrated unit; nil until hydrated (store.Reader.GetUnits).
	Unit *domain.Unit
	// Arms holds the candidate's rank and raw score per arm name; an absent arm did not return it.
	Arms map[string]armScore
	// RRF is the reciprocal-rank-fusion score.
	RRF float64
	// Reranker is the cross-encoder score in [0,1]; nil = not reranked.
	Reranker *float64
	// Boosts are the recency, temporal and proof boosts the scorer applied.
	Boosts boosts
	// Final is the final score after reranking and boosts.
	Final float64
	// Tokens is the unit text's token count, set when packing.
	Tokens int
}

// armScore is a candidate's position and raw score in one arm's list.
type armScore struct {
	// Rank is the 1-based rank in the arm's list.
	Rank int
	// Score is the arm's raw score (cosine similarity, BM25, proximity or path weight).
	Score float64
}

// Request mirrors memory.v1.Request with domain types.
type Request struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Query is the query text (max 500 tokens).
	Query string
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags is the tag filter: flat tags with a match mode, or tag groups.
	Tags domain.TagFilter
	// Budget is the effort level; unspecified = the RPC default.
	Budget domain.Budget
	// MaxTokens caps the packed results.
	MaxTokens int
	// QueryTime is the reference time for relative dates and recency; nil = now (clock.Clock).
	QueryTime *time.Time
	// Window is an explicit time window (time_window_start / end); nil = derived from the query by timex.
	Window *domain.TimeWindow
	// PreferObservations drops raw facts covered by a returned observation and backfills.
	PreferObservations bool
	// Arms are the arms to run by name; empty = the effective recall.arms (all registered arms by default). An
	// unknown or repeated name is domain.ErrInvalidArgument.
	Arms []string
	// MinScores are the arm and result floors; an unknown arm name is domain.ErrInvalidArgument.
	MinScores domain.MinScores
	// Include asks for chunks, source facts and entity state.
	Include domain.RecallInclude
	// Trace fills Result.Trace.
	Trace bool
	// Overrides are request-level config overrides; nil = none.
	Overrides *domain.BankConfig
	// Seq bounds the units' seq; zero = unbounded. Internal callers only (mental-model refreshes read a
	// stable snapshot); not on the wire.
	Seq domain.SeqRange
}

// Result mirrors memory.v1.RecallResponse with domain types.
type Result struct {
	// Results are the packed memories, best first.
	Results []Memory
	// SourceFacts are the facts behind returned observations, by id (include.source_facts).
	SourceFacts map[domain.UnitID]domain.Unit
	// SourceFactsTruncated reports that the source-fact budget cut some off.
	SourceFactsTruncated bool
	// Chunks are the source chunks of returned memories, by id (include.chunks).
	Chunks map[domain.ChunkID]Chunk
	// Entities is the entity state of returned memories, by id (include.entities).
	Entities map[domain.EntityID]domain.EntityState
	// Trace explains the run; nil unless requested.
	Trace *Trace
}

// Memory is one returned unit with its scores.
type Memory struct {
	// Unit is the memory unit.
	Unit domain.Unit
	// Scores are the unit's scores.
	Scores Scores
	// Stale marks an observation whose scope still has unconsolidated facts.
	Stale bool
}

// Scores mirrors memory.v1.Scores.
type Scores struct {
	// Final is the final score.
	Final float64
	// Reranker is the cross-encoder score; nil = not reranked.
	Reranker *float64
	// Arms are the raw scores by arm name of the arms that returned the unit.
	Arms map[string]float64
}

// Trace mirrors memory.v1.Trace.
type Trace struct {
	// Arms has one entry per arm that ran, in fusion order (an arm that returned ErrSkip has none).
	Arms []ArmTrace
	// Fused is the number of candidates after fusion.
	Fused int
	// Reranked is the number of candidates sent to the cross-encoder.
	Reranked int
	// Packed is the number of results returned.
	Packed int
	// TokenBudget is the max_tokens budget packing used.
	TokenBudget int
	// TokensUsed is the token count of the packed results.
	TokensUsed int
	// Window is the query's time window (explicit or resolved by TIME_PARSE); nil = none.
	Window *domain.TimeWindow
	// DurationMS is the recall duration.
	DurationMS int64
}

// ArmTrace mirrors memory.v1.ArmTrace: one arm's contribution.
type ArmTrace struct {
	// Arm is the arm name.
	Arm string
	// Candidates is the number of candidates the arm returned.
	Candidates int
	// DurationMS is the arm's duration.
	DurationMS int64
	// TopIDs are the arm's best few unit ids.
	TopIDs []domain.UnitID
}
