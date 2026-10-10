package reflect

import "memd/internal/domain"

// selectDirectives picks the stored directives a reflect applies, then appends the request's extra
// directives. Stored directives follow Hindsight's scoping (list_directives with isolation_mode): inactive
// ones are skipped; with applyAll every active one applies; otherwise untagged directives always apply and a
// tagged one applies only when the request has a tag filter that its tags satisfy (flat tags with the
// request's match mode, then every tag group). An untagged request gets the untagged directives only, so
// tag-scoped rules never leak into it. Extra directives are only ever added, never used to drop a stored one.
func selectDirectives(
	stored []domain.Directive, f domain.TagFilter, applyAll bool, extra []domain.Directive,
) []domain.Directive {
	out := make([]domain.Directive, 0, len(stored)+len(extra))
	for _, d := range stored {
		if d.Active && directiveApplies(d.Tags, f, applyAll) {
			out = append(out, d)
		}
	}
	return append(out, extra...)
}

// directiveApplies reports whether a directive with tags applies to a request with filter f.
func directiveApplies(tags []string, f domain.TagFilter, applyAll bool) bool {
	if applyAll || len(domain.NormalizeTags(tags)) == 0 {
		return true
	}
	if len(f.Tags) == 0 && len(f.Groups) == 0 {
		return false
	}
	if len(f.Tags) > 0 && !domain.MatchTags(tags, f.Tags, f.Match) {
		return false
	}
	for _, g := range f.Groups {
		if !domain.MatchGroup(tags, g) {
			return false
		}
	}
	return true
}
