package docs_test

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/fake"
	"memd/internal/port"
	"slices"
	"strings"
	"testing"
	"time"
)

// stubSource serves ListDocuments from a fixed, newest-first list and records the queries.
type stubSource struct {
	docs    []domain.Document
	queries []docs.DocumentQuery
}

func (s *stubSource) ListDocuments(_ context.Context, q docs.DocumentQuery) ([]domain.Document, error) {
	s.queries = append(s.queries, q)
	start := 0
	if q.After != nil {
		start = slices.IndexFunc(s.docs, func(d domain.Document) bool { return d.ID == q.After.ID }) + 1
	}
	end := min(start+q.Limit, len(s.docs))
	return slices.Clone(s.docs[start:end]), nil
}

// stubReprocessor enqueues a retain_document job like the retain engine would.
type stubReprocessor struct {
	store port.Store
	err   error
}

func (s *stubReprocessor) Reprocess(
	ctx context.Context, b port.BankRef, id port.DocumentID, _ *domain.BankConfig,
) (port.OperationID, error) {
	if s.err != nil {
		return "", s.err
	}
	var op port.OperationID
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		var err error
		op, _, err = tx.Queue().Enqueue(ctx, port.Job{
			Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:" + string(id),
			Payload: []byte(`{}`),
		})
		return err
	})
	return op, err
}

func TestDocumentAdminList(t *testing.T) {
	ctx := context.Background()
	src := &stubSource{}
	for i := range 5 {
		src.docs = append(src.docs, domain.Document{
			ID: port.DocumentID(fmt.Sprintf("d%d", 5-i)), Bank: testBank,
			CreatedAt: time.Date(2026, 1, 5-i, 0, 0, 0, 0, time.UTC),
		})
	}
	a := docs.NewDocumentAdmin(docs.DocumentAdminDeps{Source: src})

	var got []port.DocumentID
	token := ""
	for page := 0; ; page++ {
		res, err := a.ListDocuments(ctx, docs.ListDocumentsRequest{
			Bank: testBank, Tags: []string{"B", "a"}, PageSize: 2, PageToken: token,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, d := range res.Items {
			got = append(got, d.ID)
		}
		if token = res.NextPageToken; token == "" {
			break
		}
		if page > 5 {
			t.Fatal("paging never ends")
		}
	}
	if want := []port.DocumentID{"d5", "d4", "d3", "d2", "d1"}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	q := src.queries[0]
	if q.Limit != 3 || q.TagsMatch != domain.TagsMatchAnyStrict || !slices.Equal(q.Tags, []string{"a", "b"}) {
		t.Fatalf("first query = %+v; want limit 3, any_strict, normalized tags", q)
	}
	if _, err := a.ListDocuments(ctx, docs.ListDocumentsRequest{Bank: testBank, PageToken: "!!"}); !errors.Is(
		err, domain.ErrInvalidArgument) {
		t.Fatalf("bad token: %v, want ErrInvalidArgument", err)
	}
	res, err := a.ListDocuments(ctx, docs.ListDocumentsRequest{Bank: testBank, PageSize: 5000})
	if err != nil || len(res.Items) != 5 || src.queries[len(src.queries)-1].Limit != docs.MaxDocumentPageSize+1 {
		t.Fatalf("oversized page: %d items, %v", len(res.Items), err)
	}
}

func TestDocumentAdminGetAndReprocess(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blob := fake.NewBlob()
	if err := blob.Put(ctx, "k1", strings.NewReader("hello world"), 11, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rp := &stubReprocessor{store: f.store}
	a := docs.NewDocumentAdmin(docs.DocumentAdminDeps{Store: f.store, Blob: blob, Reprocessor: rp})

	v, err := a.GetDocument(ctx, testBank, "d1", false)
	if err != nil || v.Document.ID != "d1" || v.OriginalText != "" || v.Document.UnitCount != 2 {
		t.Fatalf("GetDocument without text = %+v, %v", v, err)
	}
	if v, err = a.GetDocument(ctx, testBank, "d1", true); err != nil || v.OriginalText != "hello world" {
		t.Fatalf("GetDocument with text = %q, %v", v.OriginalText, err)
	}
	if _, err := a.GetDocument(ctx, testBank, "nope", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown document: %v", err)
	}

	op, err := a.ReprocessDocument(ctx, testBank, "d1", nil)
	if err != nil || op.Kind != domain.OperationKindRetainDocument || op.Status != domain.OperationStatusPending {
		t.Fatalf("ReprocessDocument = %+v, %v", op, err)
	}
	rp.err = fmt.Errorf("retain says: %w", domain.ErrNotFound)
	if _, err := a.ReprocessDocument(ctx, testBank, "nope", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reprocess error not passed through: %v", err)
	}

	tomb := func(tx port.Tx) error { return tx.Banks().MarkDeleting(ctx, testBank, f.clock.Now()) }
	if err := f.store.InTx(ctx, tomb); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := a.GetDocument(ctx, testBank, "d1", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tombstoned bank: %v, want ErrNotFound", err)
	}
}

// TestGetDocumentMissingBlobIsEmptyText: a document whose original text is missing from the blob store
// still reads, with empty text, instead of NOT_FOUND.
func TestGetDocumentMissingBlobIsEmptyText(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := docs.NewDocumentAdmin(docs.DocumentAdminDeps{Store: f.store, Blob: fake.NewBlob()})
	v, err := a.GetDocument(ctx, testBank, "d1", true)
	if err != nil || v.Document.ID != "d1" || v.OriginalText != "" {
		t.Fatalf("GetDocument with a missing blob = %+v, %v; want the document with empty text", v, err)
	}
}
