package mentalmodel

import (
	"memd/internal/domain"
	"memd/internal/port"
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

// predicate compiles the scope's tag filter (nil = no filter). With stalenessOnly, a tagged scope also
// excludes untagged units: untagged writes never make a tagged model stale, even when its refresh reads
// untagged memories (tags_match any / all). Fuzzy leaves are compiled as exact names here (decision 1203).
func (s ResolvedScope) predicate(stalenessOnly bool) *port.SQLPredicate {
	var p *port.SQLPredicate
	switch {
	case len(s.TagGroups) > 0:
		args := make([]port.SQLPredicate, len(s.TagGroups))
		for i, g := range s.TagGroups {
			args[i] = compileGroup(g)
		}
		p = &port.SQLPredicate{Op: port.PredicateAnd, Args: args}
	case len(s.Tags) > 0 || s.TagsMatch == domain.TagsMatchExact:
		p = &port.SQLPredicate{Op: port.PredicateTags, Tags: slices.Clone(s.Tags), Match: s.TagsMatch}
	}
	if !stalenessOnly || p == nil || (len(s.TagGroups) == 0 && len(s.Tags) == 0) {
		return p
	}
	untagged := port.SQLPredicate{Op: port.PredicateTags, Tags: []string{}, Match: domain.TagsMatchExact}
	tagged := port.SQLPredicate{Op: port.PredicateNot, Args: []port.SQLPredicate{untagged}}
	return &port.SQLPredicate{Op: port.PredicateAnd, Args: []port.SQLPredicate{*p, tagged}}
}

// compileGroup turns a tag group into a predicate node with domain.MatchGroup semantics; a malformed
// group compiles to an empty Or, which matches nothing.
func compileGroup(g domain.TagGroup) port.SQLPredicate {
	switch g.Kind {
	case domain.TagGroupLeaf:
		if g.Leaf == nil {
			break
		}
		mode := g.Leaf.Match
		if mode == domain.TagsMatchUnspecified {
			mode = domain.TagsMatchAnyStrict
		}
		return port.SQLPredicate{Op: port.PredicateTags, Tags: domain.NormalizeTags(g.Leaf.Tags), Match: mode}
	case domain.TagGroupAllOf, domain.TagGroupAnyOf:
		op := port.PredicateAnd
		if g.Kind == domain.TagGroupAnyOf {
			op = port.PredicateOr
		}
		args := make([]port.SQLPredicate, len(g.Groups))
		for i, c := range g.Groups {
			args[i] = compileGroup(c)
		}
		return port.SQLPredicate{Op: op, Args: args}
	case domain.TagGroupNoneOf:
		if g.Not != nil {
			return port.SQLPredicate{Op: port.PredicateNot, Args: []port.SQLPredicate{compileGroup(*g.Not)}}
		}
	}
	return port.SQLPredicate{Op: port.PredicateOr}
}

// scopeQuery is the ScopeReader query for m's scope over seq range r.
func scopeQuery(m domain.MentalModel, r port.SeqRange, stalenessOnly bool) ScopeQuery {
	s := resolveScope(m)
	return ScopeQuery{Bank: m.Bank, Types: s.FactTypes, Tags: s.predicate(stalenessOnly), Seq: r}
}
