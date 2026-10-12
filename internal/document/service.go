package document

import (
	"context"
	"errors"
	"fmt"
	"io"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// ListDocuments returns one keyset page of documents, newest first (created_at, then id). The page size follows
// domain.ListPage; the tag filter must be flat (domain.ErrInvalidArgument otherwise) and its match defaults to
// any_strict. It asks the store for one row more than the page to learn whether another
// page follows, and encodes the last listed document as the opaque next page token (a malformed token is
// domain.ErrInvalidArgument). An unknown or tombstoned bank is domain.ErrNotFound.
func (s *Service) ListDocuments(ctx context.Context, req ListRequest) (Page, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list documents: %w", err)
	}
	if err := req.Tags.RequireFlat(); err != nil {
		return Page{}, fmt.Errorf("list documents: %w", err)
	}
	after, err := decodeCursor(req.PageToken)
	if err != nil {
		return Page{}, err
	}
	match := req.Tags.Match
	if match == domain.TagsMatchUnspecified {
		match = domain.TagsMatchAnyStrict
	}
	q := store.DocumentQuery{
		Bank: req.Bank, DocumentIDContains: req.DocumentIDContains, Tags: domain.NormalizeTags(req.Tags.Tags),
		TagsMatch: match,
		Start:     req.Start, End: req.End, TimeField: req.TimeField, After: after, Limit: size + 1,
	}
	if len(req.Tags.Tags) == 0 {
		q.Tags = nil
	}
	docs, err := s.d.DB.Read().ListDocuments(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list documents: %w", err)
	}
	if len(docs) <= size {
		return Page{Items: docs}, nil
	}
	docs = docs[:size]
	last := docs[size-1]
	token, err := domain.EncodePageToken(store.DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	if err != nil {
		return Page{}, fmt.Errorf("list documents: %w", err)
	}
	return Page{Items: docs, NextPageToken: token}, nil
}

// GetDocument reads the document in a short read-only transaction (unknown = domain.ErrNotFound, and a
// tombstoned bank reads as domain.ErrNotFound) and, when includeText, its original text from the blob store. A
// missing blob returns the document with empty text; a read failure is domain.ErrUnavailable.
func (s *Service) GetDocument(
	ctx context.Context, b domain.BankRef, id domain.DocumentID, includeText bool,
) (View, error) {
	var d domain.Document
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		d = domain.Document{}
		if err := liveBank(ctx, tx, b); err != nil {
			return err
		}
		var err error
		d, err = tx.Documents().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return View{}, err
	}
	view := View{Document: d}
	if !includeText || d.BlobKey == "" {
		return view, nil
	}
	text, err := readBlob(ctx, s.d.Blob, d.BlobKey)
	if errors.Is(err, domain.ErrNotFound) {
		return view, nil // the blob is gone (or not written yet): the document stays readable without text
	}
	if err != nil {
		return View{}, err
	}
	view.OriginalText = text
	return view, nil
}

// ReprocessDocument re-extracts the document from its stored text (resetting curation), replacing its stored
// overrides when overrides is non-nil: it enqueues a forced retain_document job (reprocess) and returns that
// operation, read in a second short transaction. An unknown document is domain.ErrNotFound, a document without
// stored text domain.ErrFailedPrecondition and a tombstoned bank domain.ErrBankDeleting.
func (s *Service) ReprocessDocument(
	ctx context.Context, b domain.BankRef, id domain.DocumentID, overrides *domain.BankConfig,
) (domain.Operation, error) {
	opID, err := s.reprocess(ctx, b, id, overrides)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("reprocess document %s: %w", id, err)
	}
	var op domain.Operation
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(ctx, b, opID)
		return err
	})
	return op, err
}

// liveBank fails with domain.ErrNotFound when b is unknown or tombstoned (reads of a deleting bank).
func liveBank(ctx context.Context, tx store.Tx, b domain.BankRef) error {
	bank, err := tx.Banks().Get(ctx, b)
	if err != nil {
		return err
	}
	if bank.Deleting() {
		return fmt.Errorf("%w %s is being deleted", domain.ErrBankNotFound, b)
	}
	return nil
}

// readBlob reads the whole object at key.
func readBlob(ctx context.Context, blob blob.Store, key string) (string, error) {
	rc, err := blob.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("%w: read document text %s: %w", domain.ErrUnavailable, key, err)
	}
	return string(data), nil
}

// decodeCursor parses a page token; "" is the first page and a malformed token is
// domain.ErrInvalidArgument.
func decodeCursor(token string) (*store.DocumentCursor, error) {
	if token == "" {
		return nil, nil
	}
	var c store.DocumentCursor
	if err := domain.DecodePageToken(token, &c); err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	if c.ID == "" {
		return nil, domain.InvalidField("page_token", "page_token has no position")
	}
	return &c, nil
}

// Deps are the Service's dependencies; every field is required.
type Deps struct {
	// DB reads and writes documents, their units and the job queue in short transactions.
	DB store.DB
	// Blob reads documents' original texts.
	Blob blob.Store
	// Clock dates the retag job's progress report.
	Clock clock.Clock
}

// Service serves DocumentService (listing, reading, retagging and reprocessing documents) and runs the
// retag_document job.
type Service struct {
	d Deps
}

// New returns the Service over d.
func New(d Deps) *Service {
	return &Service{d: d}
}

// ListRequest mirrors memory.v1.ListDocumentsRequest.
type ListRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// DocumentIDContains is a case-insensitive substring filter on the document id; empty = none.
	DocumentIDContains string
	// Tags filters by document tags, flat form only; empty tags = none, unset match = any_strict.
	Tags domain.TagFilter
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.DocumentTimeField
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// Page is one page of documents.
type Page struct {
	// Items are the page's documents.
	Items []domain.Document
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// View is a document as GetDocument returns it.
type View struct {
	// Document is the stored document.
	Document domain.Document
	// OriginalText is the retained text from the blob store; empty unless asked for.
	OriginalText string
}
