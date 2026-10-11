package document

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
)

// Page sizes of ListDocuments.
const (
	// DefaultPageSize applies when the request leaves page_size at 0.
	DefaultPageSize = 100
	// MaxPageSize caps page_size.
	MaxPageSize = 1000
)

// ListDocuments returns one keyset page of documents, newest first (created_at, then id). page_size 0 is
// DefaultPageSize, larger values are capped at MaxPageSize and a negative size is domain.ErrInvalidArgument;
// tags_match defaults to any_strict. It asks the store for one row more than the page to learn whether another
// page follows, and encodes the last listed document as the opaque next page token (a malformed token is
// domain.ErrInvalidArgument). An unknown or tombstoned bank is domain.ErrNotFound.
func (s *Service) ListDocuments(ctx context.Context, req ListRequest) (Page, error) {
	size, err := pageSize(req.PageSize)
	if err != nil {
		return Page{}, err
	}
	after, err := decodeCursor(req.PageToken)
	if err != nil {
		return Page{}, err
	}
	match := req.TagsMatch
	if match == domain.TagsMatchUnspecified {
		match = domain.TagsMatchAnyStrict
	}
	q := store.DocumentQuery{
		Bank: req.Bank, Q: req.Q, Tags: domain.NormalizeTags(req.Tags), TagsMatch: match,
		Start: req.Start, End: req.End, TimeField: req.TimeField, After: after, Limit: size + 1,
	}
	if len(req.Tags) == 0 {
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
	token, err := encodeCursor(store.DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	if err != nil {
		return Page{}, err
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
		return fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
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

// pageSize applies the default and the maximum; a negative size is domain.ErrInvalidArgument.
func pageSize(n int) (int, error) {
	switch {
	case n < 0:
		return 0, fmt.Errorf("%w: page_size %d is negative", domain.ErrInvalidArgument, n)
	case n == 0:
		return DefaultPageSize, nil
	case n > MaxPageSize:
		return MaxPageSize, nil
	default:
		return n, nil
	}
}

// encodeCursor turns a keyset position into an opaque page token.
func encodeCursor(c store.DocumentCursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor parses a page token; "" is the first page and a malformed token is
// domain.ErrInvalidArgument.
func decodeCursor(token string) (*store.DocumentCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: page_token: %w", domain.ErrInvalidArgument, err)
	}
	var c store.DocumentCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%w: page_token: %w", domain.ErrInvalidArgument, err)
	}
	if c.ID == "" {
		return nil, fmt.Errorf("%w: page_token has no position", domain.ErrInvalidArgument)
	}
	return &c, nil
}
