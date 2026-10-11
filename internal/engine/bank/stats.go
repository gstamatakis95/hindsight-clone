package bank

import (
	"context"
	"fmt"
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

// StatsSource is the counting side of StatsReader. Its methods are port.Reader methods; port.Reader
// satisfies it.
type StatsSource interface {
	// BankStats returns bank b's counts from indexed COUNT queries; an unknown or tombstoned bank is
	// domain.ErrNotFound.
	BankStats(ctx context.Context, b port.BankRef) (domain.BankStats, error)
	// TagCounts returns up to limit tags of b's source with their counts, ordered by tag, keeping only tags
	// that start with prefix and sort after after ("" = from the start). Source unspecified = memories
	// (live units). An unknown or tombstoned bank is domain.ErrNotFound.
	TagCounts(
		ctx context.Context, b port.BankRef, source domain.TagSource, prefix, after string, limit int,
	) ([]domain.TagCount, error)
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

// statsReader implements StatsReader over a StatsSource.
type statsReader struct{ src StatsSource }

var _ StatsReader = (*statsReader)(nil)

// NewStatsReader returns the StatsReader over src (port.Reader).
func NewStatsReader(src StatsSource) StatsReader { return &statsReader{src: src} }

// Stats returns the source's counts.
func (r *statsReader) Stats(ctx context.Context, b port.BankRef) (domain.BankStats, error) {
	st, err := r.src.BankStats(ctx, b)
	if err != nil {
		return domain.BankStats{}, fmt.Errorf("bank stats: %w", err)
	}
	return st, nil
}

// Tags returns one page of tag counts. The prefix is normalized like a tag (NFC, lowercase, trimmed), since
// stored tags are; the page token carries the last tag of the previous page.
func (r *statsReader) Tags(ctx context.Context, req ListTagsRequest) (TagPage, error) {
	var tok tagToken
	if err := decodeToken(req.PageToken, &tok); err != nil {
		return TagPage{}, err
	}
	source := req.Source
	if source == domain.TagSourceUnspecified {
		source = domain.TagSourceMemories
	}
	size := pageSize(req.PageSize)
	tags, err := r.src.TagCounts(ctx, req.Bank, source, domain.NormalizeTag(req.Prefix), tok.After, size+1)
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
