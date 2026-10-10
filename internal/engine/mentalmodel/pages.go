package mentalmodel

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"unicode"
)

// Page search defaults.
const (
	defaultPageLimit = 10
	rrfK             = 60
	snippetRunes     = 200
)

// PageHit is one SearchPages result; an alias of domain.PageHit.
type PageHit = domain.PageHit

// PageSearcher ranks whole pages by fused full-text and semantic scores, no rerank.
type PageSearcher interface {
	// Search returns at most req.Limit pages, best first.
	Search(ctx context.Context, req SearchPagesRequest) ([]PageHit, error)
}

// SearchPagesRequest mirrors memory.v1.SearchPagesRequest.
type SearchPagesRequest struct {
	// Bank is the bank searched.
	Bank port.BankRef
	// Query is the search text.
	Query string
	// FolderPrefix limits pages to a folder subtree; empty = all.
	FolderPrefix string
	// Limit caps the hits; 0 = the RPC default.
	Limit int
}

// pageSearcher ranks a bank's pages in memory: a bank holds tens to hundreds of pages, so v1 scores them
// in Go over one listing instead of adding page search queries to the ports (decision 1206).
type pageSearcher struct {
	models ModelLister
	emb    port.Embedder
}

var _ PageSearcher = (*pageSearcher)(nil)

// NewPageSearcher returns the PageSearcher; emb nil = full-text ranking only.
func NewPageSearcher(l ModelLister, emb port.Embedder) PageSearcher {
	return &pageSearcher{models: l, emb: emb}
}

// pageScore is one page's raw scores.
type pageScore struct {
	m        domain.MentalModel
	text     float64
	semantic float64
	fused    float64
}

// Search lists the folder's pages, scores each by term overlap (name weighted double) and by cosine
// similarity to the query embedding, and fuses the two rankings with RRF (k = 60). A page that matches
// neither (no shared term, similarity <= 0) is not a hit.
func (s *pageSearcher) Search(ctx context.Context, req SearchPagesRequest) ([]PageHit, error) {
	terms := tokenize(req.Query)
	if len(terms) == 0 {
		return nil, fmt.Errorf("%w: query is empty", domain.ErrInvalidArgument)
	}
	models, err := listAll(ctx, s.models, ModelQuery{Bank: req.Bank, FolderPrefix: normalizeFolder(req.FolderPrefix)})
	if err != nil {
		return nil, err
	}
	var qv []float32
	if s.emb != nil {
		if qv, err = s.emb.EmbedQuery(ctx, req.Query); err != nil {
			return nil, fmt.Errorf("mentalmodel: embed page query: %w", err)
		}
	}
	scores := make([]pageScore, len(models))
	for i, m := range models {
		scores[i] = pageScore{m: m, text: textScore(terms, m), semantic: cosine(qv, m.Embedding)}
	}
	fuse(scores)
	limit := req.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	var hits []PageHit
	for _, sc := range scores {
		if sc.fused <= 0 || len(hits) == limit {
			continue
		}
		hits = append(hits, PageHit{
			MentalModelID: sc.m.Spec.ID, FolderPath: sc.m.Spec.FolderPath, Name: sc.m.Spec.Name,
			Snippet: snippet(sc.m.ContentMD, terms), Score: sc.fused,
		})
	}
	return hits, nil
}

// fuse sets each page's RRF score over the text and semantic rankings and sorts best first.
func fuse(scores []pageScore) {
	rank := func(key func(pageScore) float64) {
		idx := make([]int, len(scores))
		for i := range idx {
			idx[i] = i
		}
		slices.SortStableFunc(idx, func(a, b int) int { return cmp.Compare(key(scores[b]), key(scores[a])) })
		for r, i := range idx {
			if key(scores[i]) > 0 {
				scores[i].fused += 1 / float64(rrfK+r+1)
			}
		}
	}
	rank(func(p pageScore) float64 { return p.text })
	rank(func(p pageScore) float64 { return p.semantic })
	slices.SortStableFunc(scores, func(a, b pageScore) int {
		return cmp.Or(cmp.Compare(b.fused, a.fused), cmp.Compare(a.m.Spec.ID, b.m.Spec.ID))
	})
}

// textScore counts query-term occurrences in the page, name hits weighted double, damped by page length.
func textScore(terms []string, m domain.MentalModel) float64 {
	name, body := tokenize(m.Spec.Name), tokenize(m.ContentMD)
	score := 0.0
	for _, t := range terms {
		score += 2*float64(count(name, t)) + float64(count(body, t))
	}
	if score == 0 {
		return 0
	}
	return score / math.Log(float64(len(body))+math.E)
}

// count is how often t occurs in toks.
func count(toks []string, t string) int {
	n := 0
	for _, x := range toks {
		if x == t {
			n++
		}
	}
	return n
}

// tokenize lowercases s and splits it into letter/digit runs.
func tokenize(s string) []string {
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

// snippet is the first non-heading content line holding a query term (else the first non-heading line),
// cut to snippetRunes runes.
func snippet(content string, terms []string) string {
	first := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if first == "" {
			first = line
		}
		toks := tokenize(line)
		if slices.ContainsFunc(terms, func(t string) bool { return slices.Contains(toks, t) }) {
			return cut(line)
		}
	}
	return cut(first)
}

// cut shortens s to snippetRunes runes.
func cut(s string) string {
	r := []rune(s)
	if len(r) <= snippetRunes {
		return s
	}
	return string(r[:snippetRunes]) + "…"
}
