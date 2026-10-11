package config

import "memd/internal/domain"

// MatchStrategy returns the first strategy in list order with a pattern matching scope s, and whether there is
// one; the winner applies whole (a later strategy never fills its gaps). Resolver.ForScope slots it in below the
// request level.
func MatchStrategy(strategies []domain.ConsolidationStrategy, s domain.ScopeKey) (*domain.ConsolidationStrategy, bool) {
	for i := range strategies {
		if strategyClaims(strategies[i], s.Tags) {
			st := strategies[i]
			return &st, true
		}
	}
	return nil, false
}

// strategyClaims reports whether any pattern of st matches a scope with tags.
func strategyClaims(st domain.ConsolidationStrategy, tags []string) bool {
	for _, p := range st.Scopes {
		if patternMatches(p, tags) {
			return true
		}
	}
	return false
}

// patternMatches compares a scope's tags against one pattern with domain.MatchTags. Unspecified mode is
// all_strict: the scope holds every pattern tag (others allowed) and the shared scope never matches, as
// in Hindsight. A pattern with no tags matches only under exact (the shared scope); otherwise it is
// ignored, so an empty pattern never claims every scope. An explicit any or all pattern follows
// MatchTags, which admits untagged units, so it also matches the shared scope; use any_strict or
// all_strict (or leave the mode unset) to exclude it.
func patternMatches(p domain.ScopePattern, tags []string) bool {
	mode := p.TagsMatch
	if mode == domain.TagsMatchUnspecified {
		mode = domain.TagsMatchAllStrict
	}
	if len(domain.NormalizeTags(p.Tags)) == 0 && mode != domain.TagsMatchExact {
		return false
	}
	return domain.MatchTags(tags, p.Tags, mode)
}
