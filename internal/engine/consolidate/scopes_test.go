package consolidate

import (
	"errors"
	"memd/internal/domain"
	"slices"
	"testing"
)

// TestScopeExpanderModes: every scope mode expands correctly, including untagged units.
func TestScopeExpanderModes(t *testing.T) {
	tags := []string{"B", " a ", "c"}
	tests := []struct {
		name string
		tags []string
		mode domain.ObservationScopes
		want [][]string
	}{
		{"unspecified is combined", tags, domain.ObservationScopes{}, [][]string{{"a", "b", "c"}}},
		{"combined", tags, domain.ObservationScopes{Mode: domain.ScopeModeCombined}, [][]string{{"a", "b", "c"}}},
		{"combined untagged", nil, domain.ObservationScopes{Mode: domain.ScopeModeCombined}, [][]string{{}}},
		{"shared", tags, domain.ObservationScopes{Mode: domain.ScopeModeShared}, [][]string{{}}},
		{"per_tag", tags, domain.ObservationScopes{Mode: domain.ScopeModePerTag}, [][]string{{"a"}, {"b"}, {"c"}}},
		{"per_tag untagged", nil, domain.ObservationScopes{Mode: domain.ScopeModePerTag}, [][]string{{}}},
		{
			"all_combinations",
			[]string{"a", "b"},
			domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations},
			[][]string{{"a"}, {"a", "b"}, {"b"}},
		},
		{"all_combinations untagged", nil, domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations}, [][]string{{}}},
		{
			"custom", tags,
			domain.ObservationScopes{Mode: domain.ScopeModeCustom, Custom: []domain.TagSet{
				{Tags: []string{"Team"}}, {Tags: []string{"team"}}, {Tags: nil},
			}},
			[][]string{{}, {"team"}},
		},
		{
			"custom without sets is combined", tags,
			domain.ObservationScopes{Mode: domain.ScopeModeCustom},
			[][]string{{"a", "b", "c"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := NewScopeExpander().Expand(bank, tc.tags, tc.mode)
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

// TestScopeExpanderTooManyCombinations: ALL_COMBINATIONS beyond max_combinations is ErrTooManyScopes
// (INVALID_ARGUMENT); an unknown mode is INVALID_ARGUMENT too.
func TestScopeExpanderTooManyCombinations(t *testing.T) {
	x := NewScopeExpander()
	four := []string{"a", "b", "c", "d"} // 15 subsets: exactly the default cap
	if keys, err := x.Expand(bank, four, domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations}); err != nil ||
		len(keys) != 15 {
		t.Fatalf("4 tags at the default cap: %d scopes, %v", len(keys), err)
	}
	_, err := x.Expand(bank, append(four, "e"), domain.ObservationScopes{Mode: domain.ScopeModeAllCombinations})
	if !errors.Is(err, domain.ErrTooManyScopes) || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("5 tags: err = %v, want ErrTooManyScopes", err)
	}
	_, err = x.Expand(bank, []string{"a", "b"}, domain.ObservationScopes{
		Mode: domain.ScopeModeAllCombinations, MaxCombinations: ptr(2),
	})
	if !errors.Is(err, domain.ErrTooManyScopes) {
		t.Fatalf("custom cap 2 with 3 subsets: err = %v", err)
	}
	_, err = x.Expand(bank, four, domain.ObservationScopes{Mode: "bogus"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
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
	if _, err := combinations([]string{"a", "b", "c"}, 6); !errors.Is(err, domain.ErrTooManyScopes) {
		t.Fatalf("7 subsets over a cap of 6: err = %v", err)
	}
	many := make([]string, 40)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if _, err := combinations(many, 15); !errors.Is(err, domain.ErrTooManyScopes) {
		t.Fatalf("40 tags: err = %v", err)
	}
}
