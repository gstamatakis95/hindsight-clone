package recall

import (
	"context"
	"errors"
	"memd/internal/domain"
)

// Arm is one retrieval strategy of recall: vector, text, graph and temporal are built in (internal/recall/arms), and
// cmd/memd/wire.go registers every arm through [Deps.Arms]. The engine runs the selected arms concurrently and treats
// them alike: it fuses their lists by name, applies arm_boosts by name, gives each its min_scores entry through
// [Query.ArmSettings] and traces each by name. It re-checks every hit after hydration (same bank, live, types, tag
// predicate, seq bound), so an arm cannot leak a unit the query excludes.
//
// An arm is read-only: it must never write, and it must be safe for concurrent use.
type Arm interface {
	// Name is the arm's unique name, as clients select it (RecallRequest.arms), configure it
	// (recall.arms, recall.arm_boosts, min_scores.arms) and see it in traces.
	Name() string
	// Description is a one-line description of what the arm retrieves, listed by ListRecallArms.
	Description() string
	// Retrieve returns the arm's hits for q, best first (non-increasing score), at most q.Limit, all in q.Bank.
	// It returns ErrSkip when the arm does not apply to q (temporal without a window), and an error when it
	// fails, which fails the recall. It must honour ctx cancellation.
	Retrieve(ctx context.Context, q Query) ([]Hit, error)
}

// WindowArm is an optional Arm capability: an arm whose hits are matches for the query's time window. When the
// query has a window, the scorer gives the hits of such an arm the temporal-proximity boost (recall.temporal_alpha);
// other hits get a neutral one. The built-in temporal arm implements it.
type WindowArm interface {
	Arm
	// MatchesWindow reports whether the arm's hits are time-window matches.
	MatchesWindow() bool
}

// ErrSkip is returned by Arm.Retrieve when the arm does not apply to the query. The arm is then left out of fusion
// and of the trace; it is not a failure.
var ErrSkip = errors.New("recall: arm does not apply to this query")

// Hit is one arm result: a unit and the arm's raw score for it (higher is better).
type Hit struct {
	// ID is the unit.
	ID domain.UnitID
	// Score is the arm's raw score: cosine similarity, BM25, path weight or proximity.
	Score float64
}

// Query is what an arm retrieves for: the parsed recall request. The engine builds one per recall and hands each arm
// a copy; arms read it and never modify the slices it shares.
type Query struct {
	// Bank is the bank searched; every hit must belong to it.
	Bank domain.BankRef
	// Text is the query text, cut to MaxQueryTokens.
	Text string
	// Embedding is the query vector (domain.EmbeddingDim floats).
	Embedding []float32
	// Window is the explicit or derived time window; nil = none.
	Window *domain.TimeWindow
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags is the request's tag filter, compiled once (fuzzy leaves resolved); nil = no filter.
	Tags *domain.TagPredicate
	// Seq bounds the units' seq; zero = unbounded.
	Seq domain.SeqRange
	// Limit is the per-arm candidate limit from the budget (budget_low / mid / high).
	Limit int
	// Budget is the request's effort level.
	Budget domain.Budget
	// minScores are the request's arm floors by arm name.
	minScores map[string]float64
	// recall is the call's effective recall config.
	recall domain.RecallConfig
}

// ArmSettings are one arm's effective settings for a recall.
type ArmSettings struct {
	// MinScore is the request's min_scores.arms entry for the arm; nil = unset (the arm applies its default).
	MinScore *float64
	// Recall is the call's effective recall config, for the arm's own knobs (graph_max_hops, temporal_buckets).
	Recall domain.RecallConfig
}

// ArmSettings returns the effective settings of the arm called name.
func (q Query) ArmSettings(name string) ArmSettings {
	s := ArmSettings{Recall: q.recall}
	if v, ok := q.minScores[name]; ok {
		s.MinScore = &v
	}
	return s
}

// ArmInfo describes one registered arm.
type ArmInfo struct {
	// Name is the arm name.
	Name string
	// Description is the arm's one-line description.
	Description string
}
