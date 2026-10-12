package memory

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// ListRequest mirrors memory.v1.ListMemoriesRequest with domain types.
type ListRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// FactTypes limits fact types; empty = all.
	FactTypes []domain.FactType
	// TextContains is a case-insensitive substring of the unit text; empty = none.
	TextContains string
	// DocumentID limits units to one document; empty = all.
	DocumentID domain.DocumentID
	// EntityID limits units to those attached to an entity (live units only); empty = all.
	EntityID domain.EntityID
	// State selects live (valid), archived (invalidated) or both (unspecified) units.
	State domain.MemoryState
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.TimeField
	// Tags filters by tags, flat form only; empty tags = no filter, unset match = any.
	Tags domain.TagFilter
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// Page is one page of a memory listing.
type Page struct {
	// Items are the page's units.
	Items []domain.Unit
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListMemories validates req, decodes its page token and reads one page plus one unit to know whether more
// follow. Units come newest first (created_at, then id); no total is computed. The page ends early once its units
// reach the response size limit (domain.FitBytes over Deps.MaxResponseBytes), with a token after the last one kept.
func (s *Service) ListMemories(ctx context.Context, req ListRequest) (Page, error) {
	q, err := unitQuery(req)
	if err != nil {
		return Page{}, err
	}
	size := q.Limit
	q.Limit = size + 1
	units, err := s.d.DB.Read().ListUnits(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list memories: %w", err)
	}
	keep := domain.FitBytes(units[:min(len(units), size)], s.maxResponseBytes(), unitBytes)
	page := Page{Items: units[:keep]}
	if len(units) > keep {
		last := page.Items[keep-1]
		page.NextPageToken, err = domain.EncodePageToken(store.UnitCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return Page{}, fmt.Errorf("list memories: %w", err)
		}
	}
	return page, nil
}

// unitBytes estimates the encoded size of a listed unit from its variable-length fields.
func unitBytes(u domain.Unit) int {
	n := len(u.Text) + len(u.Context) + len(u.DocumentID) + len(u.ChunkID) + 40*len(u.SourceIDs)
	for k, v := range u.Metadata {
		n += len(k) + len(v)
	}
	for _, t := range u.Tags {
		n += len(t)
	}
	for _, e := range u.Entities {
		n += len(e)
	}
	return n
}

// unitQuery turns a request into a UnitQuery, validating enums, the page size (domain.ListPage) and the flat tag
// filter, and decoding the page token.
func unitQuery(req ListRequest) (store.UnitQuery, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return store.UnitQuery{}, fmt.Errorf("list memories: %w", err)
	}
	if err := req.Tags.RequireFlat(); err != nil {
		return store.UnitQuery{}, fmt.Errorf("list memories: %w", err)
	}
	for _, t := range req.FactTypes {
		if !knownFactType(t) {
			return store.UnitQuery{}, fmt.Errorf("%w: unknown fact type %q", domain.ErrInvalidArgument, t)
		}
	}
	switch req.State {
	case domain.MemoryStateUnspecified, domain.MemoryStateValid, domain.MemoryStateInvalidated:
	default:
		return store.UnitQuery{}, fmt.Errorf("%w: unknown memory state %q", domain.ErrInvalidArgument, req.State)
	}
	q := store.UnitQuery{
		Bank: req.Bank, Types: req.FactTypes, TextContains: req.TextContains, DocumentID: req.DocumentID,
		EntityID: req.EntityID, State: req.State, Start: req.Start, End: req.End, TimeField: req.TimeField,
		Tags: domain.NormalizeTags(req.Tags.Tags), TagsMatch: req.Tags.Match, Limit: size,
	}
	if req.PageToken != "" {
		var c store.UnitCursor
		if err := domain.DecodePageToken(req.PageToken, &c); err != nil {
			return store.UnitQuery{}, fmt.Errorf("list memories: %w", err)
		}
		q.After = &c
	}
	return q, nil
}

// knownFactType reports whether t is a concrete fact type.
func knownFactType(t domain.FactType) bool {
	return t == domain.FactTypeWorld || t == domain.FactTypeExperience || t == domain.FactTypeObservation
}

// GetMemory returns one unit, live or archived; unknown = domain.ErrNotFound. An unknown or tombstoned bank is
// domain.ErrNotFound.
func (s *Service) GetMemory(ctx context.Context, b domain.BankRef, id domain.UnitID) (domain.Unit, error) {
	u, err := s.d.DB.Read().GetUnit(ctx, b, id)
	if err != nil {
		return domain.Unit{}, fmt.Errorf("get memory %s: %w", id, err)
	}
	return u, nil
}
