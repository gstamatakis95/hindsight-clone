package observation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
)

// Page sizes of ListObservationScopes.
const (
	// defaultPageSize is the page size when a request sets none.
	defaultPageSize = 100
	// maxPageSize caps a requested page size.
	maxPageSize = 1000
)

// ScopePage is one page of observation scopes.
type ScopePage struct {
	// Items are the page's scopes with their observation counts.
	Items []domain.ScopeInfo
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListObservationScopes returns one page of the bank's observation scopes (every scope that has held an
// observation) with their counts, ordered by the scope's canonical key. Page size 0 is 100 and larger sizes
// are capped at 1000; the page token is an opaque keyset cursor and a malformed one is
// domain.ErrInvalidArgument. An unknown or tombstoned bank is domain.ErrNotFound.
func (s *Service) ListObservationScopes(
	ctx context.Context, b domain.BankRef, size int, token string,
) (ScopePage, error) {
	var after string
	if token != "" {
		if err := decodeToken(token, &after); err != nil {
			return ScopePage{}, err
		}
	}
	size = pageSize(size)
	items, err := s.d.DB.Read().ListScopes(ctx, b, after, size+1)
	if err != nil {
		return ScopePage{}, fmt.Errorf("list scopes: %w", err)
	}
	page := ScopePage{Items: items}
	if len(items) > size {
		page.Items = items[:size]
		if page.NextPageToken, err = encodeToken(scopeCursor(b, page.Items[size-1])); err != nil {
			return ScopePage{}, err
		}
	}
	return page, nil
}

// scopeCursor is the keyset cursor after scope s: its canonical key, except that the shared scope's key ""
// would restart the listing, so it maps to "0", which sorts before every tagged key (decision 1560).
func scopeCursor(b domain.BankRef, s domain.ScopeInfo) string {
	if key := domain.NewScopeKey(b, s.Scope.Tags).String(); key != "" {
		return key
	}
	return "0"
}

// pageSize applies the default and the cap to a requested page size.
func pageSize(n int) int {
	switch {
	case n <= 0:
		return defaultPageSize
	case n > maxPageSize:
		return maxPageSize
	default:
		return n
	}
}

// encodeToken returns the opaque page token of a keyset position.
func encodeToken(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeToken reads a page token produced by encodeToken; a malformed one is domain.ErrInvalidArgument.
func decodeToken(tok string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return fmt.Errorf("%w: malformed page token: %w", domain.ErrInvalidArgument, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: malformed page token: %w", domain.ErrInvalidArgument, err)
	}
	return nil
}
