package domain

import (
	"slices"
	"strings"
)

// TagsMatch is how a filter's tags compare against a unit's tags (Hindsight's five modes).
type TagsMatch string

// Tag match modes, mirroring memory.v1.TagsMatch.
const (
	// TagsMatchUnspecified is the zero value; the RPC's documented default applies.
	TagsMatchUnspecified TagsMatch = ""
	// TagsMatchAny is OR matching that also admits untagged units.
	TagsMatchAny TagsMatch = "any"
	// TagsMatchAll is AND matching that also admits untagged units.
	TagsMatchAll TagsMatch = "all"
	// TagsMatchExact is set equality; empty filter tags select only untagged units.
	TagsMatchExact TagsMatch = "exact"
	// TagsMatchAnyStrict is OR matching that excludes untagged units.
	TagsMatchAnyStrict TagsMatch = "any_strict"
	// TagsMatchAllStrict is AND matching that excludes untagged units.
	TagsMatchAllStrict TagsMatch = "all_strict"
)

// TagResolve says what a tag leaf's tags are: exact names or fuzzy (trigram-resolved) names.
type TagResolve string

// Tag resolution modes, mirroring memory.v1.TagLeaf.Resolve.
const (
	// TagResolveUnspecified is the zero value (exact names).
	TagResolveUnspecified TagResolve = ""
	// TagResolveExactNames matches tag names literally.
	TagResolveExactNames TagResolve = "exact_names"
	// TagResolveFuzzy resolves each name against the bank's tags by trigram similarity first.
	TagResolveFuzzy TagResolve = "fuzzy"
)

// TagGroupKind names which branch of a TagGroup is set.
type TagGroupKind string

// Tag group kinds, mirroring the memory.v1.TagGroup oneof.
const (
	// TagGroupLeaf matches a TagLeaf.
	TagGroupLeaf TagGroupKind = "leaf"
	// TagGroupAllOf matches when every child matches.
	TagGroupAllOf TagGroupKind = "all_of"
	// TagGroupAnyOf matches when at least one child matches.
	TagGroupAnyOf TagGroupKind = "any_of"
	// TagGroupNoneOf matches when its single child does not match.
	TagGroupNoneOf TagGroupKind = "none_of"
)

// TagLeaf is a flat tag filter inside a tag group.
type TagLeaf struct {
	// Tags are the leaf's tags.
	Tags []string `json:"tags,omitempty"`
	// Match is how Tags compare; unset = TagsMatchAnyStrict.
	Match TagsMatch `json:"match,omitempty"`
	// Resolve says whether Tags are exact names or fuzzy names.
	Resolve TagResolve `json:"resolve,omitempty"`
}

// TagGroup is a boolean tree of tag leaves. Exactly one branch, named by Kind, is set.
type TagGroup struct {
	// Kind names the set branch.
	Kind TagGroupKind `json:"kind"`
	// Leaf is set when Kind is TagGroupLeaf.
	Leaf *TagLeaf `json:"leaf,omitempty"`
	// Groups holds the children of all_of and any_of.
	Groups []TagGroup `json:"groups,omitempty"`
	// Not is the single child of none_of.
	Not *TagGroup `json:"not,omitempty"`
}

// TagFilter is a request's tag filter: flat tags with a match mode, or tag groups (mutually exclusive).
// Several top-level groups are AND-ed.
type TagFilter struct {
	// Tags are flat filter tags.
	Tags []string `json:"tags,omitempty"`
	// Match is how Tags compare.
	Match TagsMatch `json:"match,omitempty"`
	// Groups are tag groups; they override Tags and Match.
	Groups []TagGroup `json:"groups,omitempty"`
}

// IsEmpty reports whether the filter selects every unit.
func (f TagFilter) IsEmpty() bool {
	return len(f.Groups) == 0 && len(f.Tags) == 0 && f.Match != TagsMatchExact
}

// NormalizeTag trims and lowercases one tag. Unicode NFC normalization is pending CCR WP-00-1
// (golang.org/x/text is not an approved dependency yet).
func NormalizeTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}

// NormalizeTags returns the canonical form of a tag set: each tag trimmed and lowercased, empty tags
// dropped, deduplicated and sorted. The result is never nil.
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if n := NormalizeTag(t); n != "" {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SameTagSet reports whether a and b are equal as normalized sets (order-insensitive).
func SameTagSet(a, b []string) bool {
	return slices.Equal(NormalizeTags(a), NormalizeTags(b))
}

// MatchTags reports whether a unit with unitTags passes a flat filter of filterTags under mode, with
// Hindsight's semantics. Both sides are compared as normalized sets.
//   - any / all: empty filter = no filter; untagged units always pass; otherwise overlap / containment.
//   - any_strict / all_strict: empty filter = no filter; untagged units fail; overlap / containment.
//   - exact: set equality; an empty filter selects only untagged units (the shared scope).
//
// Unspecified behaves as any.
func MatchTags(unitTags, filterTags []string, mode TagsMatch) bool {
	u, f := NormalizeTags(unitTags), NormalizeTags(filterTags)
	if mode == TagsMatchExact {
		return slices.Equal(u, f)
	}
	if len(f) == 0 {
		return true
	}
	if len(u) == 0 {
		return mode != TagsMatchAnyStrict && mode != TagsMatchAllStrict
	}
	if mode == TagsMatchAll || mode == TagsMatchAllStrict {
		return containsAll(u, f)
	}
	return overlaps(u, f)
}

// MatchGroup evaluates a tag group against a unit's tags. Leaves use MatchTags with their own mode
// (unset = any_strict) and must already hold resolved names.
func MatchGroup(unitTags []string, g TagGroup) bool {
	switch g.Kind {
	case TagGroupLeaf:
		if g.Leaf == nil {
			return true
		}
		mode := g.Leaf.Match
		if mode == TagsMatchUnspecified {
			mode = TagsMatchAnyStrict
		}
		return MatchTags(unitTags, g.Leaf.Tags, mode)
	case TagGroupAllOf:
		for _, c := range g.Groups {
			if !MatchGroup(unitTags, c) {
				return false
			}
		}
		return true
	case TagGroupAnyOf:
		for _, c := range g.Groups {
			if MatchGroup(unitTags, c) {
				return true
			}
		}
		return len(g.Groups) == 0
	case TagGroupNoneOf:
		return g.Not == nil || !MatchGroup(unitTags, *g.Not)
	default:
		return true
	}
}

// MatchFilter evaluates a whole TagFilter: groups (AND-ed) when present, else the flat tags.
func MatchFilter(unitTags []string, f TagFilter) bool {
	if len(f.Groups) > 0 {
		for _, g := range f.Groups {
			if !MatchGroup(unitTags, g) {
				return false
			}
		}
		return true
	}
	return MatchTags(unitTags, f.Tags, f.Match)
}

// containsAll reports whether sorted set u contains every element of sorted set f.
func containsAll(u, f []string) bool {
	for _, t := range f {
		if _, ok := slices.BinarySearch(u, t); !ok {
			return false
		}
	}
	return true
}

// overlaps reports whether sorted sets u and f share an element.
func overlaps(u, f []string) bool {
	for _, t := range f {
		if _, ok := slices.BinarySearch(u, t); ok {
			return true
		}
	}
	return false
}
