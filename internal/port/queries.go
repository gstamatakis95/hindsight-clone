package port

import "memd/internal/domain"

// SeqRange bounds a search to units whose seq is in (After, *UpTo]. The zero SeqRange matches everything.
// Mental-model refreshes use it to read a stable snapshot (UpTo = the BankSeq S they read first; S = 0
// means an empty snapshot that matches nothing) or only what changed since last_seq_seen (After).
type SeqRange struct {
	// After excludes units with seq <= After; 0 = no lower bound (every seq is >= 1).
	After int64
	// UpTo excludes units with seq > *UpTo; nil = no upper bound. A non-nil 0 matches nothing.
	UpTo *int64
}

// SeqUpTo returns a SeqRange holding everything with seq in (after, upTo].
func SeqUpTo(after, upTo int64) SeqRange {
	return SeqRange{After: after, UpTo: &upTo}
}

// Contains reports whether seq lies in the range.
func (r SeqRange) Contains(seq int64) bool {
	return seq > r.After && (r.UpTo == nil || seq <= *r.UpTo)
}

// PredicateOp names a node of an SQLPredicate tree.
type PredicateOp string

// Predicate operators.
const (
	// PredicateTags is a leaf: the unit's tags compared with Tags under Match.
	PredicateTags PredicateOp = "tags"
	// PredicateAnd holds when every argument holds (an empty And is true).
	PredicateAnd PredicateOp = "and"
	// PredicateOr holds when at least one argument holds (an empty Or is false).
	PredicateOr PredicateOp = "or"
	// PredicateNot holds when its single argument does not (a not without exactly one argument is false).
	PredicateNot PredicateOp = "not"
)

// SQLPredicate is a compiled tag filter shared by all four recall arms. It is a backend-neutral tree
// over a unit's tags whose leaves hold resolved, normalized tag names (fuzzy leaves already resolved by
// the TagFilterCompiler). The Postgres adapter renders it as one WHERE fragment over memory_units.tags;
// the in-memory fake evaluates it with Eval. A nil *SQLPredicate means no tag filter.
type SQLPredicate struct {
	// Op is the node type.
	Op PredicateOp
	// Tags are the leaf's tags (PredicateTags only).
	Tags []string
	// Match is the leaf's mode with domain.MatchTags semantics (PredicateTags only).
	Match domain.TagsMatch
	// Args are the children of and / or (any number) and not (exactly one).
	Args []SQLPredicate
}

// Eval reports whether a unit with the given tags satisfies p. It is the reference semantics every
// adapter's rendering must agree with, and it matches domain.MatchGroup: an empty and is true, an empty or
// is false, and a malformed node (unknown op, a not without exactly one argument) matches nothing.
func (p *SQLPredicate) Eval(unitTags []string) bool {
	if p == nil {
		return true
	}
	switch p.Op {
	case PredicateTags:
		return domain.MatchTags(unitTags, p.Tags, p.Match)
	case PredicateAnd:
		for i := range p.Args {
			if !p.Args[i].Eval(unitTags) {
				return false
			}
		}
		return true
	case PredicateOr:
		for i := range p.Args {
			if p.Args[i].Eval(unitTags) {
				return true
			}
		}
		return false
	case PredicateNot:
		return len(p.Args) == 1 && !p.Args[0].Eval(unitTags)
	default:
		return false
	}
}

// VectorQuery is one semantic-arm search.
type VectorQuery struct {
	// Bank is the bank searched.
	Bank BankRef
	// Embedding is the query vector (domain.EmbeddingDim floats, search_query: prefix applied).
	Embedding []float32
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *SQLPredicate
	// MinSimilarity is the cosine-similarity floor (min_scores.semantic); 0 = none.
	MinSimilarity float64
	// Seq bounds the units' seq.
	Seq SeqRange
	// Limit caps the hits.
	Limit int
}

// TextQuery is one keyword-arm (BM25) search.
type TextQuery struct {
	// Bank is the bank searched.
	Bank BankRef
	// Text is the query text.
	Text string
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *SQLPredicate
	// MinScore is the BM25 score floor (min_scores.keyword); 0 = none.
	MinScore float64
	// Seq bounds the units' seq.
	Seq SeqRange
	// Limit caps the hits.
	Limit int
}

// TemporalQuery is one temporal-arm search: units whose occurrence (or mention) overlaps Window.
type TemporalQuery struct {
	// Bank is the bank searched.
	Bank BankRef
	// Window is the time window; at least one bound is set.
	Window domain.TimeWindow
	// Buckets spreads hits evenly over this many sub-windows; 0 or 1 = no bucketing.
	Buckets int
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *SQLPredicate
	// Seq bounds the units' seq.
	Seq SeqRange
	// Limit caps the hits.
	Limit int
}

// GraphQuery is one graph-arm expansion from seed units over memory_links.
type GraphQuery struct {
	// Bank is the bank searched.
	Bank BankRef
	// Seeds are the starting units (excluded from the hits).
	Seeds []UnitID
	// MaxHops bounds the expansion (1-2).
	MaxHops int
	// Kinds limits link kinds; empty = all.
	Kinds []domain.LinkKind
	// Types limits fact types of the reached units; empty = all.
	Types []domain.FactType
	// Tags filters reached units by tags; nil = no filter.
	Tags *SQLPredicate
	// Seq bounds the reached units' seq.
	Seq SeqRange
	// Limit caps the hits.
	Limit int
}

// Hit is one search result: a unit id and its arm-specific score (cosine similarity for semantic, BM25
// for keyword, proximity for temporal, path weight for graph). Higher is better.
type Hit struct {
	// ID is the unit.
	ID UnitID
	// Score is the arm's score.
	Score float64
}
