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

// UnitSource is the read side of MemoryLister. Its methods are the port.Reader methods proposed in CCR
// WP-13-1 (UnitQuery and UnitCursor move to port with it); port.Reader satisfies it once the CCR lands.
type UnitSource interface {
	// GetUnit returns one unit of b, live (State valid) or archived (State invalidated), with Entities,
	// SourceIDs and, for observations, Stale filled; unknown = domain.ErrNotFound. An unknown or
	// tombstoned bank is domain.ErrNotFound.
	GetUnit(ctx context.Context, b port.BankRef, id port.UnitID) (domain.Unit, error)
	// ListUnits returns up to q.Limit units matching q, newest first (created_at, then id, both
	// descending), starting strictly after q.After. Units are filled like GetUnit. An unknown or tombstoned
	// bank is domain.ErrNotFound.
	ListUnits(ctx context.Context, q UnitQuery) ([]domain.Unit, error)
	// ObservationHistory returns the observation_versions rows of observation id, newest first; an
	// unknown id is domain.ErrNotFound. An unknown or tombstoned bank is domain.ErrNotFound.
	ObservationHistory(ctx context.Context, b port.BankRef, id port.UnitID) ([]domain.ObservationVersion, error)
}

// UnitQuery filters ListUnits. Zero fields do not filter.
type UnitQuery struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Q is a case-insensitive substring filter on the text; empty = none.
	Q string
	// DocumentID limits units to one document.
	DocumentID port.DocumentID
	// EntityID limits units to live units attached to the entity (archived units never match).
	EntityID port.EntityID
	// State selects live (valid), archived (invalidated) or both (unspecified) tables.
	State domain.MemoryState
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at. A unit whose field is
	// null never matches a bound on it.
	TimeField domain.TimeField
	// Tags filters by normalized tags with domain.MatchTags semantics; empty = none.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// After continues a listing: only units strictly after this cursor in the listing order; nil = first.
	After *UnitCursor
	// Limit caps the result (> 0).
	Limit int
}

// UnitCursor is a keyset position in a unit listing.
type UnitCursor struct {
	// CreatedAt is the last listed unit's created_at.
	CreatedAt time.Time `json:"c"`
	// ID is the last listed unit's id.
	ID port.UnitID `json:"i"`
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
	// State selects live (valid), archived (invalidated) or both (unspecified) units.
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
