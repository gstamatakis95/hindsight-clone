package mentalmodel

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/recall"
	"memd/internal/store"
	"slices"
)

// resolveScope applies the scope defaults to m (domain.MentalModelSpec.ScopeFilter): the match mode defaults to
// all_strict when the model is tagged (any otherwise), and tag groups override the flat tags.
func resolveScope(m domain.MentalModel) ResolvedScope {
	return ResolvedScope{Tags: m.Spec.ScopeFilter(), FactTypes: slices.Clone(m.Spec.Scope.FactTypes)}
}

// tagFilter is the scope as the recall and reflect libraries take it.
func (s ResolvedScope) tagFilter() domain.TagFilter {
	return domain.TagFilter{Tags: slices.Clone(s.Tags.Tags), Match: s.Tags.Match, Groups: slices.Clone(s.Tags.Groups)}
}

// predicate adds the staleness rule to p, the scope's compiled tag filter (nil = no filter). With
// stalenessOnly, a tagged scope also excludes untagged units: untagged writes never make a tagged model
// stale, even when its refresh reads untagged memories (tags_match any / all).
func (s ResolvedScope) predicate(p *domain.TagPredicate, stalenessOnly bool) *domain.TagPredicate {
	if !stalenessOnly || (len(s.Tags.Groups) == 0 && len(s.Tags.Tags) == 0) {
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
	f := s.Tags
	switch {
	case len(f.Groups) > 0:
		args := make([]domain.TagPredicate, len(f.Groups))
		for i, g := range f.Groups {
			args[i] = compileGroup(g)
		}
		return &domain.TagPredicate{Op: domain.PredicateAnd, Args: args}
	case len(f.Tags) > 0 || f.Match == domain.TagsMatchExact:
		return &domain.TagPredicate{Op: domain.PredicateTags, Tags: slices.Clone(f.Tags), Match: f.Match}
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

// scopeQuery is the NewestSeqInScope query for m's scope over seq range r. A scope with a fuzzy leaf first
// resolves its names to the bank's exact tags with recall's tag compiler (tc) at minSim, so staleness and the
// pre-check see exactly the units recall would retrieve.
func scopeQuery(
	ctx context.Context, tc *recall.Engine, m domain.MentalModel, r domain.SeqRange, stalenessOnly bool, minSim float64,
) (store.ScopeQuery, error) {
	s := resolveScope(m)
	tags := s.tagPredicate()
	if hasFuzzyLeaf(s.Tags.Groups) {
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
