package domain

import "time"

// Directive is a set of rules reflect must follow. Untagged directives are global; tagged ones apply
// when the request's tags match.
type Directive struct {
	// ID is the directive id; empty on create.
	ID DirectiveID
	// Bank owns the directive.
	Bank BankRef
	// Name is a display name.
	Name string
	// Rules are the instructions, rendered into the reflect prompt.
	Rules []string
	// Tags select which requests the directive applies to; none = global.
	Tags []string
	// Active directives are applied; inactive ones are kept but skipped.
	Active bool
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// SelectDirectives returns the directives of ds that apply to a request with tag filter f, in ds order. It is the
// one selection rule: reflect, the mental-model synthesize prompt and the prompt preview all use it. Inactive
// directives never apply. With applyAll every active one applies. Otherwise an untagged directive always applies,
// and a tagged one applies only when f is set and its tags satisfy f (the flat tags under f's match mode, then
// every tag group), so tag-scoped rules never leak into an untagged request. Callers append request-only
// directives after the selection; those are never used to drop a stored one.
func SelectDirectives(ds []Directive, f TagFilter, applyAll bool) []Directive {
	out := make([]Directive, 0, len(ds))
	for _, d := range ds {
		if d.Active && directiveApplies(d.Tags, f, applyAll) {
			out = append(out, d)
		}
	}
	return out
}

// directiveApplies reports whether a directive with tags applies to a request with filter f.
func directiveApplies(tags []string, f TagFilter, applyAll bool) bool {
	if applyAll || len(NormalizeTags(tags)) == 0 {
		return true
	}
	if len(f.Tags) == 0 && len(f.Groups) == 0 {
		return false
	}
	if len(f.Tags) > 0 && !MatchTags(tags, f.Tags, f.Match) {
		return false
	}
	for _, g := range f.Groups {
		if !MatchGroup(tags, g) {
			return false
		}
	}
	return true
}
