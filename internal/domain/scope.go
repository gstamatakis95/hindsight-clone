package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// ScopeKey identifies one observation scope: a canonical tag set. Empty Tags = the shared scope.
type ScopeKey struct {
	BankRef
	// Tags are normalized by NormalizeTags: trimmed, lowercased, deduplicated, sorted.
	Tags []string
}

// NewScopeKey builds a ScopeKey with normalized tags.
func NewScopeKey(b BankRef, tags []string) ScopeKey {
	return ScopeKey{BankRef: b, Tags: NormalizeTags(tags)}
}

// String is a length-prefixed encoding of the tags ("3:foo,5:bar,b" for ["foo", "bar,b"]), so no tag
// content can make two scopes collide. Each tag is written as its byte length, a colon and the tag;
// tags are joined by commas. The shared scope (no tags) encodes as "". The bank is not part of the
// encoding: storage keys scopes by (tenant, bank, String()).
func (s ScopeKey) String() string {
	return EncodeScopeTags(s.Tags)
}

// IsShared reports whether s is the shared (untagged) scope.
func (s ScopeKey) IsShared() bool { return len(s.Tags) == 0 }

// EncodeScopeTags returns the length-prefixed encoding of tags exactly as given (callers normalize).
func EncodeScopeTags(tags []string) string {
	var b strings.Builder
	for i, t := range tags {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(len(t)))
		b.WriteByte(':')
		b.WriteString(t)
	}
	return b.String()
}

// ParseScopeKey decodes a stored scope key (ScopeKey.String) for bank b. It accepts only the canonical
// encoding: every tag normalized and non-empty, strictly increasing (sorted, no duplicates). Anything else
// is ErrInvalidArgument, so one scope has exactly one key.
func ParseScopeKey(b BankRef, s string) (ScopeKey, error) {
	tags, err := DecodeScopeTags(s)
	if err != nil {
		return ScopeKey{}, err
	}
	for i, t := range tags {
		if t == "" || NormalizeTag(t) != t || (i > 0 && tags[i-1] >= t) {
			return ScopeKey{}, fmt.Errorf("%w: scope key %q is not canonical", ErrInvalidArgument, s)
		}
	}
	return ScopeKey{BankRef: b, Tags: tags}, nil
}

// DecodeScopeTags parses an EncodeScopeTags string back into its tags exactly as encoded (no
// canonicality check; use ParseScopeKey for stored keys).
func DecodeScopeTags(s string) ([]string, error) {
	tags := []string{}
	for i := 0; i < len(s); {
		colon := strings.IndexByte(s[i:], ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: scope key %q: missing length at %d", ErrInvalidArgument, s, i)
		}
		n, err := strconv.Atoi(s[i : i+colon])
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: scope key %q: bad length at %d", ErrInvalidArgument, s, i)
		}
		start := i + colon + 1
		end := start + n
		if end > len(s) {
			return nil, fmt.Errorf("%w: scope key %q: truncated tag at %d", ErrInvalidArgument, s, i)
		}
		tags = append(tags, s[start:end])
		if end < len(s) && s[end] != ',' {
			return nil, fmt.Errorf("%w: scope key %q: missing separator at %d", ErrInvalidArgument, s, end)
		}
		i = end + 1
	}
	return tags, nil
}

// TagSet is one explicit set of tags (a custom observation scope, a consolidate scope or a document's tags).
type TagSet struct {
	// Tags are the set's tags.
	Tags []string `json:"tags"`
}

// ScopeMode decides which scopes a unit consolidates under, from its tags.
type ScopeMode string

// Scope modes, mirroring memory.v1.ObservationScopes.Mode.
const (
	// ScopeModeUnspecified is the zero value (inherit the bank default).
	ScopeModeUnspecified ScopeMode = ""
	// ScopeModeCombined is one scope: the unit's full tag set.
	ScopeModeCombined ScopeMode = "combined"
	// ScopeModeShared is the single untagged scope, whatever the unit's tags.
	ScopeModeShared ScopeMode = "shared"
	// ScopeModePerTag is one scope per tag.
	ScopeModePerTag ScopeMode = "per_tag"
	// ScopeModeAllCombinations is every non-empty subset of the tags, capped by MaxCombinations.
	ScopeModeAllCombinations ScopeMode = "all_combinations"
	// ScopeModeCustom uses the explicit Custom tag sets.
	ScopeModeCustom ScopeMode = "custom"
)

// DefaultMaxCombinations is the ALL_COMBINATIONS fan-out cap when unset.
const DefaultMaxCombinations = 15

// ObservationScopes configures a unit's scope fan-out.
type ObservationScopes struct {
	// Mode picks the fan-out rule.
	Mode ScopeMode `json:"mode,omitempty"`
	// Custom holds the scopes of ScopeModeCustom.
	Custom []TagSet `json:"custom,omitempty"`
	// MaxCombinations caps ALL_COMBINATIONS (default 15); exceeding it fails Retain synchronously.
	MaxCombinations *int `json:"max_combinations,omitempty"`
}

// ScopeInfo summarizes one scope for ListScopes.
type ScopeInfo struct {
	// Scope is the scope's tags.
	Scope TagSet
	// Observations is the number of observations in the scope.
	Observations int64
	// UnconsolidatedUnits is the number of units in the scope still waiting for consolidation.
	UnconsolidatedUnits int64
}
