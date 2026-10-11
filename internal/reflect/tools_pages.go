package reflect

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/recall"
	"slices"
	"time"
)

// pageOut is one mental-model page as the page tools show it to the model.
type pageOut struct {
	ID           domain.MentalModelID `json:"id"`
	Name         string               `json:"name"`
	Content      string               `json:"content,omitempty"`
	Snippet      string               `json:"snippet,omitempty"`
	ContentChars int                  `json:"content_chars,omitempty"`
	Tags         []string             `json:"tags,omitempty"`
	Relevance    *float64             `json:"relevance,omitempty"`
	UpdatedAt    *time.Time           `json:"updated_at,omitempty"`
	Stale        bool                 `json:"stale"`
}

// visiblePages reads pages by id and keeps those the request may see: not excluded and inside its tag filter.
func visiblePages(
	ctx context.Context, pages *recall.Engine, st *agentState, ids []domain.MentalModelID,
) ([]domain.MentalModel, error) {
	req := st.Request
	if req.ExcludeMentalModels {
		return nil, nil
	}
	ids = slices.DeleteFunc(slices.Clone(ids), func(id domain.MentalModelID) bool {
		return slices.Contains(req.ExcludeMentalModelIDs, id)
	})
	if len(ids) == 0 {
		return nil, nil
	}
	ms, err := pages.ReadPages(ctx, req.Bank, ids)
	if err != nil {
		return nil, fmt.Errorf("read pages: %w", err)
	}
	return slices.DeleteFunc(ms, func(m domain.MentalModel) bool {
		return !domain.MatchFilter(m.Spec.Tags, req.Tags)
	}), nil
}

// rememberPages records retrieved pages in the agent state.
func rememberPages(st *agentState, ms []domain.MentalModel) {
	if st.RetrievedModels == nil {
		st.RetrievedModels = map[domain.MentalModelID]struct{}{}
	}
	for _, m := range ms {
		st.RetrievedModels[m.Spec.ID] = struct{}{}
	}
}

// fullPage returns a page with its whole content.
func fullPage(m domain.MentalModel) pageOut {
	p := pageOut{ID: m.Spec.ID, Name: m.Spec.Name, Content: m.ContentMD, Tags: m.Spec.Tags, Stale: m.Stale}
	if !m.UpdatedAt.IsZero() {
		updated := m.UpdatedAt
		p.UpdatedAt = &updated
	}
	return p
}

// searchPagesTool is search_mental_models: ranked pages, the best one in full and snippets of the rest.
type searchPagesTool struct{ pages *recall.Engine }

var _ tool = (*searchPagesTool)(nil)

// Name implements tool.
func (*searchPagesTool) Name() string { return ToolSearchMentalModels }

// Schema implements tool.
func (*searchPagesTool) Schema() json.RawMessage { return schemaOf(ToolSearchMentalModels) }

// Call implements tool.
func (t *searchPagesTool) Call(ctx context.Context, st *agentState, args json.RawMessage) (toolResult, error) {
	var a queryArgs
	if res, ok := decodeArgs(ToolSearchMentalModels, args, &a); !ok {
		return res, nil
	}
	if a.Query == "" {
		return malformed("search_mental_models requires a query parameter"), nil
	}
	limit := defaultPageResults
	if a.MaxResults != nil && *a.MaxResults >= 1 {
		limit = int(*a.MaxResults)
	}
	hits, err := t.pages.SearchMentalModels(ctx, recall.SearchMentalModelsRequest{
		Bank: st.Request.Bank, Query: a.Query,
		MaxResults: limit,
	})
	if err != nil {
		return retrievalError(ctx, ToolSearchMentalModels, err)
	}
	ids := make([]domain.MentalModelID, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.MentalModelID)
	}
	ms, err := visiblePages(ctx, t.pages, st, ids)
	if err != nil {
		return retrievalError(ctx, ToolSearchMentalModels, err)
	}
	st.Searched = true
	rememberPages(st, ms)
	out := make([]pageOut, 0, len(ms))
	for i, m := range ms {
		p := fullPage(m)
		if hit, ok := findHit(hits, m.Spec.ID); ok {
			score := hit.Score
			p.Relevance = &score
			if i > 0 || estimateTokens(m.ContentMD) > maxFullPageTokens {
				p.Content, p.Snippet, p.ContentChars = "", snippet(hit.Snippet, m.ContentMD), len(m.ContentMD)
			}
		}
		out = append(out, p)
	}
	return output(map[string]any{"query": a.Query, "count": len(out), "mental_models": out})
}

// findHit returns the search hit for a page.
func findHit(hits []domain.MentalModelHit, id domain.MentalModelID) (domain.MentalModelHit, bool) {
	for _, h := range hits {
		if h.MentalModelID == id {
			return h, true
		}
	}
	return domain.MentalModelHit{}, false
}

// snippet returns the search snippet, else the content's first 280 characters.
func snippet(s, content string) string {
	if s != "" {
		return s
	}
	r := []rune(content)
	if len(r) > snippetChars {
		return string(r[:snippetChars]) + "..."
	}
	return content
}

// idList decodes an ids argument given as an array of strings or a single string.
func idList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err == nil {
		return ids, true
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, true
	}
	return nil, false
}

// readPagesTool is read_mental_models: whole pages by id within a token budget.
type readPagesTool struct{ pages *recall.Engine }

var _ tool = (*readPagesTool)(nil)

// Name implements tool.
func (*readPagesTool) Name() string { return ToolReadMentalModels }

// Schema implements tool.
func (*readPagesTool) Schema() json.RawMessage { return schemaOf(ToolReadMentalModels) }

// Call implements tool.
func (t *readPagesTool) Call(ctx context.Context, st *agentState, args json.RawMessage) (toolResult, error) {
	var a struct {
		IDs       json.RawMessage `json:"mental_model_ids"`
		MaxTokens *float64        `json:"max_tokens"`
	}
	if res, ok := decodeArgs(ToolReadMentalModels, args, &a); !ok {
		return res, nil
	}
	raw, ok := idList(a.IDs)
	if !ok || len(raw) == 0 {
		return malformed("read_mental_models requires mental_model_ids"), nil
	}
	ids := make([]domain.MentalModelID, 0, len(raw))
	for _, id := range raw {
		ids = append(ids, domain.MentalModelID(id))
	}
	ms, err := visiblePages(ctx, t.pages, st, ids)
	if err != nil {
		return retrievalError(ctx, ToolReadMentalModels, err)
	}
	budget := tokenArg(a.MaxTokens, defaultReadPagesMaxTokens)
	var read []domain.MentalModel
	var notRead []domain.MentalModelID
	used := 0
	for _, id := range ids {
		i := slices.IndexFunc(ms, func(m domain.MentalModel) bool { return m.Spec.ID == id })
		if i < 0 || (len(read) > 0 && used+estimateTokens(ms[i].ContentMD) > budget) {
			notRead = append(notRead, id)
			continue
		}
		used += estimateTokens(ms[i].ContentMD)
		read = append(read, ms[i])
	}
	rememberPages(st, read)
	st.Searched = st.Searched || len(read) > 0
	out := make([]pageOut, 0, len(read))
	for _, m := range read {
		out = append(out, fullPage(m))
	}
	res := map[string]any{"mental_models": out}
	if len(notRead) > 0 {
		res["not_read"] = notRead
	}
	return output(res)
}
