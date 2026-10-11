package document

import (
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

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
	// Q is a case-insensitive substring filter on the document id; empty = none.
	Q string
	// Tags filters by document tags, flat form only; empty tags = none, unset match = any_strict.
	Tags domain.TagFilter
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.DocTimeField
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
