package linking

import (
	"context"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ResolveInput is the entity mentions of one fact to resolve.
type ResolveInput struct {
	// Mentions are the fact's extracted mentions.
	Mentions []domain.Mention
	// Hints are the retain item's caller-supplied entities, merged with Mentions (deduplicated by name,
	// case-insensitively).
	Hints []domain.EntityHint
	// Literal takes the caller's hint names as given (resolve_entities=false): they match an existing entity
	// only by exact (case-insensitive) name, never fuzzily. Extracted mentions always resolve, as in
	// Hindsight, unless a hint names them too.
	Literal bool
	// At is the fact's occurrence or mention time, used for temporal proximity; zero = unknown.
	At time.Time
	// Threshold is the merge threshold (retain.entity_match_threshold).
	Threshold float64
}

// ResolverConfig tunes the Resolver.
type ResolverConfig struct {
	// CandidateLimit is how many trigram candidates are scored per mention; 0 = 10.
	CandidateLimit int
}

// DefaultEntityMatchThreshold is the merge threshold when retain.entity_match_threshold is unset (Hindsight: 0.6,
// inclusive).
const DefaultEntityMatchThreshold = 0.6

// Scoring weights and limits (Hindsight's entity resolver).
const (
	nameWeight         = 0.5
	cooccurrenceWeight = 0.3
	temporalWeight     = 0.2
	temporalDays       = 7.0
	maxEntityNameRune  = 512
	defaultCandidates  = 10
	// mergeMinSimilarity is the trigram floor a candidate must reach before it is scored at all
	// (Hindsight's DEFAULT_ENTITY_MERGE_MIN_SIMILARITY).
	mergeMinSimilarity = 0.3
	// minTokenSimilarity is the sequence ratio at which two words count as the same word.
	minTokenSimilarity = 0.6
)

// Resolver maps a fact's free-form entity mentions to existing or new entities over store.EntityRepo.
type Resolver struct {
	limit int
}

// NewResolver returns the Resolver.
func NewResolver(cfg ResolverConfig) *Resolver {
	if cfg.CandidateLimit <= 0 {
		cfg.CandidateLimit = defaultCandidates
	}
	return &Resolver{limit: cfg.CandidateLimit}
}

// nameIn is one distinct name to resolve.
type nameIn struct {
	name, kind string
	literal    bool
}

// Resolve gates candidates (same kind, trigram floor, word-by-word agreement), scores the rest by name similarity +
// co-occurrence overlap + temporal proximity and merges a mention into the best candidate at or above the
// threshold, else creates an entity. It runs inside the chunk batch's transaction and makes no model call. It
// returns one id per distinct name, in mention order (mentions first, then hints).
func (r *Resolver) Resolve(
	ctx context.Context, tx store.Tx, b domain.BankRef, in ResolveInput,
) ([]domain.EntityID, error) {
	names := mergeNames(in)
	cands := make([][]domain.EntityCandidate, len(names))
	for i, n := range names {
		cs, err := tx.Entities().Candidates(ctx, b, n.name, r.limit)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if !c.Entity.IsLabel() {
				cands[i] = append(cands[i], c)
			}
		}
	}
	ids := make([]domain.EntityID, 0, len(names))
	seen := map[domain.EntityID]bool{}
	for i, n := range names {
		id, err := r.resolveOne(ctx, tx, b, n, cands[i], nearby(names, cands, i), len(names)-1, in)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// resolveOne merges n into its best candidate or creates a new entity. others are the existing entities of
// the fact's other names; nOthers is how many other names the fact has (the co-occurrence denominator).
func (r *Resolver) resolveOne(
	ctx context.Context, tx store.Tx, b domain.BankRef, n nameIn, cands []domain.EntityCandidate,
	others []domain.EntityID, nOthers int, in ResolveInput,
) (domain.EntityID, error) {
	var best *domain.EntityCandidate
	bestScore := 0.0
	for i := range cands {
		c := &cands[i]
		if !mergeable(n, *c) {
			continue
		}
		if n.literal {
			if strings.EqualFold(c.Entity.CanonicalName, n.name) {
				best, bestScore = c, 1
				break
			}
			continue
		}
		cooc := 0
		if len(others) > 0 {
			var err error
			if cooc, err = tx.Entities().Cooccurrence(ctx, b, c.Entity.ID, others); err != nil {
				return "", err
			}
		}
		if s := scoreCandidate(*c, n.name, cooc, nOthers, in.At); s > bestScore {
			best, bestScore = c, s
		}
	}
	if best != nil && meetsThreshold(bestScore, in.Threshold) {
		return touchEntity(ctx, tx, best.Entity, in.At)
	}
	e := domain.Entity{Bank: b, CanonicalName: n.name, Kind: n.kind}
	if !in.At.IsZero() {
		at := in.At
		e.FirstSeen, e.LastSeen = &at, &at
	}
	return tx.Entities().Upsert(ctx, e)
}

// meetsThreshold compares a score with the merge threshold inclusively, on the score rounded to 6 places, so
// equal scores compare equal whichever signals produced them (0.3 + 0.3 and 0.4 + 0.2 both reach 0.6).
func meetsThreshold(score, threshold float64) bool {
	return math.Round(score*1e6)/1e6 >= threshold
}

// mergeable applies the merge gates before scoring. An identical name always passes, whatever its kind (as in
// Hindsight: "Kubernetes" extracted once as a technology and once as a concept is one entity). A fuzzy match
// must have the same kind when both are known (case-insensitive), reach mergeMinSimilarity and agree word by
// word (tokensCompatible). Co-occurrence and recency can never merge a name that fails.
func mergeable(n nameIn, c domain.EntityCandidate) bool {
	if identicalName(c, n.name) {
		return true
	}
	if n.kind != "" && c.Entity.Kind != "" && !strings.EqualFold(n.kind, c.Entity.Kind) {
		return false
	}
	return c.NameSimilarity >= mergeMinSimilarity && tokensCompatible(n.name, c.Entity.CanonicalName)
}

// identicalName reports whether the candidate's name is the mention's: equal case-insensitively, or with
// identical trigram sets (the same words, differing only in case, punctuation or decoration).
func identicalName(c domain.EntityCandidate, name string) bool {
	return strings.EqualFold(c.Entity.CanonicalName, name) || c.NameSimilarity >= 1
}

// scoreCandidate is Hindsight's score: an identical name (case-insensitive, or identical trigrams) is 1;
// otherwise 0.5 x name similarity + 0.3 x the share of the fact's other entities the candidate co-occurs with
// + 0.2 x temporal proximity (last seen within 7 days of at, linear falloff).
func scoreCandidate(c domain.EntityCandidate, name string, cooc, others int, at time.Time) float64 {
	if identicalName(c, name) {
		return 1
	}
	score := c.NameSimilarity * nameWeight
	if others > 0 {
		score += float64(cooc) / float64(others) * cooccurrenceWeight
	}
	if c.Entity.LastSeen != nil && !at.IsZero() {
		days := math.Abs(at.Sub(*c.Entity.LastSeen).Hours()) / 24
		if days < temporalDays {
			score += (1 - days/temporalDays) * temporalWeight
		}
	}
	return score
}

// touchEntity widens the merged entity's first / last seen to include at and returns its id.
func touchEntity(ctx context.Context, tx store.Tx, e domain.Entity, at time.Time) (domain.EntityID, error) {
	if at.IsZero() {
		return e.ID, nil
	}
	changed := false
	if e.FirstSeen == nil || at.Before(*e.FirstSeen) {
		e.FirstSeen, changed = &at, true
	}
	if e.LastSeen == nil || at.After(*e.LastSeen) {
		e.LastSeen, changed = &at, true
	}
	if !changed {
		return e.ID, nil
	}
	return tx.Entities().Upsert(ctx, e)
}

// nearby returns, for every other name, the existing entity it would merge into on the name alone: its first
// candidate that passes the gates and has an identical name. These are the fact's other entities for the
// co-occurrence signal. As in Hindsight, which matches co-occurring entities by name, a merely similar
// entity does not count.
func nearby(names []nameIn, cands [][]domain.EntityCandidate, self int) []domain.EntityID {
	var out []domain.EntityID
	for i, cs := range cands {
		if i == self {
			continue
		}
		for _, c := range cs {
			if mergeable(names[i], c) && identicalName(c, names[i].name) {
				out = append(out, c.Entity.ID)
				break
			}
		}
	}
	return out
}

// mergeNames merges mentions and hints into distinct names: whitespace collapsed, blank or over-long names
// dropped, duplicates (case-insensitive) merged. A hint naming an extracted mention makes it literal when the
// caller asked for literal names, and fills a missing kind.
func mergeNames(in ResolveInput) []nameIn {
	var out []nameIn
	at := map[string]int{}
	add := func(name, kind string, literal bool) {
		name, kind = strings.Join(strings.Fields(name), " "), strings.ToLower(strings.TrimSpace(kind))
		if name == "" || utf8.RuneCountInString(name) > maxEntityNameRune {
			return
		}
		key := strings.ToLower(name)
		if i, ok := at[key]; ok {
			out[i].literal = out[i].literal || literal
			if out[i].kind == "" {
				out[i].kind = kind
			}
			return
		}
		at[key] = len(out)
		out = append(out, nameIn{name: name, kind: kind, literal: literal})
	}
	for _, m := range in.Mentions {
		add(m.Name, m.Kind, false)
	}
	for _, h := range in.Hints {
		add(h.Text, h.Type, in.Literal)
	}
	return out
}

// Word and number patterns for tokensCompatible (Hindsight's TRGM_WORD and _DIGIT_RUN).
var (
	wordRE  = regexp.MustCompile(`[\p{L}\p{N}]+`)
	digitRE = regexp.MustCompile(`\p{Nd}+(?:\.\p{Nd}+)*`)
)

// tokensCompatible is Hindsight's _tokens_are_compatible: whether two names agree word by word. Names that
// each carry a number the other lacks are different things ("Room 101" / "Room 102", "UA123" / "UA124").
// Otherwise single-word names pass (the whole-name floor already judged them), and for multi-word names every
// word of the shorter name must match a word of the longer one (tokensMatch), so a long shared word cannot
// drown out a different short one ("John Smith" / "Jane Smith").
func tokensCompatible(a, b string) bool {
	na, nb := numbersIn(a), numbersIn(b)
	if hasOther(na, nb) && hasOther(nb, na) {
		return false
	}
	ta, tb := wordRE.FindAllString(strings.ToLower(a), -1), wordRE.FindAllString(strings.ToLower(b), -1)
	if len(ta) < 2 && len(tb) < 2 {
		return true
	}
	short, rest := ta, tb
	if len(ta) > len(tb) {
		short, rest = tb, ta
	}
	for _, w := range short {
		if !slices.ContainsFunc(rest, func(o string) bool { return tokensMatch(w, o) }) {
			return false
		}
	}
	return true
}

// tokensMatch reports whether two words are plausibly the same word: equal, one a prefix of the other (an
// abbreviation), or a sequence ratio of at least minTokenSimilarity.
func tokensMatch(a, b string) bool {
	return a == b || strings.HasPrefix(a, b) || strings.HasPrefix(b, a) ||
		sequenceRatio([]rune(a), []rune(b)) >= minTokenSimilarity
}

// hasOther reports whether a holds a number b lacks.
func hasOther(a, b map[string]bool) bool {
	for n := range a {
		if !b[n] {
			return true
		}
	}
	return false
}

// numbersIn returns the numbers in a name, normalized: leading zeros dropped ("UA0123" gives 123) and an
// all-zero fraction dropped ("4.0" gives 4, while "3.10" stays distinct from "3.1").
func numbersIn(name string) map[string]bool {
	out := map[string]bool{}
	for _, run := range digitRE.FindAllString(name, -1) {
		parts := strings.Split(run, ".")
		if parts[0] = strings.TrimLeft(parts[0], "0"); parts[0] == "" {
			parts[0] = "0"
		}
		if len(parts) == 2 && strings.Trim(parts[1], "0") == "" {
			parts = parts[:1]
		}
		out[strings.Join(parts, ".")] = true
	}
	return out
}

// sequenceRatio is Python's difflib.SequenceMatcher(None, a, b).ratio() for short strings (no junk
// heuristic): 2M / (len(a) + len(b)), M the runes in the Ratcliff/Obershelp matching blocks.
func sequenceRatio(a, b []rune) float64 {
	if len(a)+len(b) == 0 {
		return 1
	}
	return 2 * float64(matchedRunes(a, b)) / float64(len(a)+len(b))
}

// matchedRunes counts the runes in the matching blocks: the longest common run (earliest in a, then in b),
// plus the matches to its left and to its right, recursively.
func matchedRunes(a, b []rune) int {
	bi, bj, bk := 0, 0, 0
	for i := range a {
		for j := range b {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > bk {
				bi, bj, bk = i, j, k
			}
		}
	}
	if bk == 0 {
		return 0
	}
	return bk + matchedRunes(a[:bi], b[:bj]) + matchedRunes(a[bi+bk:], b[bj+bk:])
}
