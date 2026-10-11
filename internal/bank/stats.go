package bank

import (
	"context"
	"fmt"
	"memd/internal/domain"
)

// ListTagsRequest mirrors memory.v1.ListTagsRequest.
type ListTagsRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// Source is where tags are counted; unspecified = memories.
	Source domain.TagSource
	// Prefix limits tags to those starting with it; empty = all.
	Prefix string
	// PageSize caps the page (domain.ListPage); 0 = the default.
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
// stored tags are; the page size follows domain.ListPage, and the page token carries the last tag of the
// previous page.
func (s *Service) ListTags(ctx context.Context, req ListTagsRequest) (TagPage, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return TagPage{}, fmt.Errorf("list tags: %w", err)
	}
	var tok tagToken
	if req.PageToken != "" {
		if err := domain.DecodePageToken(req.PageToken, &tok); err != nil {
			return TagPage{}, fmt.Errorf("list tags: %w", err)
		}
	}
	source := req.Source
	if source == domain.TagSourceUnspecified {
		source = domain.TagSourceMemories
	}
	tags, err := s.d.DB.Read().TagCounts(ctx, req.Bank, source, domain.NormalizeTag(req.Prefix), tok.After, size+1)
	if err != nil {
		return TagPage{}, fmt.Errorf("list tags: %w", err)
	}
	page := TagPage{Tags: tags}
	if len(tags) > size {
		page.Tags = tags[:size]
		if page.NextPageToken, err = domain.EncodePageToken(tagToken{After: tags[size-1].Tag}); err != nil {
			return TagPage{}, fmt.Errorf("list tags: %w", err)
		}
	}
	return page, nil
}

// tagToken is the decoded form of a ListTags page token.
type tagToken struct {
	// After is the last tag of the previous page.
	After string `json:"a"`
}
