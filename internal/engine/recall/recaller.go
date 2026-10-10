package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// Arm names, as returned by Retriever.Name and used as keys of Fuser lists, Candidate.Arms,
// RecallConfig.StrategyBoosts and RecallConfig.EnabledStrategies.
const (
	// ArmSemantic is the HNSW cosine arm.
	ArmSemantic = "semantic"
	// ArmKeyword is the BM25 arm.
	ArmKeyword = "keyword"
	// ArmGraph is the link-expansion arm.
	ArmGraph = "graph"
	// ArmTemporal is the occurrence-overlap arm.
	ArmTemporal = "temporal"
)

// Recaller answers one recall request.
type Recaller interface {
	// Recall parses, retrieves over all enabled arms, fuses, scores, gates, packs and enriches.
	Recall(ctx context.Context, req RecallRequest) (RecallResult, error)
}

// Retriever is one recall arm.
type Retriever interface {
	// Name is the arm name: semantic | keyword | graph | temporal.
	Name() string
	// Retrieve returns the arm's candidates for q, best first, at most q.Limit.
	Retrieve(ctx context.Context, q *ParsedQuery) ([]Candidate, error)
}

// Candidate is one unit moving through the recall pipeline with every score it has collected.
type Candidate struct {
	// ID is the unit.
	ID port.UnitID
	// Unit is the hydrated unit; nil until hydrated (port.Reader.GetUnits).
	Unit *domain.Unit
	// Arms holds the candidate's rank and raw score per arm name; an absent arm did not return it.
	Arms map[string]ArmScore
	// RRF is the reciprocal-rank-fusion score.
	RRF float64
	// Reranker is the cross-encoder score in [0,1]; nil = not reranked.
	Reranker *float64
	// Boosts are the recency, temporal and proof boosts the Scorer applied.
	Boosts Boosts
	// Final is the final score after reranking and boosts.
	Final float64
	// Tokens is the unit text's token count, set when packing.
	Tokens int
}

// ArmScore is a candidate's position and raw score in one arm's list.
type ArmScore struct {
	// Rank is the 1-based rank in the arm's list.
	Rank int
	// Score is the arm's raw score (cosine similarity, BM25, proximity or path weight).
	Score float64
}

// RecallRequest mirrors memory.v1.RecallRequest with domain types.
type RecallRequest struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Bank is the bank searched.
	Bank port.BankRef
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
	// QueryTime is the reference time for relative dates and recency; nil = now (port.Clock).
	QueryTime *time.Time
	// Window is an explicit time window (time_window_start / end); nil = derived from the query by timex.
	Window *domain.TimeWindow
	// PreferObservations drops raw facts covered by a returned observation and backfills.
	PreferObservations bool
	// MinScores are the arm and result floors.
	MinScores domain.MinScores
	// Include asks for chunks, source facts and entity state.
	Include domain.RecallInclude
	// Trace fills RecallResult.Trace.
	Trace bool
	// Overrides are request-level config overrides; nil = none.
	Overrides *domain.BankConfig
	// Seq bounds the units' seq; zero = unbounded. Internal callers only (mental-model refreshes read a
	// stable snapshot); not on the wire.
	Seq port.SeqRange
}

// RecallResult mirrors memory.v1.RecallResponse with domain types.
type RecallResult struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Results are the packed memories, best first.
	Results []Memory
	// SourceFacts are the facts behind returned observations, by id (include.source_facts).
	SourceFacts map[port.UnitID]domain.Unit
	// SourceFactsTruncated reports that the source-fact budget cut some off.
	SourceFactsTruncated bool
	// Chunks are the source chunks of returned memories, by id (include.chunks).
	Chunks map[port.ChunkID]Chunk
	// Entities is the entity state of returned memories, by id (include.entities).
	Entities map[port.EntityID]domain.EntityState
	// Trace explains the run; nil unless requested.
	Trace *RecallTrace
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
	// Semantic is the cosine similarity; nil = not returned by the semantic arm.
	Semantic *float64
	// Keyword is the BM25 score; nil = not returned by the keyword arm.
	Keyword *float64
}

// RecallTrace mirrors memory.v1.RecallTrace.
type RecallTrace struct { //nolint:revive // stutter: name fixed by the spec and the proto message it mirrors
	// Arms has one entry per arm that ran: semantic, keyword, graph, temporal.
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
	// Window is the time window the temporal arm used (explicit or resolved by TIME_PARSE); nil = none.
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
	TopIDs []port.UnitID
}
