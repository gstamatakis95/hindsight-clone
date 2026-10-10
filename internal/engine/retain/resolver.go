package retain

import (
	"context"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"time"
	"unicode/utf8"
)

// EntityResolver maps a fact's free-form entity mentions to existing or new entities.
type EntityResolver interface {
	// Resolve scores candidates by name similarity + co-occurrence overlap + temporal proximity and
	// merges a mention into the best candidate at or above the threshold, else creates an entity. It
	// runs inside the chunk batch's transaction and makes no model call. It returns one id per distinct
	// name, in mention order (mentions first, then hints).
	Resolve(ctx context.Context, tx port.Tx, b port.BankRef, in ResolveInput) ([]port.EntityID, error)
}

// ResolveInput is the entity mentions of one fact to resolve.
type ResolveInput struct {
	// Mentions are the fact's extracted mentions.
	Mentions []Mention
	// Hints are the retain item's caller-supplied entities, merged with Mentions (deduplicated by name,
	// case-insensitively).
	Hints []domain.EntityHint
	// Literal takes the caller's hint names as given (resolve_entities=false): they match an existing entity
	// only by exact (case-insensitive) name, never fuzzily. Extracted mentions always resolve, as in
	// Hindsight, unless a hint names them too.
	Literal bool
	// At is the fact's occurrence or mention time, used for temporal proximity; zero = unknown.
	At time.Time
	// Threshold is the merge threshold (RetainTuning.EntityMatchThreshold).
	Threshold float64
}

// ResolverConfig tunes the EntityResolver.
type ResolverConfig struct {
	// CandidateLimit is how many trigram candidates are scored per mention; 0 = 10.
	CandidateLimit int
}

// Scoring weights and limits (Hindsight's entity resolver).
const (
	nameWeight         = 0.5
	cooccurrenceWeight = 0.3
	temporalWeight     = 0.2
	temporalDays       = 7.0
	maxEntityNameRune  = 512
	defaultCandidates  = 10
)

// entityResolver is the EntityResolver over EntityRepo.
type entityResolver struct {
	limit int
}

var _ EntityResolver = (*entityResolver)(nil)

// NewEntityResolver returns the EntityResolver.
func NewEntityResolver(cfg ResolverConfig) EntityResolver {
	if cfg.CandidateLimit <= 0 {
		cfg.CandidateLimit = defaultCandidates
	}
	return &entityResolver{limit: cfg.CandidateLimit}
}

// nameIn is one distinct name to resolve.
type nameIn struct {
	name, kind string
	literal    bool
}

// Resolve implements EntityResolver.
func (r *entityResolver) Resolve(
	ctx context.Context, tx port.Tx, b port.BankRef, in ResolveInput,
) ([]port.EntityID, error) {
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
	ids := make([]port.EntityID, 0, len(names))
	seen := map[port.EntityID]bool{}
	for i, n := range names {
		id, err := r.resolveOne(ctx, tx, b, n, cands[i], nearby(cands, i), in)
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

// resolveOne merges n into its best candidate or creates a new entity.
func (r *entityResolver) resolveOne(
	ctx context.Context, tx port.Tx, b port.BankRef, n nameIn, cands []domain.EntityCandidate,
	others []port.EntityID, in ResolveInput,
) (port.EntityID, error) {
	var best *domain.EntityCandidate
	bestScore := 0.0
	for i := range cands {
		c := &cands[i]
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
		if s := scoreCandidate(*c, n.name, cooc, len(others), in.At); s > bestScore {
			best, bestScore = c, s
		}
	}
	if best != nil && math.Round(bestScore*1e6)/1e6 >= in.Threshold {
		return touchEntity(ctx, tx, best.Entity, in.At)
	}
	e := domain.Entity{Bank: b, CanonicalName: n.name, Kind: n.kind}
	if !in.At.IsZero() {
		at := in.At
		e.FirstSeen, e.LastSeen = &at, &at
	}
	return tx.Entities().Upsert(ctx, e)
}

// scoreCandidate is Hindsight's score: an identical name (case-insensitive, or identical trigrams) is 1;
// otherwise 0.5 x name similarity + 0.3 x the share of the fact's other entities the candidate co-occurs with
// + 0.2 x temporal proximity (last seen within 7 days of at, linear falloff).
func scoreCandidate(c domain.EntityCandidate, name string, cooc, others int, at time.Time) float64 {
	if strings.EqualFold(c.Entity.CanonicalName, name) || c.NameSimilarity >= 1 {
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
func touchEntity(ctx context.Context, tx port.Tx, e domain.Entity, at time.Time) (port.EntityID, error) {
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

// nearby returns the best candidate of every other name: the fact's other entities as far as they already
// exist, for the co-occurrence signal.
func nearby(cands [][]domain.EntityCandidate, self int) []port.EntityID {
	var out []port.EntityID
	for i, cs := range cands {
		if i != self && len(cs) > 0 {
			out = append(out, cs[0].Entity.ID)
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
