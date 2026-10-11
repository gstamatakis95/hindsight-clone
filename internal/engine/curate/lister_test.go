package curate

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"testing"
	"time"
)

// unitSourceStub serves a fixed unit list (newest first) with keyset paging, recording the last query.
type unitSourceStub struct {
	units []domain.Unit
	last  UnitQuery
}

// GetUnit returns the unit with id.
func (s *unitSourceStub) GetUnit(_ context.Context, _ port.BankRef, id port.UnitID) (domain.Unit, error) {
	for _, u := range s.units {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.Unit{}, fmt.Errorf("%w: unit %s", domain.ErrNotFound, id)
}

// ListUnits pages s.units, which are already in listing order.
func (s *unitSourceStub) ListUnits(_ context.Context, q UnitQuery) ([]domain.Unit, error) {
	s.last = q
	start := 0
	if q.After != nil {
		start = slices.IndexFunc(s.units, func(u domain.Unit) bool { return u.ID == q.After.ID }) + 1
	}
	end := min(start+q.Limit, len(s.units))
	return slices.Clone(s.units[start:end]), nil
}

// ObservationHistory returns one version for o1 and not-found otherwise.
func (s *unitSourceStub) ObservationHistory(
	_ context.Context, _ port.BankRef, id port.UnitID,
) ([]domain.ObservationVersion, error) {
	if id != "o1" {
		return nil, fmt.Errorf("%w: observation %s", domain.ErrNotFound, id)
	}
	return []domain.ObservationVersion{{Version: 1, Text: "v1"}}, nil
}

// listAll pages through l two units at a time, checking each query the source saw.
func listAll(t *testing.T, l MemoryLister, src *unitSourceStub) ([]port.UnitID, int) {
	t.Helper()
	var got []port.UnitID
	tok, pages := "", 0
	for {
		page, err := l.List(context.Background(), ListMemoriesRequest{
			Bank: testBank, PageSize: 2, PageToken: tok, Tags: []string{" Team ", "team"},
		})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, u := range page.Items {
			got = append(got, u.ID)
		}
		if !slices.Equal(src.last.Tags, []string{"team"}) || src.last.Limit != 3 {
			t.Errorf("query = %+v, want normalized tags and limit page+1", src.last)
		}
		if tok = page.NextPageToken; tok == "" {
			return got, pages
		}
	}
}

func TestListerPaging(t *testing.T) {
	ctx := context.Background()
	src := &unitSourceStub{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 5; i >= 1; i-- {
		src.units = append(src.units, domain.Unit{ID: port.UnitID(fmt.Sprintf("u%d", i)), CreatedAt: base.Add(
			time.Duration(i) * time.Hour)})
	}
	l := NewMemoryLister(src)
	got, pages := listAll(t, l, src)
	if pages != 3 || !slices.Equal(got, []port.UnitID{"u5", "u4", "u3", "u2", "u1"}) {
		t.Errorf("listed %v over %d pages", got, pages)
	}

	bad := []ListMemoriesRequest{
		{Bank: testBank, PageToken: "!!not-a-token"},
		{Bank: testBank, Types: []domain.FactType{"bogus"}},
		{Bank: testBank, State: "bogus"},
	}
	for _, req := range bad {
		if _, err := l.List(ctx, req); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("List(%+v) err = %v, want invalid argument", req, err)
		}
	}
	if _, err := l.List(ctx, ListMemoriesRequest{Bank: testBank, PageSize: 5000}); err != nil ||
		src.last.Limit != MaxPageSize+1 {
		t.Errorf("page size not capped: limit %d, err %v", src.last.Limit, err)
	}
	if _, err := l.Get(ctx, testBank, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get missing err = %v", err)
	}
	if vs, err := l.ObservationHistory(ctx, testBank, "o1"); err != nil || len(vs) != 1 {
		t.Errorf("history = %v, %v", vs, err)
	}
}
