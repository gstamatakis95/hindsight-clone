package bank

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
)

// Page-size bounds shared by the bank listings.
const (
	// DefaultPageSize is the page size when a request asks for none.
	DefaultPageSize = 100
	// MaxPageSize caps a requested page size.
	MaxPageSize = 1000
)

// pageSize clamps a requested page size to (0, MaxPageSize]; 0 or less = DefaultPageSize.
func pageSize(n int) int {
	switch {
	case n <= 0:
		return DefaultPageSize
	case n > MaxPageSize:
		return MaxPageSize
	default:
		return n
	}
}

// tagToken is the decoded form of a ListTags page token.
type tagToken struct {
	// After is the last tag of the previous page.
	After string `json:"a"`
}

// encodeToken returns v as an opaque, URL-safe page token.
func encodeToken(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeToken decodes a page token into v; empty leaves v unchanged, a malformed one is
// domain.ErrInvalidArgument.
func decodeToken(tok string, v any) error {
	if tok == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err == nil {
		err = json.Unmarshal(raw, v)
	}
	if err != nil {
		return fmt.Errorf("%w: malformed page token: %w", domain.ErrInvalidArgument, err)
	}
	return nil
}
