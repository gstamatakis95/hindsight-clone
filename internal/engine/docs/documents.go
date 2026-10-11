package docs

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// DocumentAdmin lists, reads and reprocesses documents. Its method set mirrors server.DocumentAdmin with
// the request, page and view types declared here (decision 1302).
type DocumentAdmin interface {
	// ListDocuments returns one page of documents.
	ListDocuments(ctx context.Context, req ListDocumentsRequest) (DocumentPage, error)
	// GetDocument returns one document, with its original text when includeText; unknown =
	// domain.ErrNotFound.
	GetDocument(ctx context.Context, b port.BankRef, id port.DocumentID, includeText bool) (DocumentView, error)
	// ReprocessDocument re-extracts the document from its stored text (resetting curation), replacing its
	// stored overrides when overrides is non-nil, and returns the retain operation.
	ReprocessDocument(
		ctx context.Context, b port.BankRef, id port.DocumentID, overrides *domain.BankConfig,
	) (domain.Operation, error)
}

// DocumentSource lists documents. It is the port.Reader method; port.Reader satisfies it.
type DocumentSource interface {
	// ListDocuments returns up to q.Limit documents matching q, newest first (created_at, then id, both
	// descending), starting strictly after q.After, with UnitCount and UnitsByFactType filled. An unknown
	// or tombstoned bank is domain.ErrNotFound.
	ListDocuments(ctx context.Context, q DocumentQuery) ([]domain.Document, error)
}

// Reprocessor re-runs retain over a stored document; the retain engine (WP-08) owns the retain_document
// payload (decision 1303).
type Reprocessor interface {
	// Reprocess enqueues a retain_document job that re-extracts document id from its stored text,
	// replacing its stored overrides when overrides is non-nil, and returns the operation id. An unknown
	// document is domain.ErrNotFound; a tombstoned bank is domain.ErrBankDeleting.
	Reprocess(
		ctx context.Context, b port.BankRef, id port.DocumentID, overrides *domain.BankConfig,
	) (port.OperationID, error)
}

// DocumentQuery filters DocumentSource.ListDocuments (port.DocumentQuery). Zero fields do not filter.
type DocumentQuery = port.DocumentQuery

// DocumentCursor is a keyset position in a document listing (port.DocumentCursor).
type DocumentCursor = port.DocumentCursor

// ListDocumentsRequest mirrors memory.v1.ListDocumentsRequest; server.ListDocumentsRequest becomes an alias
// of it (decision 1302).
type ListDocumentsRequest struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// Q is a case-insensitive substring filter on the document id; empty = none.
	Q string
	// Tags filters by document tags; empty = none.
	Tags []string
	// TagsMatch is how Tags compare; unspecified = any_strict.
	TagsMatch domain.TagsMatch
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.DocTimeField
	// PageSize caps the page; 0 = the RPC default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// DocumentPage is one page of documents; server.DocumentPage becomes an alias of it (decision 1302).
type DocumentPage struct {
	// Items are the page's documents.
	Items []domain.Document
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// DocumentView is a document as GetDocument returns it; server.DocumentView becomes an alias of it
// (decision 1302).
type DocumentView struct {
	// Document is the stored document.
	Document domain.Document
	// OriginalText is the retained text from the blob store; empty unless asked for.
	OriginalText string
}

// DocumentAdminDeps are the DocumentAdmin's dependencies.
type DocumentAdminDeps struct {
	// Store reads documents and operations.
	Store port.Store
	// Blob reads original texts.
	Blob port.Blob
	// Source lists documents.
	Source DocumentSource
	// Reprocessor enqueues reprocessing.
	Reprocessor Reprocessor
}
