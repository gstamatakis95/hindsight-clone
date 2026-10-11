package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/store"
	"slices"
)

// resolveScope applies the scope defaults to m: tags_match defaults to all_strict when the model is tagged
// (any otherwise), and tag groups override the flat tags.
func resolveScope(m domain.MentalModel) ResolvedScope {
	t := m.Spec.Trigger
	s := ResolvedScope{FactTypes: slices.Clone(t.FactTypes)}
	if len(t.TagGroups) > 0 {
		s.TagGroups = slices.Clone(t.TagGroups)
		s.TagsMatch = domain.TagsMatchUnspecified
		return s
	}
	s.Tags = domain.NormalizeTags(m.Spec.Tags)
	s.TagsMatch = t.TagsMatch
	if s.TagsMatch == domain.TagsMatchUnspecified {
		s.TagsMatch = domain.TagsMatchAny
		if len(s.Tags) > 0 {
			s.TagsMatch = domain.TagsMatchAllStrict
		}
	}
	return s
}

// tagFilter is the scope as the recall and reflect engines take it.
func (s ResolvedScope) tagFilter() domain.TagFilter {
	if len(s.TagGroups) > 0 {
		return domain.TagFilter{Groups: slices.Clone(s.TagGroups)}
	}
	return domain.TagFilter{Tags: slices.Clone(s.Tags), Match: s.TagsMatch}
}

// predicate adds the staleness rule to p, the scope's compiled tag filter (nil = no filter). With
// stalenessOnly, a tagged scope also excludes untagged units: untagged writes never make a tagged model
// stale, even when its refresh reads untagged memories (tags_match any / all).
func (s ResolvedScope) predicate(p *store.SQLPredicate, stalenessOnly bool) *store.SQLPredicate {
	if !stalenessOnly || (len(s.TagGroups) == 0 && len(s.Tags) == 0) {
		return p
	}
	untagged := store.SQLPredicate{Op: store.PredicateTags, Tags: []string{}, Match: domain.TagsMatchExact}
	tagged := store.SQLPredicate{Op: store.PredicateNot, Args: []store.SQLPredicate{untagged}}
	if p == nil {
		return &tagged
	}
	return &store.SQLPredicate{Op: store.PredicateAnd, Args: []store.SQLPredicate{*p, tagged}}
}

// tagPredicate compiles the tags or tag groups alone; nil = no tag filter. A scope with a fuzzy leaf needs
// the bank's tags and compiles in scopeQuery instead.
func (s ResolvedScope) tagPredicate() *store.SQLPredicate {
	switch {
	case len(s.TagGroups) > 0:
		args := make([]store.SQLPredicate, len(s.TagGroups))
		for i, g := range s.TagGroups {
			args[i] = compileGroup(g)
		}
		return &store.SQLPredicate{Op: store.PredicateAnd, Args: args}
	case len(s.Tags) > 0 || s.TagsMatch == domain.TagsMatchExact:
		return &store.SQLPredicate{Op: store.PredicateTags, Tags: slices.Clone(s.Tags), Match: s.TagsMatch}
	}
	return nil
}

// hasFuzzyLeaf reports whether any leaf in gs (at any depth) resolves its names fuzzily.
func hasFuzzyLeaf(gs []domain.TagGroup) bool {
	for _, g := range gs {
		if g.Leaf != nil && g.Leaf.Resolve == domain.TagResolveFuzzy {
			return true
		}
		if hasFuzzyLeaf(g.Groups) || (g.Not != nil && hasFuzzyLeaf([]domain.TagGroup{*g.Not})) {
			return true
		}
	}
	return false
}

// compileGroup turns a tag group into a predicate node with domain.MatchGroup semantics; a malformed
// group compiles to an empty Or, which matches nothing.
func compileGroup(g domain.TagGroup) store.SQLPredicate {
	switch g.Kind {
	case domain.TagGroupLeaf:
		if g.Leaf == nil {
			break
		}
		mode := g.Leaf.Match
		if mode == domain.TagsMatchUnspecified {
			mode = domain.TagsMatchAnyStrict
		}
		return store.SQLPredicate{Op: store.PredicateTags, Tags: domain.NormalizeTags(g.Leaf.Tags), Match: mode}
	case domain.TagGroupAllOf, domain.TagGroupAnyOf:
		op := store.PredicateAnd
		if g.Kind == domain.TagGroupAnyOf {
			op = store.PredicateOr
		}
		args := make([]store.SQLPredicate, len(g.Groups))
		for i, c := range g.Groups {
			args[i] = compileGroup(c)
		}
		return store.SQLPredicate{Op: op, Args: args}
	case domain.TagGroupNoneOf:
		if g.Not != nil {
			return store.SQLPredicate{Op: store.PredicateNot, Args: []store.SQLPredicate{compileGroup(*g.Not)}}
		}
	}
	return store.SQLPredicate{Op: store.PredicateOr}
}

// scopeQuery is the ScopeReader query for m's scope over seq range r. A scope with a fuzzy leaf first
// resolves its names to the bank's exact tags (sr.TagCounts) with recall's TagFilterCompiler at minSim, so
// staleness and the pre-check see exactly the units recall would retrieve (decision 1203).
func scopeQuery(
	ctx context.Context, sr ScopeReader, m domain.MentalModel, r store.SeqRange, stalenessOnly bool, minSim float64,
) (store.ScopeQuery, error) {
	s := resolveScope(m)
	tags := s.tagPredicate()
	if hasFuzzyLeaf(s.TagGroups) {
		p, err := recall.NewTagFilterCompiler(sr).Compile(ctx, m.Bank, s.tagFilter(), minSim)
		if err != nil {
			return store.ScopeQuery{}, fmt.Errorf("mentalmodel: resolve fuzzy scope: %w", err)
		}
		tags = &p
	}
	return store.ScopeQuery{Bank: m.Bank, Types: s.FactTypes, Tags: s.predicate(tags, stalenessOnly), Seq: r}, nil
}
