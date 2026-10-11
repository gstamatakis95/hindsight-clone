package domain

import (
	"errors"
	"slices"
	"testing"
)

// TestExpandScopesModes: every scope mode expands correctly, including untagged units.
func TestExpandScopesModes(t *testing.T) {
	tags := []string{"B", " a ", "c"}
	tests := []struct {
		name string
		tags []string
		mode ObservationScopeModes
		want [][]string
	}{
		{"unspecified is combined", tags, ObservationScopeModes{}, [][]string{{"a", "b", "c"}}},
		{"combined", tags, ObservationScopeModes{Mode: ScopeModeCombined}, [][]string{{"a", "b", "c"}}},
		{"combined untagged", nil, ObservationScopeModes{Mode: ScopeModeCombined}, [][]string{{}}},
		{"shared", tags, ObservationScopeModes{Mode: ScopeModeShared}, [][]string{{}}},
		{"per_tag", tags, ObservationScopeModes{Mode: ScopeModePerTag}, [][]string{{"a"}, {"b"}, {"c"}}},
		{"per_tag untagged", nil, ObservationScopeModes{Mode: ScopeModePerTag}, [][]string{{}}},
		{
			"all_combinations",
			[]string{"a", "b"},
			ObservationScopeModes{Mode: ScopeModeAllCombinations},
			[][]string{{"a"}, {"a", "b"}, {"b"}},
		},
		{"all_combinations untagged", nil, ObservationScopeModes{Mode: ScopeModeAllCombinations}, [][]string{{}}},
		{
			"custom", tags,
			ObservationScopeModes{Mode: ScopeModeCustom, Custom: []TagSet{
				{Tags: []string{"Team"}}, {Tags: []string{"team"}}, {Tags: nil},
			}},
			[][]string{{}, {"team"}},
		},
		{
			"custom without sets is combined", tags,
			ObservationScopeModes{Mode: ScopeModeCustom},
			[][]string{{"a", "b", "c"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := ExpandScopes(bank, tc.tags, tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			var got [][]string
			for _, k := range keys {
				if k.BankRef != bank {
					t.Fatalf("scope of bank %v", k.BankRef)
				}
				got = append(got, k.Tags)
			}
			if !slices.EqualFunc(got, tc.want, func(a, b []string) bool { return slices.Equal(a, b) }) {
				t.Fatalf("Expand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExpandScopesTooManyCombinations: ALL_COMBINATIONS beyond max_combinations is ErrTooManyScopes
// (INVALID_ARGUMENT); an unknown mode is INVALID_ARGUMENT too.
func TestExpandScopesTooManyCombinations(t *testing.T) {
	four := []string{"a", "b", "c", "d"} // 15 subsets: exactly the default cap
	if keys, err := ExpandScopes(bank, four, ObservationScopeModes{Mode: ScopeModeAllCombinations}); err != nil ||
		len(keys) != 15 {
		t.Fatalf("4 tags at the default cap: %d scopes, %v", len(keys), err)
	}
	_, err := ExpandScopes(bank, append(four, "e"), ObservationScopeModes{Mode: ScopeModeAllCombinations})
	if !errors.Is(err, ErrTooManyScopes) || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("5 tags: err = %v, want ErrTooManyScopes", err)
	}
	_, err = ExpandScopes(bank, []string{"a", "b"}, ObservationScopeModes{
		Mode: ScopeModeAllCombinations, MaxCombinations: ptr(2),
	})
	if !errors.Is(err, ErrTooManyScopes) {
		t.Fatalf("custom cap 2 with 3 subsets: err = %v", err)
	}
	_, err = ExpandScopes(bank, four, ObservationScopeModes{Mode: "bogus"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unknown mode: err = %v", err)
	}
}

// TestCombinations: non-empty subsets, sorted by size then lexicographically, capped.
func TestCombinations(t *testing.T) {
	got, err := combinations([]string{"a", "b", "c"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a"}, {"b"}, {"c"}, {"a", "b"}, {"a", "c"}, {"b", "c"}, {"a", "b", "c"}}
	if !slices.EqualFunc(got, want, func(a, b []string) bool { return slices.Equal(a, b) }) {
		t.Fatalf("combinations = %q, want %q", got, want)
	}
	if got, err := combinations(nil, 15); err != nil || len(got) != 0 {
		t.Fatalf("no tags: %q, %v", got, err)
	}
	if _, err := combinations([]string{"a", "b", "c"}, 6); !errors.Is(err, ErrTooManyScopes) {
		t.Fatalf("7 subsets over a cap of 6: err = %v", err)
	}
	many := make([]string, 40)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if _, err := combinations(many, 15); !errors.Is(err, ErrTooManyScopes) {
		t.Fatalf("40 tags: err = %v", err)
	}
}

// TestExpandScopesRejectsNonPositiveCap: max_combinations <= 0 is INVALID_ARGUMENT.
func TestExpandScopesRejectsNonPositiveCap(t *testing.T) {
	for _, n := range []int{0, -1} {
		s := ObservationScopeModes{Mode: ScopeModeAllCombinations, MaxCombinations: ptr(n)}
		_, err := ExpandScopes(bank, []string{"a"}, s)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("max_combinations %d: err = %v", n, err)
		}
	}
}

// bank is the bank the scope tests expand under.
var bank = BankRef{Tenant: "t", Bank: "b"}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
