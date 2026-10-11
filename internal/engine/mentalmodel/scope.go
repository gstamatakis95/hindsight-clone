package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
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
func (s ResolvedScope) predicate(p *domain.TagPredicate, stalenessOnly bool) *domain.TagPredicate {
	if !stalenessOnly || (len(s.TagGroups) == 0 && len(s.Tags) == 0) {
		return p
	}
	untagged := domain.TagPredicate{Op: domain.PredicateTags, Tags: []string{}, Match: domain.TagsMatchExact}
	tagged := domain.TagPredicate{Op: domain.PredicateNot, Args: []domain.TagPredicate{untagged}}
	if p == nil {
		return &tagged
	}
	return &domain.TagPredicate{Op: domain.PredicateAnd, Args: []domain.TagPredicate{*p, tagged}}
}

// tagPredicate compiles the tags or tag groups alone; nil = no tag filter. A scope with a fuzzy leaf needs
// the bank's tags and compiles in scopeQuery instead.
func (s ResolvedScope) tagPredicate() *domain.TagPredicate {
	switch {
	case len(s.TagGroups) > 0:
		args := make([]domain.TagPredicate, len(s.TagGroups))
		for i, g := range s.TagGroups {
			args[i] = compileGroup(g)
		}
		return &domain.TagPredicate{Op: domain.PredicateAnd, Args: args}
	case len(s.Tags) > 0 || s.TagsMatch == domain.TagsMatchExact:
		return &domain.TagPredicate{Op: domain.PredicateTags, Tags: slices.Clone(s.Tags), Match: s.TagsMatch}
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
func compileGroup(g domain.TagGroup) domain.TagPredicate {
	switch g.Kind {
	case domain.TagGroupLeaf:
		if g.Leaf == nil {
			break
		}
		mode := g.Leaf.Match
		if mode == domain.TagsMatchUnspecified {
			mode = domain.TagsMatchAnyStrict
		}
		return domain.TagPredicate{Op: domain.PredicateTags, Tags: domain.NormalizeTags(g.Leaf.Tags), Match: mode}
	case domain.TagGroupAllOf, domain.TagGroupAnyOf:
		op := domain.PredicateAnd
		if g.Kind == domain.TagGroupAnyOf {
			op = domain.PredicateOr
		}
		args := make([]domain.TagPredicate, len(g.Groups))
		for i, c := range g.Groups {
			args[i] = compileGroup(c)
		}
		return domain.TagPredicate{Op: op, Args: args}
	case domain.TagGroupNoneOf:
		if g.Not != nil {
			return domain.TagPredicate{Op: domain.PredicateNot, Args: []domain.TagPredicate{compileGroup(*g.Not)}}
		}
	}
	return domain.TagPredicate{Op: domain.PredicateOr}
}

// TagCompiler compiles a tag filter, resolving fuzzy leaves against the bank's tags; recall.Engine satisfies it.
type TagCompiler interface {
	// CompileTags resolves f's fuzzy leaves at minSimilarity and compiles f into a tag predicate.
	CompileTags(
		ctx context.Context, b domain.BankRef, f domain.TagFilter, minSimilarity float64,
	) (domain.TagPredicate, error)
}

// scopeQuery is the ScopeReader query for m's scope over seq range r. A scope with a fuzzy leaf first
// resolves its names to the bank's exact tags with recall's tag compiler (tc) at minSim, so staleness and the
// pre-check see exactly the units recall would retrieve.
func scopeQuery(
	ctx context.Context, tc TagCompiler, m domain.MentalModel, r domain.SeqRange, stalenessOnly bool, minSim float64,
) (store.ScopeQuery, error) {
	s := resolveScope(m)
	tags := s.tagPredicate()
	if hasFuzzyLeaf(s.TagGroups) {
		if tc == nil {
			return store.ScopeQuery{}, fmt.Errorf("%w: mentalmodel: no tag compiler for a fuzzy scope",
				domain.ErrFailedPrecondition)
		}
		p, err := tc.CompileTags(ctx, m.Bank, s.tagFilter(), minSim)
		if err != nil {
			return store.ScopeQuery{}, fmt.Errorf("mentalmodel: resolve fuzzy scope: %w", err)
		}
		tags = &p
	}
	return store.ScopeQuery{Bank: m.Bank, Types: s.FactTypes, Tags: s.predicate(tags, stalenessOnly), Seq: r}, nil
}
