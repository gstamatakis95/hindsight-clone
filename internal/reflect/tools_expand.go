package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
)

// expandTool is expand: the source facts behind observations and the source chunks behind facts.
type expandTool struct {
	r store.Reader
}

var _ tool = (*expandTool)(nil)

// Name implements tool.
func (*expandTool) Name() string { return ToolExpand }

// Schema implements tool.
func (*expandTool) Schema() json.RawMessage { return schemaOf(ToolExpand) }

// expandItem is one expand result.
type expandItem struct {
	MemoryID    domain.UnitID  `json:"memory_id"`
	Memory      map[string]any `json:"memory,omitempty"`
	SourceFacts []memoryOut    `json:"source_facts,omitempty"`
	Chunk       map[string]any `json:"chunk,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// Call implements tool. Expand only reads ids; it registers nothing as retrieved (as in Hindsight), so its
// output can't be cited unless an earlier search returned the unit.
func (t *expandTool) Call(ctx context.Context, st *agentState, args json.RawMessage) (toolResult, error) {
	var a struct {
		IDs json.RawMessage `json:"memory_ids"`
	}
	if res, ok := decodeArgs(ToolExpand, args, &a); !ok {
		return res, nil
	}
	raw, ok := idList(a.IDs)
	if !ok || len(raw) == 0 {
		return malformed("expand requires memory_ids"), nil
	}
	ids := make([]domain.UnitID, 0, len(raw))
	for _, id := range raw {
		ids = append(ids, domain.UnitID(id))
	}
	units, err := t.visibleUnits(ctx, st, ids)
	if err != nil {
		return retrievalError(ctx, ToolExpand, err)
	}
	sources, chunks, err := t.context(ctx, st, units)
	if err != nil {
		return retrievalError(ctx, ToolExpand, err)
	}
	items := make([]expandItem, 0, len(ids))
	for _, id := range ids {
		u, ok := units[id]
		if !ok {
			items = append(items, expandItem{MemoryID: id, Error: fmt.Sprintf("Memory not found: %s", id)})
			continue
		}
		it := expandItem{MemoryID: id, Memory: map[string]any{
			"id": u.ID, "text": u.Text, "type": u.Type, "context": u.Context,
		}}
		for _, sid := range u.SourceIDs {
			if s, ok := sources[sid]; ok {
				it.SourceFacts = append(it.SourceFacts, toMemoryOut(s))
			}
		}
		if c, ok := chunks[u.ChunkID]; ok {
			it.Chunk = map[string]any{"id": c.ID, "text": c.Text, "index": c.Index, "document_id": c.DocumentID}
		}
		items = append(items, it)
	}
	return output(map[string]any{"results": items, "count": len(items)})
}

// visibleUnits reads units and keeps those inside the request's fact types, tag filter and seq range, by id.
func (t *expandTool) visibleUnits(
	ctx context.Context, st *agentState, ids []domain.UnitID,
) (map[domain.UnitID]domain.Unit, error) {
	us, err := t.r.GetUnits(ctx, st.Request.Bank, ids)
	if err != nil {
		return nil, fmt.Errorf("get units: %w", err)
	}
	seq := st.Request.Seq
	unbounded := seq.After == 0 && seq.UpTo == nil
	out := make(map[domain.UnitID]domain.Unit, len(us))
	for _, u := range us {
		typed := len(st.Request.FactTypes) == 0 || slices.Contains(st.Request.FactTypes, u.Type)
		if typed && domain.MatchFilter(u.Tags, st.Request.Tags) && (unbounded || seq.Contains(u.Seq)) {
			out[u.ID] = u
		}
	}
	return out, nil
}

// context reads the observations' source facts and the facts' chunks (Reader.GetChunks).
func (t *expandTool) context(
	ctx context.Context, st *agentState, units map[domain.UnitID]domain.Unit,
) (map[domain.UnitID]domain.Unit, map[domain.ChunkID]domain.Chunk, error) {
	var sourceIDs []domain.UnitID
	var chunkIDs []domain.ChunkID
	for _, u := range units {
		sourceIDs = append(sourceIDs, u.SourceIDs...)
		if u.ChunkID != "" {
			chunkIDs = append(chunkIDs, u.ChunkID)
		}
	}
	sources := map[domain.UnitID]domain.Unit{}
	if len(sourceIDs) > 0 {
		var err error
		if sources, err = t.visibleUnits(ctx, st, sourceIDs); err != nil {
			return nil, nil, err
		}
	}
	chunks := map[domain.ChunkID]domain.Chunk{}
	if len(chunkIDs) > 0 {
		cs, err := t.r.GetChunks(ctx, st.Request.Bank, chunkIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("get chunks: %w", err)
		}
		for _, c := range cs {
			chunks[c.ID] = c
		}
	}
	return sources, chunks, nil
}
