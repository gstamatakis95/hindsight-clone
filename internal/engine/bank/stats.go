package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// StatsReader answers bank stats and tag listings with indexed COUNT queries (no counter tables in v1).
type StatsReader interface {
	// Stats returns bank b's counts; an unknown bank is domain.ErrNotFound.
	Stats(ctx context.Context, b port.BankRef) (domain.BankStats, error)
	// Tags lists tags with counts from the requested source, one page at a time.
	Tags(ctx context.Context, req ListTagsRequest) (TagPage, error)
}

// ListTagsRequest mirrors memory.v1.ListTagsRequest.
type ListTagsRequest struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// Source is where tags are counted; unspecified = memories.
	Source domain.TagSource
	// Prefix limits tags to those starting with it; empty = all.
	Prefix string
	// PageSize caps the page; 0 = the RPC default.
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
