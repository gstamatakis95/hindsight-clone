package recall

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// Includer attaches chunks, source facts and entity state within their own token budgets.
type Includer interface {
	// Enrich reads what inc asks for about results; each kind stays within its own budget.
	//   - chunks: the results' chunks in result order within chunks_max_tokens (default 8192); the chunk that
	//     crosses the budget is cut to the tokens left and marked Truncated, and the walk stops there.
	//   - source facts: the facts cited by returned observations, in order of first citation, within
	//     source_facts_max_tokens (default 4096); a fact that doesn't fit is skipped (later, shorter ones may
	//     still fit) and SourceFactsTruncated is set.
	//   - entities: the entity state of every entity attached to a result.
	Enrich(ctx context.Context, b port.BankRef, results []Candidate, inc domain.RecallInclude) (Enrichment, error)
}

// Enrichment is what an Includer attached to a result set.
type Enrichment struct {
	// SourceFacts are the facts behind returned observations, by id.
	SourceFacts map[port.UnitID]domain.Unit
	// SourceFactsTruncated reports that the source-fact budget cut some off.
	SourceFactsTruncated bool
	// Chunks are the source chunks of returned memories, by id.
	Chunks map[port.ChunkID]Chunk
	// Entities is the entity state of returned memories, by id.
	Entities map[port.EntityID]domain.EntityState
}

// Chunk mirrors memory.v1.Chunk: a source chunk attached to recall results.
type Chunk struct {
	// ID is the chunk id.
	ID port.ChunkID
	// Text is the chunk text, possibly cut to fit the chunk budget.
	Text string
	// Index is the chunk's position in its document, from 0.
	Index int
	// Truncated reports that Text was cut to fit the budget.
	Truncated bool
}

// includer is the Includer.
type includer struct {
	r     port.Reader
	l     Lookups
	count TokenCounter
}

var _ Includer = (*includer)(nil)

// NewIncluder returns the Includer. l may be nil: chunks and entities then attach nothing (they need the
// reads of CCR WP-09-1); source facts only need r.
func NewIncluder(r port.Reader, l Lookups, count TokenCounter) Includer {
	return &includer{r: r, l: l, count: count}
}

// Enrich implements Includer.
func (in *includer) Enrich(
	ctx context.Context, b port.BankRef, results []Candidate, inc domain.RecallInclude,
) (Enrichment, error) {
	var e Enrichment
	var err error
	if isSet(inc.Chunks) && in.l != nil {
		if e.Chunks, err = in.chunks(ctx, b, results, intOr(inc.ChunksMaxTokens, DefaultChunksMaxTokens)); err != nil {
			return Enrichment{}, err
		}
	}
	if isSet(inc.SourceFacts) {
		budget := intOr(inc.SourceFactsMaxTokens, DefaultSourceFactsMaxTokens)
		if e.SourceFacts, e.SourceFactsTruncated, err = in.sourceFacts(ctx, b, results, budget); err != nil {
			return Enrichment{}, err
		}
	}
	if isSet(inc.Entities) && in.l != nil {
		if e.Entities, err = in.entities(ctx, b, results); err != nil {
			return Enrichment{}, err
		}
	}
	return e, nil
}

// chunks reads the results' chunks and fits them into budget tokens.
func (in *includer) chunks(
	ctx context.Context, b port.BankRef, results []Candidate, budget int,
) (map[port.ChunkID]Chunk, error) {
	var ids []port.ChunkID
	seen := map[port.ChunkID]bool{}
	for _, c := range results {
		if c.Unit != nil && c.Unit.ChunkID != "" && !seen[c.Unit.ChunkID] {
			seen[c.Unit.ChunkID] = true
			ids = append(ids, c.Unit.ChunkID)
		}
	}
	out := map[port.ChunkID]Chunk{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := in.l.GetChunks(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("recall: read chunks: %w", err)
	}
	used := 0
	for _, ch := range rows {
		t := in.count.Count(ch.Text)
		if used+t <= budget {
			used += t
			out[ch.ID] = Chunk{ID: ch.ID, Text: ch.Text, Index: ch.Index}
			continue
		}
		if left := budget - used; left > 0 {
			out[ch.ID] = Chunk{ID: ch.ID, Text: truncateTokens(ch.Text, left, in.count), Index: ch.Index, Truncated: true}
		}
		break
	}
	return out, nil
}

// sourceFacts reads the facts cited by the returned observations and fits them into budget tokens.
func (in *includer) sourceFacts(
	ctx context.Context, b port.BankRef, results []Candidate, budget int,
) (map[port.UnitID]domain.Unit, bool, error) {
	var ids []port.UnitID
	seen := map[port.UnitID]bool{}
	for _, c := range results {
		if c.Unit == nil || c.Unit.Type != domain.FactTypeObservation {
			continue
		}
		for _, id := range c.Unit.SourceIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	out := map[port.UnitID]domain.Unit{}
	if len(ids) == 0 {
		return out, false, nil
	}
	units, err := in.r.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, false, err
	}
	used, truncated := 0, false
	for _, u := range units {
		t := in.count.Count(u.Text)
		if used+t > budget {
			truncated = true
			continue
		}
		used += t
		out[u.ID] = u
	}
	return out, truncated, nil
}

// entities reads the entity state of every entity attached to a result.
func (in *includer) entities(
	ctx context.Context, b port.BankRef, results []Candidate,
) (map[port.EntityID]domain.EntityState, error) {
	ids := make([]port.UnitID, 0, len(results))
	for _, c := range results {
		ids = append(ids, c.ID)
	}
	out := map[port.EntityID]domain.EntityState{}
	if len(ids) == 0 {
		return out, nil
	}
	states, err := in.l.EntityStates(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("recall: read entity state: %w", err)
	}
	for _, s := range states {
		out[s.EntityID] = s
	}
	return out, nil
}

// isSet reports whether an optional flag is set and true.
func isSet(p *bool) bool { return p != nil && *p }
