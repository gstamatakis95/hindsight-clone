package docs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"
)

// Page sizes of ListDocuments.
const (
	// DefaultDocumentPageSize applies when the request leaves page_size at 0.
	DefaultDocumentPageSize = 100
	// MaxDocumentPageSize caps page_size.
	MaxDocumentPageSize = 1000
)

// documentAdmin implements DocumentAdmin.
type documentAdmin struct {
	d DocumentAdminDeps
}

var _ DocumentAdmin = (*documentAdmin)(nil)

// NewDocumentAdmin returns the DocumentAdmin over d.
func NewDocumentAdmin(d DocumentAdminDeps) DocumentAdmin {
	return &documentAdmin{d: d}
}

// ListDocuments returns one keyset page: it asks the source for one row more than the page to learn
// whether another page follows, and encodes the last listed document as the next page token.
func (a *documentAdmin) ListDocuments(ctx context.Context, req ListDocumentsRequest) (DocumentPage, error) {
	size, err := pageSize(req.PageSize)
	if err != nil {
		return DocumentPage{}, err
	}
	after, err := decodeCursor(req.PageToken)
	if err != nil {
		return DocumentPage{}, err
	}
	match := req.TagsMatch
	if match == domain.TagsMatchUnspecified {
		match = domain.TagsMatchAnyStrict
	}
	q := DocumentQuery{
		Bank: req.Bank, Q: req.Q, Tags: domain.NormalizeTags(req.Tags), TagsMatch: match,
		Start: req.Start, End: req.End, TimeField: req.TimeField, After: after, Limit: size + 1,
	}
	if len(req.Tags) == 0 {
		q.Tags = nil
	}
	docs, err := a.d.Source.ListDocuments(ctx, q)
	if err != nil {
		return DocumentPage{}, fmt.Errorf("list documents: %w", err)
	}
	if len(docs) <= size {
		return DocumentPage{Items: docs}, nil
	}
	docs = docs[:size]
	last := docs[size-1]
	token, err := encodeCursor(DocumentCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	if err != nil {
		return DocumentPage{}, err
	}
	return DocumentPage{Items: docs, NextPageToken: token}, nil
}

// GetDocument reads the document in a short read-only transaction (a tombstoned bank reads as
// domain.ErrNotFound) and, when asked, its original text from the blob store.
func (a *documentAdmin) GetDocument(
	ctx context.Context, b port.BankRef, id port.DocumentID, includeText bool,
) (DocumentView, error) {
	var d domain.Document
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error {
		d = domain.Document{}
		if err := liveBank(ctx, tx, b); err != nil {
			return err
		}
		var err error
		d, err = tx.Documents().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return DocumentView{}, err
	}
	view := DocumentView{Document: d}
	if !includeText || d.BlobKey == "" {
		return view, nil
	}
	text, err := readBlob(ctx, a.d.Blob, d.BlobKey)
	if errors.Is(err, domain.ErrNotFound) {
		return view, nil // the blob is gone (or not written yet): the document stays readable without text
	}
	if err != nil {
		return DocumentView{}, err
	}
	view.OriginalText = text
	return view, nil
}

// ReprocessDocument hands the document to the retain engine and returns the operation it enqueued.
func (a *documentAdmin) ReprocessDocument(
	ctx context.Context, b port.BankRef, id port.DocumentID, overrides *domain.BankConfig,
) (domain.Operation, error) {
	opID, err := a.d.Reprocessor.Reprocess(ctx, b, id, overrides)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("reprocess document %s: %w", id, err)
	}
	var op domain.Operation
	err = a.d.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(ctx, b, opID)
		return err
	})
	return op, err
}

// liveBank fails with domain.ErrNotFound when b is unknown or tombstoned (reads of a deleting bank).
func liveBank(ctx context.Context, tx port.Tx, b port.BankRef) error {
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
func readBlob(ctx context.Context, blob port.Blob, key string) (string, error) {
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
		return DefaultDocumentPageSize, nil
	case n > MaxDocumentPageSize:
		return MaxDocumentPageSize, nil
	default:
		return n, nil
	}
}

// encodeCursor turns a keyset position into an opaque page token.
func encodeCursor(c DocumentCursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeCursor parses a page token; "" is the first page and a malformed token is
// domain.ErrInvalidArgument.
func decodeCursor(token string) (*DocumentCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("%w: page_token: %w", domain.ErrInvalidArgument, err)
	}
	var c DocumentCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%w: page_token: %w", domain.ErrInvalidArgument, err)
	}
	if c.ID == "" {
		return nil, fmt.Errorf("%w: page_token has no position", domain.ErrInvalidArgument)
	}
	return &c, nil
}
