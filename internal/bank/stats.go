package bank

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
)

// Page-size bounds of ListTags.
const (
	// DefaultPageSize is the page size when a request asks for none.
	DefaultPageSize = 100
	// MaxPageSize caps a requested page size.
	MaxPageSize = 1000
)

// ListTagsRequest mirrors memory.v1.ListTagsRequest.
type ListTagsRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// Source is where tags are counted; unspecified = memories.
	Source domain.TagSource
	// Prefix limits tags to those starting with it; empty = all.
	Prefix string
	// PageSize caps the page; 0 = DefaultPageSize.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// TagPage is one page of a tag listing.
type TagPage struct {
	// Tags are the page's tags with their counts.
	Tags []domain.TagCount
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// GetBankStats returns bank b's counts from indexed COUNT queries (no counter tables); an unknown or tombstoned
// bank is domain.ErrNotFound.
func (s *Service) GetBankStats(ctx context.Context, b domain.BankRef) (domain.BankStats, error) {
	st, err := s.d.DB.Read().BankStats(ctx, b)
	if err != nil {
		return domain.BankStats{}, fmt.Errorf("bank stats: %w", err)
	}
	return st, nil
}

// ListTags returns one page of tag counts. The prefix is normalized like a tag (NFC, lowercase, trimmed), since
// stored tags are; the page token carries the last tag of the previous page.
func (s *Service) ListTags(ctx context.Context, req ListTagsRequest) (TagPage, error) {
	var tok tagToken
	if err := decodeToken(req.PageToken, &tok); err != nil {
		return TagPage{}, err
	}
	source := req.Source
	if source == domain.TagSourceUnspecified {
		source = domain.TagSourceMemories
	}
	size := pageSize(req.PageSize)
	tags, err := s.d.DB.Read().TagCounts(ctx, req.Bank, source, domain.NormalizeTag(req.Prefix), tok.After, size+1)
	if err != nil {
		return TagPage{}, fmt.Errorf("list tags: %w", err)
	}
	page := TagPage{Tags: tags}
	if len(tags) > size {
		page.Tags = tags[:size]
		page.NextPageToken = encodeToken(tagToken{After: tags[size-1].Tag})
	}
	return page, nil
}

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
