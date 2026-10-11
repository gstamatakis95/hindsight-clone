package observation

import (
	"context"
	"fmt"
	"memd/internal/domain"
)

// ScopePage is one page of observation scopes.
type ScopePage struct {
	// Items are the page's scopes with their observation counts.
	Items []domain.ScopeInfo
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListObservationScopes returns one page of the bank's observation scopes (every scope that has held an
// observation) with their counts, ordered by the scope's canonical key. The page size follows domain.ListPage;
// the page token is an opaque keyset cursor and a malformed one is domain.ErrInvalidArgument. An unknown or
// tombstoned bank is domain.ErrNotFound.
func (s *Service) ListObservationScopes(
	ctx context.Context, b domain.BankRef, size int, token string,
) (ScopePage, error) {
	size, err := domain.ListPage.Size(size)
	if err != nil {
		return ScopePage{}, fmt.Errorf("list scopes: %w", err)
	}
	var after string
	if token != "" {
		if err := domain.DecodePageToken(token, &after); err != nil {
			return ScopePage{}, fmt.Errorf("list scopes: %w", err)
		}
	}
	items, err := s.d.DB.Read().ListObservationScopes(ctx, b, after, size+1)
	if err != nil {
		return ScopePage{}, fmt.Errorf("list scopes: %w", err)
	}
	page := ScopePage{Items: items}
	if len(items) > size {
		page.Items = items[:size]
		if page.NextPageToken, err = domain.EncodePageToken(scopeCursor(b, page.Items[size-1])); err != nil {
			return ScopePage{}, fmt.Errorf("list scopes: %w", err)
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
