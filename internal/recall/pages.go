package recall

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"strings"
	"unicode"
)

// Page search constants: the RRF k and the snippet length.
const (
	pageRRFK         = 60
	pageSnippetRunes = 200
)

// SearchPagesRequest mirrors memory.v1.SearchPagesRequest.
type SearchPagesRequest struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Query is the search text.
	Query string
	// FolderPrefix limits pages to a folder subtree; empty = all.
	FolderPrefix string
	// Limit caps the hits; 0 or less = domain.SearchResults' default (10).
	Limit int
}

// pageScore is one page's raw scores.
type pageScore struct {
	m        domain.MentalModel
	text     float64
	semantic float64
	fused    float64
}

// SearchPages ranks whole mental-model pages for req.Query, best first, at most req.Limit (default 10): it lists the
// folder's pages, scores each by term overlap (name weighted double) and by cosine similarity to the query
// embedding, and fuses the two rankings with RRF (k = 60), with no rerank. A bank holds tens to hundreds of pages, so
// they are scored in Go over one listing. A page that matches neither (no shared term, similarity <= 0) is not a
// hit. When the embedder fails the search ranks by terms only. An empty query is domain.ErrInvalidArgument.
func (r *Engine) SearchPages(ctx context.Context, req SearchPagesRequest) ([]domain.PageHit, error) {
	terms := pageTerms(req.Query)
	if len(terms) == 0 {
		return nil, fmt.Errorf("%w: query is empty", domain.ErrInvalidArgument)
	}
	q := store.ModelQuery{Bank: req.Bank, FolderPrefix: strings.Trim(strings.TrimSpace(req.FolderPrefix), "/")}
	models, err := r.listPages(ctx, q)
	if err != nil {
		return nil, err
	}
	var qv []float32
	if r.embedder != nil {
		if qv, err = r.embedder.EmbedQuery(ctx, req.Query); err != nil {
			qv = nil // the embedder is down: rank by terms only rather than fail the search
		}
	}
	scores := make([]pageScore, len(models))
	for i, m := range models {
		scores[i] = pageScore{m: m, text: pageTextScore(terms, m), semantic: cosine(qv, m.Embedding)}
	}
	fusePages(scores)
	limit := req.Limit
	if limit <= 0 {
		limit = domain.SearchResults.Default
	}
	var hits []domain.PageHit
	for _, sc := range scores {
		if sc.fused <= 0 || len(hits) == limit {
			continue
		}
		hits = append(hits, domain.PageHit{
			MentalModelID: sc.m.Spec.ID, FolderPath: sc.m.Spec.FolderPath, Name: sc.m.Spec.Name,
			Snippet: pageSnippet(sc.m.ContentMD, terms), Score: sc.fused,
		})
	}
	return hits, nil
}

// fusePages sets each page's RRF score over the text and semantic rankings and sorts best first.
func fusePages(scores []pageScore) {
	rank := func(key func(pageScore) float64) {
		idx := make([]int, len(scores))
		for i := range idx {
			idx[i] = i
		}
		slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(key(scores[b]), key(scores[a])) })
		for r, i := range idx {
			if key(scores[i]) > 0 {
				scores[i].fused += 1 / float64(pageRRFK+r+1)
			}
		}
	}
	rank(func(p pageScore) float64 { return p.text })
	rank(func(p pageScore) float64 { return p.semantic })
	slices.SortStableFunc(scores, func(a, b pageScore) int {
		return cmp.Or(cmp.Compare(b.fused, a.fused), cmp.Compare(a.m.Spec.ID, b.m.Spec.ID))
	})
}

// pageTextScore counts query-term occurrences in the page, name hits weighted double, damped by page length.
func pageTextScore(terms []string, m domain.MentalModel) float64 {
	name, body := pageTerms(m.Spec.Name), pageTerms(m.ContentMD)
	score := 0.0
	for _, t := range terms {
		score += 2*float64(termCount(name, t)) + float64(termCount(body, t))
	}
	if score == 0 {
		return 0
	}
	return score / math.Log(float64(len(body))+math.E)
}

// termCount is how often t occurs in toks.
func termCount(toks []string, t string) int {
	n := 0
	for _, x := range toks {
		if x == t {
			n++
		}
	}
	return n
}

// pageTerms lowercases s and splits it into letter/digit runs.
func pageTerms(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// cosine is the cosine similarity of a and b; 0 when either is empty or the lengths differ.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// pageSnippet is the first non-heading content line holding a query term (else the first non-heading line),
// cut to pageSnippetRunes runes.
func pageSnippet(content string, terms []string) string {
	first := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first == "" {
			first = line
		}
		toks := pageTerms(line)
		if slices.ContainsFunc(terms, func(t string) bool { return slices.Contains(toks, t) }) {
			return cutRunes(line)
		}
	}
	return cutRunes(first)
}

// cutRunes shortens s to pageSnippetRunes runes.
func cutRunes(s string) string {
	r := []rune(s)
	if len(r) <= pageSnippetRunes {
		return s
	}
	return string(r[:pageSnippetRunes]) + "…"
}

// listPages reads every page of q.
func (r *Engine) listPages(ctx context.Context, q store.ModelQuery) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	q.PageToken = ""
	for {
		page, next, err := r.read.ListMentalModels(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("recall: list pages: %w", err)
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		q.PageToken = next
	}
}

// ReadPages returns b's mental-model pages with the given ids, in order, skipping unknown ones, read in one short
// read-only transaction.
func (r *Engine) ReadPages(
	ctx context.Context, b domain.BankRef, ids []domain.MentalModelID,
) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	err := r.db.InTx(ctx, func(tx store.Tx) error {
		out = out[:0]
		for _, id := range ids {
			m, err := tx.MentalModels().Get(ctx, b, id)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recall: read pages: %w", err)
	}
	return out, nil
}
