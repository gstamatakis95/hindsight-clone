package curate

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// MemoryLister lists live and archived units with filters and keyset pagination.
type MemoryLister interface {
	// List returns one page of units matching req; no total is computed.
	List(ctx context.Context, req ListMemoriesRequest) (ListMemoriesPage, error)
	// Get returns one unit, live or archived; unknown = domain.ErrNotFound.
	Get(ctx context.Context, b port.BankRef, id port.UnitID) (domain.Unit, error)
	// ObservationHistory returns an observation's versions, newest first.
	ObservationHistory(ctx context.Context, b port.BankRef, id port.UnitID) ([]domain.ObservationVersion, error)
}

// ListMemoriesRequest mirrors memory.v1.ListMemoriesRequest with domain types.
type ListMemoriesRequest struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Q is a case-insensitive text filter; empty = none.
	Q string
	// DocumentID limits units to one document; empty = all.
	DocumentID port.DocumentID
	// EntityID limits units to those attached to an entity (live units only); empty = all.
	EntityID port.EntityID
	// State selects live (unspecified or valid) or archived (invalidated) units.
	State domain.MemoryState
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.TimeField
	// Tags filters by tags; empty = no filter.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// PageSize caps the page; 0 = the RPC default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// ListMemoriesPage is one page of a memory listing.
type ListMemoriesPage struct {
	// Items are the page's units.
	Items []domain.Unit
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}
