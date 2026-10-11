package domain

import (
	"fmt"
	"slices"
	"strings"
)

// ExpandScopes returns the observation scopes a unit with tags consolidates under, fanning the tags out by mode
// (unspecified = combined). Untagged units have one scope, the shared one, in every mode except custom. The result
// is deduplicated and sorted by ScopeKey.String, so a unit's scopes are always decided in the same order.
// ALL_COMBINATIONS beyond max_combinations is ErrTooManyScopes (INVALID_ARGUMENT); retain checks it before storing
// anything, so Retain fails instead of a job failing later.
//   - combined: one scope, the full tag set.
//   - shared: the shared scope, whatever the tags.
//   - per_tag: one scope per tag.
//   - all_combinations: every non-empty subset, at most max_combinations (default 15) of them.
//   - custom: the explicit tag sets as given (normalized); none = combined.
func ExpandScopes(b BankRef, tags []string, s ObservationScopeModes) ([]ScopeKey, error) {
	tags = NormalizeTags(tags)
	var sets [][]string
	switch s.Mode {
	case ScopeModeShared:
		sets = [][]string{{}}
	case ScopeModePerTag:
		for _, t := range tags {
			sets = append(sets, []string{t})
		}
	case ScopeModeAllCombinations:
		limit := DefaultMaxCombinations
		if s.MaxCombinations != nil {
			if limit = *s.MaxCombinations; limit <= 0 {
				return nil, fmt.Errorf("%w: max_combinations must be positive, got %d", ErrInvalidArgument, limit)
			}
		}
		subsets, err := combinations(tags, limit)
		if err != nil {
			return nil, err
		}
		sets = subsets
	case ScopeModeCustom:
		for _, c := range s.Custom {
			sets = append(sets, c.Tags)
		}
	case ScopeModeCombined, ScopeModeUnspecified:
	default:
		return nil, fmt.Errorf("%w: unknown observation scope mode %q", ErrInvalidArgument, s.Mode)
	}
	if len(sets) == 0 {
		sets = [][]string{tags}
	}
	return scopeKeys(b, sets), nil
}

// scopeKeys builds normalized, deduplicated scope keys sorted by their encoding.
func scopeKeys(b BankRef, sets [][]string) []ScopeKey {
	seen := map[string]bool{}
	var out []ScopeKey
	for _, set := range sets {
		k := NewScopeKey(b, set)
		if !seen[k.String()] {
			seen[k.String()] = true
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(x, y ScopeKey) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// combinations returns every non-empty subset of tags, each in the order tags gives (sorted when tags
// is normalized). Subsets are ordered by size, then lexicographically: ["a"], ["b"], ["a","b"]. ExpandScopes
// re-sorts its result by ScopeKey.String. More than limit subsets is ErrTooManyScopes.
func combinations(tags []string, limit int) ([][]string, error) {
	n := len(tags)
	if n == 0 {
		return nil, nil
	}
	if n >= 31 || (1<<n)-1 > limit {
		return nil, fmt.Errorf("%w: %d tags make %s subsets, max_combinations is %d",
			ErrTooManyScopes, n, subsetCount(n), limit)
	}
	out := make([][]string, 0, (1<<n)-1)
	for mask := 1; mask < 1<<n; mask++ {
		var set []string
		for i := range n {
			if mask&(1<<i) != 0 {
				set = append(set, tags[i])
			}
		}
		out = append(out, set)
	}
	slices.SortFunc(out, func(x, y []string) int {
		if len(x) != len(y) {
			return len(x) - len(y)
		}
		return slices.Compare(x, y)
	})
	return out, nil
}

// subsetCount renders 2^n - 1 for an error message without overflowing.
func subsetCount(n int) string {
	if n >= 63 {
		return fmt.Sprintf("2^%d-1", n)
	}
	return fmt.Sprint(int64(1)<<n - 1)
}
