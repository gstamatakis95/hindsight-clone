package consolidate

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"memd/internal/prompts"
	"strings"
)

// relatedPerFact is how many existing observations each new fact pulls into the prompt (per arm, then
// interleaved and capped again).
const relatedPerFact = 10

// maxSourcesPerObservation bounds the source facts read per shown observation.
const maxSourcesPerObservation = 20

// decision is what one CONSOLIDATE call decided for a batch, ready to apply.
type decision struct {
	actions []ConsolidationAction
	usage   []port.Usage
}

// consolidateResponse is the CONSOLIDATE response (prompts consolidate_system.schema.json).
type consolidateResponse struct {
	Creates []struct {
		Text          string   `json:"text"`
		SourceFactIDs []string `json:"source_fact_ids"`
	} `json:"creates"`
	Updates []struct {
		ObservationID string   `json:"observation_id"`
		Text          string   `json:"text"`
		SourceFactIDs []string `json:"source_fact_ids"`
	} `json:"updates"`
}

// decide runs everything slow for one batch with no transaction open: the related-observation lookup,
// the CONSOLIDATE call (a failure is a *batchError), embeddings of the new texts and the dedup checks.
func (c *consolidator) decide(
	ctx context.Context, s port.ScopeKey, cfg domain.BankConfig, facts []domain.Unit,
) (decision, error) {
	shown, err := c.related(ctx, s, facts)
	if err != nil {
		return decision{}, err
	}
	remaining, err := c.capacity(ctx, s, cfg)
	if err != nil {
		return decision{}, err
	}
	req, err := c.consolidateRequest(ctx, s, cfg, facts, shown, remaining)
	if err != nil {
		return decision{}, err
	}
	var out consolidateResponse
	usage, err := c.d.LLM.Structured(ctx, req, &out)
	dec := decision{usage: []port.Usage{usage}}
	if err != nil {
		return dec, &batchError{err: err}
	}
	dec.actions = parseActions(out, facts, shown, remaining)
	if err := c.embed(ctx, dec.actions); err != nil {
		return dec, err
	}
	dedupUsage, err := c.dedup(ctx, s, cfg, dec.actions)
	dec.usage = append(dec.usage, dedupUsage...)
	if err != nil {
		return dec, err
	}
	dec.actions = mergeUpdates(dec.actions)
	return dec, c.embed(ctx, dec.actions)
}

// related returns the scope's existing observations near the batch's facts: per fact, the semantic and
// keyword arms over observations of exactly this scope, interleaved (no rerank, as in Hindsight), then the
// union over the batch in first-seen order.
func (c *consolidator) related(ctx context.Context, s port.ScopeKey, facts []domain.Unit) ([]domain.Unit, error) {
	read := c.d.Store.Read()
	types := []domain.FactType{domain.FactTypeObservation}
	var ids []port.UnitID
	seen := map[port.UnitID]bool{}
	for _, f := range facts {
		var vec []port.Hit
		if len(f.Embedding) == domain.EmbeddingDim {
			hits, err := read.SearchVector(ctx, port.VectorQuery{
				Bank: s.BankRef, Embedding: f.Embedding, Types: types, Tags: scopePredicate(s), Limit: relatedPerFact,
			})
			if err != nil {
				return nil, fmt.Errorf("consolidate: related observations: %w", err)
			}
			vec = hits
		}
		kw, err := read.SearchText(ctx, port.TextQuery{
			Bank: s.BankRef, Text: f.Text, Types: types, Tags: scopePredicate(s), Limit: relatedPerFact,
		})
		if err != nil {
			return nil, fmt.Errorf("consolidate: related observations: %w", err)
		}
		for _, id := range interleave(vec, kw, relatedPerFact) {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	obs, err := read.GetUnits(ctx, s.BankRef, ids)
	if err != nil {
		return nil, fmt.Errorf("consolidate: read related observations: %w", err)
	}
	return obs, nil
}

// interleave alternates the two hit lists (deduplicated) up to limit ids.
func interleave(a, b []port.Hit, limit int) []port.UnitID {
	var out []port.UnitID
	seen := map[port.UnitID]bool{}
	for i := 0; len(out) < limit && (i < len(a) || i < len(b)); i++ {
		for _, hs := range [][]port.Hit{a, b} {
			if i < len(hs) && !seen[hs[i].ID] && len(out) < limit {
				seen[hs[i].ID] = true
				out = append(out, hs[i].ID)
			}
		}
	}
	return out
}

// capacity returns how many observations scope s may still create, or nil when uncapped.
func (c *consolidator) capacity(ctx context.Context, s port.ScopeKey, cfg domain.BankConfig) (*int, error) {
	if cfg.MaxObservationsPerScope == nil || *cfg.MaxObservationsPerScope < 0 {
		return nil, nil
	}
	limit := *cfg.MaxObservationsPerScope
	if limit == 0 {
		return &limit, nil
	}
	if c.d.Counter == nil {
		return nil, fmt.Errorf("%w: consolidate: max_observations_per_scope needs an ObservationCounter",
			domain.ErrFailedPrecondition)
	}
	n, err := c.d.Counter.CountObservations(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("consolidate: count observations: %w", err)
	}
	left := max(limit-n, 0)
	return &left, nil
}

// consolidateRequest renders the CONSOLIDATE call with the per-call schema.
func (c *consolidator) consolidateRequest(
	ctx context.Context, s port.ScopeKey, cfg domain.BankConfig, facts, shown []domain.Unit, remaining *int,
) (port.StructuredRequest, error) {
	observations, err := c.promptObservations(ctx, s, cfg, shown)
	if err != nil {
		return port.StructuredRequest{}, err
	}
	data := prompts.ConsolidateData{Mission: cfg.ObservationsMission, Observations: observations}
	if remaining != nil {
		data.Capacity = &prompts.ConsolidateCapacity{Max: *cfg.MaxObservationsPerScope, Remaining: *remaining}
	}
	ids := make([]string, 0, len(facts))
	for _, f := range facts {
		ids = append(ids, string(f.ID))
		data.Facts = append(data.Facts, prompts.SourceFact{
			ID: string(f.ID), Type: string(f.Type), Text: f.Text, Context: f.Context,
			OccurredStart: f.OccurredStart, OccurredEnd: f.OccurredEnd, MentionedAt: f.MentionedAt,
		})
	}
	binding, err := c.d.Router.Resolve(cfg, domain.ModelSlotConsolidate)
	if err != nil {
		return port.StructuredRequest{}, fmt.Errorf("consolidate: binding: %w", err)
	}
	system, err := prompts.Render(prompts.ConsolidateSystem, data)
	if err != nil {
		return port.StructuredRequest{}, fmt.Errorf("consolidate: render system: %w", err)
	}
	user, err := prompts.Render(prompts.ConsolidateUser, data)
	if err != nil {
		return port.StructuredRequest{}, fmt.Errorf("consolidate: render user: %w", err)
	}
	schema, err := consolidateSchema(ids, remaining)
	if err != nil {
		return port.StructuredRequest{}, err
	}
	return port.StructuredRequest{
		Slot: domain.ModelSlotConsolidate, Binding: binding, Name: prompts.ConsolidateSystem,
		System: system, User: user, Schema: schema,
	}, nil
}

// promptObservations renders the shown observations with some of their source facts, within the
// per-observation token cap (default 256) and the call's source-fact cap (unset = none).
func (c *consolidator) promptObservations(
	ctx context.Context, s port.ScopeKey, cfg domain.BankConfig, shown []domain.Unit,
) ([]prompts.ConsolidateObservation, error) {
	var srcIDs []port.UnitID
	for _, o := range shown {
		srcIDs = append(srcIDs, o.SourceIDs[:min(len(o.SourceIDs), maxSourcesPerObservation)]...)
	}
	sources := map[port.UnitID]domain.Unit{}
	if len(srcIDs) > 0 {
		us, err := c.d.Store.Read().GetUnits(ctx, s.BankRef, srcIDs)
		if err != nil {
			return nil, fmt.Errorf("consolidate: read source facts: %w", err)
		}
		for _, u := range us {
			sources[u.ID] = u
		}
	}
	perObs := orDefault(cfg.ConsolidationSourceTokensPerObs, defaultSourceTokensPerO)
	budget := -1
	if cfg.ConsolidationSourceFactsMaxTokens != nil {
		budget = *cfg.ConsolidationSourceFactsMaxTokens
	}
	out := make([]prompts.ConsolidateObservation, 0, len(shown))
	for _, o := range shown {
		po := prompts.ConsolidateObservation{
			ID: string(o.ID), Text: o.Text, ProofCount: max(o.ProofCount, 1),
			OccurredStart: o.OccurredStart, OccurredEnd: o.OccurredEnd, MentionedAt: o.MentionedAt,
		}
		used := 0
		for _, id := range o.SourceIDs {
			src, ok := sources[id]
			if !ok {
				continue
			}
			n := c.d.Tokens.Count(src.Text)
			if used+n > perObs || (budget >= 0 && n > budget) {
				break
			}
			used += n
			if budget >= 0 {
				budget -= n
			}
			po.SourceMemories = append(po.SourceMemories, prompts.SourceMemory{
				Text: src.Text, Context: src.Context,
				OccurredStart: src.OccurredStart, OccurredEnd: src.OccurredEnd, MentionedAt: src.MentionedAt,
			})
		}
		out = append(out, po)
	}
	return out, nil
}

// consolidateSchema returns the CONSOLIDATE schema with this call's constraints injected, as Hindsight's
// per-batch response model does: source_fact_ids items limited to the batch's fact ids with minItems 1
// (creates and updates), and creates.maxItems = the remaining observation capacity when capped.
func consolidateSchema(ids []string, remaining *int) (json.RawMessage, error) {
	raw, err := prompts.Schema(prompts.ConsolidateSystem)
	if err != nil {
		return nil, fmt.Errorf("consolidate: schema: %w", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("consolidate: schema: %w", err)
	}
	enum := make([]any, len(ids))
	for i, id := range ids {
		enum[i] = id
	}
	for _, list := range []string{"creates", "updates"} {
		arr, ok1 := dig(root, "properties", list)
		src, ok2 := dig(arr, "items", "properties", "source_fact_ids")
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("consolidate: schema has no %s.items.properties.source_fact_ids", list)
		}
		src["items"] = map[string]any{"type": "string", "enum": enum}
		src["minItems"] = 1
		if list == "creates" && remaining != nil {
			arr["maxItems"] = *remaining
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("consolidate: schema: %w", err)
	}
	return out, nil
}

// dig walks nested JSON objects by key.
func dig(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil, false
		}
		m = next
	}
	return m, true
}

// parseActions turns the response into actions: source ids limited to the batch (deduplicated, each
// cited with its exact text), updates only of shown observations, empty or source-less actions dropped,
// a create repeating a shown observation's text turned into an update of it, and creates capped at the
// remaining capacity.
func parseActions(out consolidateResponse, facts, shown []domain.Unit, remaining *int) []ConsolidationAction {
	byID := map[string]domain.Unit{}
	for _, f := range facts {
		byID[string(f.ID)] = f
	}
	shownByID, shownByText := map[string]bool{}, map[string]port.UnitID{}
	for _, o := range shown {
		shownByID[string(o.ID)] = true
		shownByText[normText(o.Text)] = o.ID
	}
	var actions []ConsolidationAction
	for _, u := range out.Updates {
		ev := evidence(u.SourceFactIDs, byID)
		text := strings.TrimSpace(u.Text)
		if text == "" || len(ev) == 0 || !shownByID[u.ObservationID] {
			continue
		}
		actions = append(actions, ConsolidationAction{
			Kind: ActionUpdate, ObservationID: port.UnitID(u.ObservationID), Text: text, Evidence: ev,
		})
	}
	creates := 0
	for _, cr := range out.Creates {
		ev := evidence(cr.SourceFactIDs, byID)
		text := strings.TrimSpace(cr.Text)
		if text == "" || len(ev) == 0 {
			continue
		}
		if twin, ok := shownByText[normText(text)]; ok {
			actions = append(actions, ConsolidationAction{Kind: ActionUpdate, ObservationID: twin, Text: text, Evidence: ev})
			continue
		}
		if remaining != nil && creates >= *remaining {
			continue
		}
		creates++
		actions = append(actions, ConsolidationAction{Kind: ActionCreate, Text: text, Evidence: ev})
	}
	return actions
}

// evidence cites each distinct batch fact among ids with its exact text.
func evidence(ids []string, facts map[string]domain.Unit) []domain.Evidence {
	var out []domain.Evidence
	seen := map[string]bool{}
	for _, id := range ids {
		f, ok := facts[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, domain.Evidence{Unit: f.ID, Quote: f.Text})
	}
	return out
}

// normText collapses whitespace (case kept) for exact-duplicate matching.
func normText(s string) string { return strings.Join(strings.Fields(s), " ") }
