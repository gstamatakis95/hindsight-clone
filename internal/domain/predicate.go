package domain

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

// PredicateOp names a node of an TagPredicate tree.
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

// TagPredicate is a compiled tag filter shared by all four recall arms. It is a backend-neutral tree
// over a unit's tags whose leaves hold resolved, normalized tag names (fuzzy leaves already resolved by
// recall.Engine.CompileTags). The Postgres adapter renders it as one WHERE fragment over memory_units.tags;
// the in-memory fake evaluates it with Eval. A nil *TagPredicate means no tag filter.
type TagPredicate struct {
	// Op is the node type.
	Op PredicateOp
	// Tags are the leaf's tags (PredicateTags only).
	Tags []string
	// Match is the leaf's mode with MatchTags semantics (PredicateTags only).
	Match TagsMatch
	// Args are the children of and / or (any number) and not (exactly one).
	Args []TagPredicate
}

// Eval reports whether a unit with the given tags satisfies p. It is the reference semantics every
// adapter's rendering must agree with, and it matches MatchGroup: an empty and is true, an empty or
// is false, and a malformed node (unknown op, a not without exactly one argument) matches nothing.
func (p *TagPredicate) Eval(unitTags []string) bool {
	if p == nil {
		return true
	}
	switch p.Op {
	case PredicateTags:
		return MatchTags(unitTags, p.Tags, p.Match)
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
