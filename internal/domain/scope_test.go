package domain

import (
	"errors"
	"slices"
	"testing"
)

func TestScopeKeyString(t *testing.T) {
	b := BankRef{Tenant: "t", Bank: "b"}
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{"shared", nil, ""},
		{"spec example", []string{"foo", "bar,b"}, "5:bar,b,3:foo"},
		{"normalized", []string{" Foo", "foo", "BAR"}, "3:bar,3:foo"},
		{"utf8 byte length", []string{"é"}, "2:é"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewScopeKey(b, tc.tags).String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
	if got := EncodeScopeTags([]string{"foo", "bar,b"}); got != "3:foo,5:bar,b" {
		t.Fatalf("EncodeScopeTags = %q, want the spec example 3:foo,5:bar,b", got)
	}
}

func TestScopeKeyNoCollisions(t *testing.T) {
	sets := [][]string{
		{"a,b"}, {"a", "b"}, {"1:a"}, {"a", "1:b"}, {"a,1:b"}, {}, {""}, {",", ""}, {"3:foo,5:bar"},
	}
	seen := map[string][]string{}
	for _, s := range sets {
		enc := EncodeScopeTags(s)
		if prev, ok := seen[enc]; ok && !slices.Equal(prev, s) {
			t.Fatalf("collision: %q and %q both encode to %q", prev, s, enc)
		}
		seen[enc] = s
		dec, err := DecodeScopeTags(enc)
		if err != nil {
			t.Fatalf("decode %q: %v", enc, err)
		}
		if len(s) == 1 && s[0] == "" {
			// A single empty tag encodes as "0:" and round-trips.
			if !slices.Equal(dec, s) {
				t.Fatalf("round trip %q: got %q", s, dec)
			}
			continue
		}
		if !slices.Equal(dec, s) && (len(s) != 0 || len(dec) != 0) {
			t.Fatalf("round trip %q: got %q", s, dec)
		}
	}
}

func TestParseScopeKeyCanonicalOnly(t *testing.T) {
	b := BankRef{Tenant: "t", Bank: "b"}
	k := NewScopeKey(b, []string{"Zed", "alpha", "é"})
	got, err := ParseScopeKey(b, k.String())
	if err != nil || !slices.Equal(got.Tags, k.Tags) || got.BankRef != b {
		t.Fatalf("ParseScopeKey(%q) = %+v, %v; want %+v", k.String(), got, err, k)
	}
	if got, err := ParseScopeKey(b, ""); err != nil || len(got.Tags) != 0 {
		t.Fatalf("shared scope: got %+v, %v", got, err)
	}
	for _, in := range []string{
		EncodeScopeTags([]string{"b", "a"}), // unsorted
		EncodeScopeTags([]string{"a", "a"}), // duplicate
		EncodeScopeTags([]string{"A"}),      // not lowercased
		EncodeScopeTags([]string{" a"}),     // not trimmed
		EncodeScopeTags([]string{""}),       // empty tag
		EncodeScopeTags([]string{"é"}),     // not NFC
		"3:foo;",                            // garbage
	} {
		if _, err := ParseScopeKey(b, in); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("ParseScopeKey(%q) err = %v, want ErrInvalidArgument", in, err)
		}
	}
}

func TestDecodeScopeTagsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"x", "3:fo", "3:foo;1:a", ":a", "-1:a"} {
		if _, err := DecodeScopeTags(in); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("DecodeScopeTags(%q) err = %v, want ErrInvalidArgument", in, err)
		}
	}
}
