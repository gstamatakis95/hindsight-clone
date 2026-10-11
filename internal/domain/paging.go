package domain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// PageBounds are the size rules of one kind of listing: a request's size 0 means Default, a size above Max is
// clamped to Max, and a negative size is invalid.
type PageBounds struct {
	// Field is the request field the size comes from, named in the error of a negative size.
	Field string
	// Default is the size of a request that sets none.
	Default int
	// Max caps a requested size.
	Max int
}

// The paging convention of the API, stated once.
var (
	// ListPage bounds page_size on every List RPC: 0 = 100, at most 1000.
	ListPage = PageBounds{Field: "page_size", Default: 100, Max: 1000}
	// SearchResults bounds max_results on SearchPages: 0 = 10, at most 100.
	SearchResults = PageBounds{Field: "max_results", Default: 10, Max: 100}
)

// Size applies b to a requested size n: 0 is b.Default, above b.Max is b.Max, and a negative n is an
// ErrInvalidArgument naming b.Field.
func (b PageBounds) Size(n int) (int, error) {
	switch {
	case n < 0:
		return 0, InvalidField(b.Field, "%s %d must not be negative", b.Field, n)
	case n == 0:
		return b.Default, nil
	default:
		return min(n, b.Max), nil
	}
}

// EncodePageToken returns the opaque page token of a keyset position v: base64url of its JSON.
func EncodePageToken(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodePageToken reads a token made by EncodePageToken into v; a malformed one is an ErrInvalidArgument naming
// page_token.
func DecodePageToken(token string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		err = json.Unmarshal(raw, v)
	}
	if err != nil {
		return InvalidField("page_token", "malformed page token: %v", err)
	}
	return nil
}
