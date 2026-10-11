package recall

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// enrichment is what includer.Enrich attached to a result set.
type enrichment struct {
	// SourceFacts are the facts behind returned observations, by id.
	SourceFacts map[domain.UnitID]domain.Unit
	// SourceFactsTruncated reports that the source-fact budget cut some off.
	SourceFactsTruncated bool
	// Chunks are the source chunks of returned memories, by id.
	Chunks map[domain.ChunkID]Chunk
	// Entities is the entity state of returned memories, by id.
	Entities map[domain.EntityID]domain.EntityState
}

// Chunk mirrors memory.v1.Chunk: a source chunk attached to recall results.
type Chunk struct {
	// ID is the chunk id.
	ID domain.ChunkID
	// Text is the chunk text, possibly cut to fit the chunk budget.
	Text string
	// Index is the chunk's position in its document, from 0.
	Index int
	// Truncated reports that Text was cut to fit the budget.
	Truncated bool
}

// includer attaches chunks, source facts and entity state within their own token budgets.
type includer struct {
	r     store.Reader
	count tokenCounter
}

// newIncluder returns the includer over r (GetChunks, GetUnits and EntityStates).
func newIncluder(r store.Reader, count tokenCounter) *includer {
	return &includer{r: r, count: count}
}

// Enrich reads what inc asks for about results; each kind stays within its own budget.
//   - chunks: the results' chunks in result order within chunks_max_tokens (default 8192); the chunk that crosses
//     the budget is cut to the tokens left and marked Truncated, and the walk stops there.
//   - source facts: the facts cited by returned observations, in order of first citation (results in rank order),
//     within source_facts_max_tokens (default 4096) and seq (a fact rewritten outside the caller's snapshot is
//     left out); a fact that doesn't fit is skipped (later, shorter ones may still fit) and SourceFactsTruncated is
//     set.
//   - entities: the entity state of every entity attached to a result.
func (in *includer) Enrich(
	ctx context.Context, b domain.BankRef, seq domain.SeqRange, results []candidate, inc domain.RecallInclude,
) (enrichment, error) {
	var e enrichment
	var err error
	if isSet(inc.Chunks) {
		if e.Chunks, err = in.chunks(ctx, b, results, domain.ValueOr(inc.ChunksMaxTokens, DefaultChunksMaxTokens)); err != nil {
			return enrichment{}, err
		}
	}
	if isSet(inc.SourceFacts) {
		budget := domain.ValueOr(inc.SourceFactsMaxTokens, DefaultSourceFactsMaxTokens)
		if e.SourceFacts, e.SourceFactsTruncated, err = in.sourceFacts(ctx, b, seq, results, budget); err != nil {
			return enrichment{}, err
		}
	}
	if isSet(inc.Entities) {
		if e.Entities, err = in.entities(ctx, b, results); err != nil {
			return enrichment{}, err
		}
	}
	return e, nil
}

// chunks reads the results' chunks and fits them into budget tokens.
func (in *includer) chunks(
	ctx context.Context, b domain.BankRef, results []candidate, budget int,
) (map[domain.ChunkID]Chunk, error) {
	var ids []domain.ChunkID
	seen := map[domain.ChunkID]bool{}
	for _, c := range results {
		if c.Unit != nil && c.Unit.ChunkID != "" && !seen[c.Unit.ChunkID] {
			seen[c.Unit.ChunkID] = true
			ids = append(ids, c.Unit.ChunkID)
		}
	}
	out := map[domain.ChunkID]Chunk{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := in.r.GetChunks(ctx, b, ids)
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
	ctx context.Context, b domain.BankRef, seq domain.SeqRange, results []candidate, budget int,
) (map[domain.UnitID]domain.Unit, bool, error) {
	var ids []domain.UnitID
	seen := map[domain.UnitID]bool{}
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
	out := map[domain.UnitID]domain.Unit{}
	if len(ids) == 0 {
		return out, false, nil
	}
	units, err := in.r.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, false, err
	}
	byID := make(map[domain.UnitID]domain.Unit, len(units))
	for _, u := range units {
		if seq.Contains(u.Seq) {
			byID[u.ID] = u
		}
	}
	used, truncated := 0, false
	for _, id := range ids {
		u, ok := byID[id]
		if !ok {
			continue
		}
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
	ctx context.Context, b domain.BankRef, results []candidate,
) (map[domain.EntityID]domain.EntityState, error) {
	ids := make([]domain.UnitID, 0, len(results))
	for _, c := range results {
		ids = append(ids, c.ID)
	}
	out := map[domain.EntityID]domain.EntityState{}
	if len(ids) == 0 {
		return out, nil
	}
	states, err := in.r.EntityStates(ctx, b, ids)
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
