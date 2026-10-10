package mentalmodel

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// PageHit is one SearchPages result; an alias of domain.PageHit.
type PageHit = domain.PageHit

// PageSearcher ranks whole pages by fused full-text and semantic scores, no rerank.
type PageSearcher interface {
	// Search returns at most req.Limit pages, best first.
	Search(ctx context.Context, req SearchPagesRequest) ([]PageHit, error)
}

// SearchPagesRequest mirrors memory.v1.SearchPagesRequest.
type SearchPagesRequest struct {
	// Bank is the bank searched.
	Bank port.BankRef
	// Query is the search text.
	Query string
	// FolderPrefix limits pages to a folder subtree; empty = all.
	FolderPrefix string
	// Limit caps the hits; 0 = the RPC default.
	Limit int
}
