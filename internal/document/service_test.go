package document_test

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/document"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/store"
	"slices"
	"strings"
	"testing"
)

// TestListDocuments pages newest first with normalized tag filters (any_strict by default) and rejects a
// malformed token.
func TestListDocuments(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	listed := domain.BankRef{Tenant: "acme", Bank: "listed"}
	for i := 1; i <= 6; i++ {
		tags := []string{"a"}
		if i == 6 {
			tags = []string{"z"}
		}
		err := f.store.InTx(ctx, func(tx store.Tx) error { // one transaction each: distinct created_at
			if _, err := tx.Banks().Ensure(ctx, listed); err != nil {
				return err
			}
			return tx.Documents().Upsert(ctx, domain.Document{
				ID: domain.DocumentID(fmt.Sprintf("d%d", i)), Bank: listed, Tags: tags,
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	s := document.New(document.Deps{DB: f.store, Blob: fake.NewBlob(), Clock: f.clock})
	tags := domain.TagFilter{Tags: []string{"B", " A"}}
	got := listAll(t, s, document.ListRequest{Bank: listed, Tags: tags, PageSize: 2})
	if want := []domain.DocumentID{"d5", "d4", "d3", "d2", "d1"}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	if _, err := s.ListDocuments(ctx, document.ListRequest{Bank: listed, PageToken: "!!"}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Fatalf("malformed page token: %v, want ErrInvalidArgument", err)
	}
}

// listAll pages through ListDocuments from req and returns every listed id; it fails after 6 pages.
func listAll(t *testing.T, s *document.Service, req document.ListRequest) []domain.DocumentID {
	t.Helper()
	var got []domain.DocumentID
	for page := 0; page < 6; page++ {
		res, err := s.ListDocuments(context.Background(), req)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, d := range res.Items {
			got = append(got, d.ID)
		}
		if req.PageToken = res.NextPageToken; req.PageToken == "" {
			return got
		}
	}
	t.Fatal("paging never ends")
	return nil
}

// TestGetDocument reads with and without text; an unknown document and a tombstoned bank are NotFound.
func TestGetDocument(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	blob := fake.NewBlob()
	if err := blob.Put(ctx, "k1", strings.NewReader("hello world"), 11, "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	s := document.New(document.Deps{DB: f.store, Blob: blob, Clock: f.clock})
	v, err := s.GetDocument(ctx, testBank, "d1", false)
	if err != nil || v.Document.ID != "d1" || v.OriginalText != "" || v.Document.MemoryCount != 2 {
		t.Fatalf("GetDocument without text = %+v, %v", v, err)
	}
	if v, err = s.GetDocument(ctx, testBank, "d1", true); err != nil || v.OriginalText != "hello world" {
		t.Fatalf("GetDocument with text = %q, %v", v.OriginalText, err)
	}
	if _, err := s.GetDocument(ctx, testBank, "nope", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown document: %v", err)
	}
	tomb := func(tx store.Tx) error { return tx.Banks().MarkDeleting(ctx, testBank, f.clock.Now()) }
	if err := f.store.InTx(ctx, tomb); err != nil {
		t.Fatalf("MarkDeleting: %v", err)
	}
	if _, err := s.GetDocument(ctx, testBank, "d1", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tombstoned bank: %v, want ErrNotFound", err)
	}
}

// TestGetDocumentMissingBlobIsEmptyText: a document whose original text is missing from the blob store
// still reads, with empty text, instead of NOT_FOUND.
func TestGetDocumentMissingBlobIsEmptyText(t *testing.T) {
	f := newFixture(t)
	s := document.New(document.Deps{DB: f.store, Blob: fake.NewBlob(), Clock: f.clock})
	v, err := s.GetDocument(context.Background(), testBank, "d1", true)
	if err != nil || v.Document.ID != "d1" || v.OriginalText != "" {
		t.Fatalf("GetDocument with a missing blob = %+v, %v; want the document with empty text", v, err)
	}
}
