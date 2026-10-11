package domain

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
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

// TagFilter is a request's tag filter: flat tags with a match mode, or tag groups. On Recall and Reflect
// the two are mutually exclusive (Validate rejects both with ErrInvalidArgument). Several top-level groups
// are AND-ed. Only a mental model's RefreshTrigger.tag_groups override its tags; that is resolved before a
// TagFilter is built.
type TagFilter struct {
	// Tags are flat filter tags.
	Tags []string `json:"tags,omitempty"`
	// Match is how Tags compare.
	Match TagsMatch `json:"match,omitempty"`
	// Groups are tag groups, AND-ed; mutually exclusive with Tags on Recall and Reflect.
	Groups []TagGroup `json:"groups,omitempty"`
}

// Validate rejects a filter that sets both flat tags and tag groups (INVALID_ARGUMENT on Recall and
// Reflect) and groups whose Kind and branches disagree.
func (f TagFilter) Validate() error {
	if len(f.Tags) > 0 && len(f.Groups) > 0 {
		return fmt.Errorf("%w: tags and tag_groups are mutually exclusive", ErrInvalidArgument)
	}
	for _, g := range f.Groups {
		if err := g.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks that exactly the branch named by Kind is set, recursively.
func (g TagGroup) Validate() error {
	ok := false
	switch g.Kind {
	case TagGroupLeaf:
		ok = g.Leaf != nil && len(g.Groups) == 0 && g.Not == nil
	case TagGroupAllOf, TagGroupAnyOf:
		ok = g.Leaf == nil && g.Not == nil
	case TagGroupNoneOf:
		ok = g.Leaf == nil && len(g.Groups) == 0 && g.Not != nil
	}
	if !ok {
		return fmt.Errorf("%w: malformed tag group %q", ErrInvalidArgument, g.Kind)
	}
	for _, c := range g.Groups {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	if g.Not != nil {
		return g.Not.Validate()
	}
	return nil
}

// IsEmpty reports whether the filter selects every unit.
func (f TagFilter) IsEmpty() bool {
	return len(f.Groups) == 0 && len(f.Tags) == 0 && f.Match != TagsMatchExact
}

// NormalizeTag applies Unicode NFC, then trims and lowercases one tag (NFC again after lowercasing, so
// the result is always in NFC).
func NormalizeTag(tag string) string {
	return norm.NFC.String(strings.ToLower(strings.TrimSpace(norm.NFC.String(tag))))
}

// NormalizeTags returns the canonical form of a tag set: each tag NFC-normalized, trimmed and lowercased,
// empty tags dropped, deduplicated and sorted. The result is never nil.
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
// (unset = any_strict) and must already hold resolved names. Semantics match store.SQLPredicate.Eval: an
// empty all_of is true, an empty any_of is false, a leaf with no tags is no filter (except exact), and a
// malformed group (unknown kind, missing leaf or child) matches nothing.
func MatchGroup(unitTags []string, g TagGroup) bool {
	switch g.Kind {
	case TagGroupLeaf:
		if g.Leaf == nil {
			return false
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
		return false
	case TagGroupNoneOf:
		return g.Not != nil && !MatchGroup(unitTags, *g.Not)
	default:
		return false
	}
}

// MatchFilter evaluates a whole TagFilter: the flat tags AND every group. It never drops a part of the
// filter; callers on Recall and Reflect run Validate first, so at most one part is set there.
func MatchFilter(unitTags []string, f TagFilter) bool {
	for _, g := range f.Groups {
		if !MatchGroup(unitTags, g) {
			return false
		}
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
