package consolidate

import (
	"fmt"
	"memd/internal/domain"
	"slices"
	"strings"
)

// ScopeExpander turns a unit's tags and scope mode into the scopes it consolidates under.
type ScopeExpander interface {
	// Expand returns the scopes for tags under s. ALL_COMBINATIONS beyond max_combinations returns
	// ErrTooManyScopes; the Ingestor calls Expand before storing anything, so Retain fails with
	// INVALID_ARGUMENT instead of a job failing later.
	Expand(b domain.BankRef, tags []string, s domain.ObservationScopes) ([]domain.ScopeKey, error)
}

// scopeExpander implements ScopeExpander.
type scopeExpander struct{}

var _ ScopeExpander = scopeExpander{}

// NewScopeExpander returns the ScopeExpander.
func NewScopeExpander() ScopeExpander { return scopeExpander{} }

// Expand fans tags out by mode (unspecified = combined). Untagged units have one scope, the shared one,
// in every mode except custom. The result is deduplicated and sorted by ScopeKey.String, so a unit's
// scopes are always decided in the same order.
//   - combined: one scope, the full tag set.
//   - shared: the shared scope, whatever the tags.
//   - per_tag: one scope per tag.
//   - all_combinations: every non-empty subset, at most max_combinations (default 15) of them.
//   - custom: the explicit tag sets as given (normalized); none = combined.
func (scopeExpander) Expand(b domain.BankRef, tags []string, s domain.ObservationScopes) ([]domain.ScopeKey, error) {
	tags = domain.NormalizeTags(tags)
	var sets [][]string
	switch s.Mode {
	case domain.ScopeModeShared:
		sets = [][]string{{}}
	case domain.ScopeModePerTag:
		for _, t := range tags {
			sets = append(sets, []string{t})
		}
	case domain.ScopeModeAllCombinations:
		limit := domain.DefaultMaxCombinations
		if s.MaxCombinations != nil {
			if limit = *s.MaxCombinations; limit <= 0 {
				return nil, fmt.Errorf("%w: max_combinations must be positive, got %d", domain.ErrInvalidArgument, limit)
			}
		}
		subsets, err := combinations(tags, limit)
		if err != nil {
			return nil, err
		}
		sets = subsets
	case domain.ScopeModeCustom:
		for _, c := range s.Custom {
			sets = append(sets, c.Tags)
		}
	case domain.ScopeModeCombined, domain.ScopeModeUnspecified:
	default:
		return nil, fmt.Errorf("%w: unknown observation scope mode %q", domain.ErrInvalidArgument, s.Mode)
	}
	if len(sets) == 0 {
		sets = [][]string{tags}
	}
	return scopeKeys(b, sets), nil
}

// scopeKeys builds normalized, deduplicated scope keys sorted by their encoding.
func scopeKeys(b domain.BankRef, sets [][]string) []domain.ScopeKey {
	seen := map[string]bool{}
	var out []domain.ScopeKey
	for _, set := range sets {
		k := domain.NewScopeKey(b, set)
		if !seen[k.String()] {
			seen[k.String()] = true
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(x, y domain.ScopeKey) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// combinations returns every non-empty subset of tags, each in the order tags gives (sorted when tags
// is normalized). Subsets are ordered by size, then lexicographically: ["a"], ["b"], ["a","b"]. Expand
// re-sorts its result by ScopeKey.String. More than limit subsets is domain.ErrTooManyScopes.
func combinations(tags []string, limit int) ([][]string, error) {
	n := len(tags)
	if n == 0 {
		return nil, nil
	}
	if n >= 31 || (1<<n)-1 > limit {
		return nil, fmt.Errorf("%w: %d tags make %s subsets, max_combinations is %d",
			domain.ErrTooManyScopes, n, subsetCount(n), limit)
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
