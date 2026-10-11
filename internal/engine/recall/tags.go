package recall

import (
	"cmp"
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"strings"
)

// TagFilterCompiler turns flat tags or tag groups into one SQL predicate shared by all four arms.
type TagFilterCompiler interface {
	// Compile validates f, resolves fuzzy leaves against the bank's tags by trigram similarity (at or above
	// minSimilarity) and compiles the result. Leaves always carry normalized tags and an explicit match
	// mode. An empty filter compiles to an empty PredicateAnd, which matches every unit.
	Compile(ctx context.Context, b domain.BankRef, f domain.TagFilter, minSimilarity float64) (store.SQLPredicate, error)
}

// maxExactCombinations caps the branches a fuzzy exact leaf may expand into (Hindsight's
// MAX_EXACT_COMBINATIONS). Exceeding it is an error rather than a silent truncation.
const maxExactCombinations = 32

// tagCompiler is the TagFilterCompiler; vocab may be nil, in which case fuzzy leaves fail.
type tagCompiler struct {
	vocab TagVocabulary
}

var _ TagFilterCompiler = (*tagCompiler)(nil)

// NewTagFilterCompiler returns a TagFilterCompiler that reads the bank's tags from v for fuzzy leaves.
// v may be nil: filters without fuzzy leaves still compile, fuzzy ones fail with domain.ErrFailedPrecondition.
func NewTagFilterCompiler(v TagVocabulary) TagFilterCompiler {
	return &tagCompiler{vocab: v}
}

// Compile implements TagFilterCompiler. The flat leaf (if it filters anything) and every top-level group
// are AND-ed under one PredicateAnd.
func (c *tagCompiler) Compile(
	ctx context.Context, b domain.BankRef, f domain.TagFilter, minSimilarity float64,
) (store.SQLPredicate, error) {
	if err := f.Validate(); err != nil {
		return store.SQLPredicate{}, fmt.Errorf("recall: tag filter: %w", err)
	}
	out := store.SQLPredicate{Op: store.PredicateAnd}
	if leaf, ok := flatLeaf(f); ok {
		out.Args = append(out.Args, leaf)
	}
	if len(f.Groups) == 0 {
		return out, nil
	}
	resolved, err := c.resolveFuzzy(ctx, b, f.Groups, minSimilarity)
	if err != nil {
		return store.SQLPredicate{}, err
	}
	for _, g := range f.Groups {
		p, err := compileGroup(g, resolved)
		if err != nil {
			return store.SQLPredicate{}, err
		}
		out.Args = append(out.Args, p)
	}
	return out, nil
}

// flatLeaf compiles f's flat tags into one leaf, defaulting an unspecified match to any. It reports false
// when the flat part filters nothing (no tags under a non-exact mode).
func flatLeaf(f domain.TagFilter) (store.SQLPredicate, bool) {
	mode := f.Match
	if mode == domain.TagsMatchUnspecified {
		mode = domain.TagsMatchAny
	}
	tags := domain.NormalizeTags(f.Tags)
	if len(tags) == 0 && mode != domain.TagsMatchExact {
		return store.SQLPredicate{}, false
	}
	return tagsLeaf(tags, mode), true
}

// tagsLeaf builds a PredicateTags leaf over already-normalized tags.
func tagsLeaf(tags []string, mode domain.TagsMatch) store.SQLPredicate {
	return store.SQLPredicate{Op: store.PredicateTags, Tags: tags, Match: mode}
}

// resolveFuzzy expands every fuzzy token in groups against the bank's tag vocabulary, keyed by the
// normalized token. It reads the vocabulary only when at least one leaf is fuzzy and returns nil otherwise.
func (c *tagCompiler) resolveFuzzy(
	ctx context.Context, b domain.BankRef, groups []domain.TagGroup, minSimilarity float64,
) (map[string][]string, error) {
	var tokens []string
	for _, g := range groups {
		tokens = appendFuzzyTokens(tokens, g)
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	if c.vocab == nil {
		return nil, fmt.Errorf("%w: recall: fuzzy tag resolution needs a tag vocabulary", domain.ErrFailedPrecondition)
	}
	raw, err := bankTags(ctx, c.vocab, b)
	if err != nil {
		return nil, fmt.Errorf("recall: read bank tags: %w", err)
	}
	vocab := normalizeOrdered(raw)
	resolved := make(map[string][]string, len(tokens))
	for _, t := range tokens {
		if _, ok := resolved[t]; !ok {
			resolved[t] = expandToken(t, vocab, minSimilarity)
		}
	}
	return resolved, nil
}

// appendFuzzyTokens appends the normalized tokens of every fuzzy leaf under g to dst.
func appendFuzzyTokens(dst []string, g domain.TagGroup) []string {
	if g.Leaf != nil && g.Leaf.Resolve == domain.TagResolveFuzzy {
		dst = append(dst, fuzzyTokens(g.Leaf.Tags)...)
	}
	for _, c := range g.Groups {
		dst = appendFuzzyTokens(dst, c)
	}
	if g.Not != nil {
		dst = appendFuzzyTokens(dst, *g.Not)
	}
	return dst
}

// fuzzyTokens returns a fuzzy leaf's tokens: normalized, empty ones dropped, deduplicated in order.
func fuzzyTokens(tags []string) []string {
	return normalizeOrdered(tags)
}

// normalizeOrdered normalizes tags with domain.NormalizeTag, dropping empty and repeated ones but keeping
// the first-seen order (vocabulary order carries usage rank).
func normalizeOrdered(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		if n := domain.NormalizeTag(t); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// compileGroup compiles one tag group. Exact leaves compile verbatim (normalized tags, unset match =
// any_strict); fuzzy leaves are rewritten over their expansions in resolved, keyed by normalized token.
func compileGroup(g domain.TagGroup, resolved map[string][]string) (store.SQLPredicate, error) {
	switch g.Kind {
	case domain.TagGroupLeaf:
		if g.Leaf == nil {
			break
		}
		return compileLeaf(*g.Leaf, resolved)
	case domain.TagGroupAllOf, domain.TagGroupAnyOf:
		op := store.PredicateAnd
		if g.Kind == domain.TagGroupAnyOf {
			op = store.PredicateOr
		}
		out := store.SQLPredicate{Op: op, Args: make([]store.SQLPredicate, 0, len(g.Groups))}
		for _, c := range g.Groups {
			p, err := compileGroup(c, resolved)
			if err != nil {
				return store.SQLPredicate{}, err
			}
			out.Args = append(out.Args, p)
		}
		return out, nil
	case domain.TagGroupNoneOf:
		if g.Not == nil {
			break
		}
		p, err := compileGroup(*g.Not, resolved)
		if err != nil {
			return store.SQLPredicate{}, err
		}
		return store.SQLPredicate{Op: store.PredicateNot, Args: []store.SQLPredicate{p}}, nil
	}
	return store.SQLPredicate{}, fmt.Errorf("%w: malformed tag group %q", domain.ErrInvalidArgument, g.Kind)
}

// compileLeaf compiles one leaf; a fuzzy leaf with tokens is rewritten per Hindsight's _resolve_leaf.
func compileLeaf(l domain.TagLeaf, resolved map[string][]string) (store.SQLPredicate, error) {
	mode := l.Match
	if mode == domain.TagsMatchUnspecified {
		mode = domain.TagsMatchAnyStrict
	}
	tokens := fuzzyTokens(l.Tags)
	if l.Resolve != domain.TagResolveFuzzy || len(tokens) == 0 {
		return tagsLeaf(domain.NormalizeTags(l.Tags), mode), nil
	}
	expanded := make([][]string, len(tokens))
	for i, t := range tokens {
		if e, ok := resolved[t]; ok && len(e) > 0 {
			expanded[i] = e
		} else {
			expanded[i] = []string{t}
		}
	}
	switch mode {
	case domain.TagsMatchAll, domain.TagsMatchAllStrict:
		return fuzzyAll(expanded, mode), nil
	case domain.TagsMatchExact:
		return fuzzyExact(tokens, expanded)
	default:
		return fuzzyAny(expanded, mode), nil
	}
}

// fuzzyAny rewrites an any / any_strict fuzzy leaf: one leaf over the union of every token's expansions.
func fuzzyAny(expanded [][]string, mode domain.TagsMatch) store.SQLPredicate {
	var union []string
	for _, e := range expanded {
		union = append(union, e...)
	}
	return tagsLeaf(domain.NormalizeTags(union), mode)
}

// fuzzyAll rewrites an all / all_strict fuzzy leaf: an AND of one any (all) or any_strict (all_strict) leaf
// per token, so each token needs some spelling present. A single token is just its leaf.
func fuzzyAll(expanded [][]string, mode domain.TagsMatch) store.SQLPredicate {
	inner := domain.TagsMatchAny
	if mode == domain.TagsMatchAllStrict {
		inner = domain.TagsMatchAnyStrict
	}
	if len(expanded) == 1 {
		return tagsLeaf(domain.NormalizeTags(expanded[0]), inner)
	}
	out := store.SQLPredicate{Op: store.PredicateAnd, Args: make([]store.SQLPredicate, 0, len(expanded))}
	for _, e := range expanded {
		out.Args = append(out.Args, tagsLeaf(domain.NormalizeTags(e), inner))
	}
	return out
}

// fuzzyExact rewrites an exact fuzzy leaf: an OR over the cross product of the expansions, one exact leaf
// per combination of distinct tags. More than maxExactCombinations candidate combinations is
// domain.ErrInvalidArgument; when every combination is degenerate the leaf stays exact over the tokens.
func fuzzyExact(tokens []string, expanded [][]string) (store.SQLPredicate, error) {
	n := 1
	for _, e := range expanded {
		n *= len(e)
		if n > maxExactCombinations {
			return store.SQLPredicate{}, fmt.Errorf(
				"%w: fuzzy resolution of an exact tag leaf expands to more than %d candidate scopes; "+
					"narrow the tags or resolve them exactly", domain.ErrInvalidArgument, maxExactCombinations)
		}
	}
	var combos [][]string
	seen := map[string]bool{}
	for _, combo := range crossProduct(expanded) {
		set := domain.NormalizeTags(combo)
		if len(set) != len(combo) {
			continue // two tokens landed on the same tag
		}
		if key := strings.Join(set, "\x00"); !seen[key] {
			seen[key] = true
			combos = append(combos, set)
		}
	}
	switch len(combos) {
	case 0:
		return tagsLeaf(domain.NormalizeTags(tokens), domain.TagsMatchExact), nil
	case 1:
		return tagsLeaf(combos[0], domain.TagsMatchExact), nil
	}
	out := store.SQLPredicate{Op: store.PredicateOr, Args: make([]store.SQLPredicate, 0, len(combos))}
	for _, c := range combos {
		out.Args = append(out.Args, tagsLeaf(c, domain.TagsMatchExact))
	}
	return out, nil
}

// crossProduct returns every combination picking one element from each list, in lexicographic order.
func crossProduct(lists [][]string) [][]string {
	out := [][]string{{}}
	for _, l := range lists {
		next := make([][]string, 0, len(out)*len(l))
		for _, prefix := range out {
			for _, v := range l {
				next = append(next, append(slices.Clip(prefix), v))
			}
		}
		out = next
	}
	return out
}

// expandToken returns the tags a fuzzy token should match given the bank's normalized vocabulary
// (Hindsight's expand_token). An exact hit wins outright. Otherwise only vocabulary tags in the token's
// namespace are compared, by trigram similarity of the value parts; those at or above minSimilarity are
// returned most similar first (ties keep vocabulary order). No match returns the token itself, which keeps
// the leaf unsatisfiable rather than empty.
func expandToken(token string, vocab []string, minSimilarity float64) []string {
	t := domain.NormalizeTag(token)
	if slices.Contains(vocab, t) {
		return []string{t}
	}
	ns, val := splitNamespace(t)
	type scored struct {
		tag string
		sim float64
	}
	var hits []scored
	for _, v := range vocab {
		vns, vval := splitNamespace(v)
		if vns != ns {
			continue
		}
		if s := trigramSimilarity(val, vval); s >= minSimilarity {
			hits = append(hits, scored{tag: v, sim: s})
		}
	}
	if len(hits) == 0 {
		return []string{t}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return cmp.Compare(b.sim, a.sim) })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.tag
	}
	return out
}

// splitNamespace splits a tag on its first ':' into (namespace, value); a tag without one has namespace "".
func splitNamespace(tag string) (string, string) {
	if ns, val, ok := strings.Cut(tag, ":"); ok {
		return ns, val
	}
	return "", tag
}
