package memory

import (
	"errors"
	"memd/internal/domain"
	"slices"
	"testing"
)

// TestListMemoriesPaging pages the seeded units two at a time with unnormalized tags: every unit comes once,
// over two pages; malformed requests are INVALID_ARGUMENT and an unknown unit NOT_FOUND.
func TestListMemoriesPaging(t *testing.T) {
	e := newCurateEnv(t)
	s := e.service(nil)
	var got []domain.UnitID
	tok, pages := "", 0
	for {
		page, err := s.ListMemories(e.ctx, ListRequest{
			Bank: testBank, PageSize: 2, PageToken: tok, Tags: domain.TagFilter{Tags: []string{" Team ", "team"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, u := range page.Items {
			got = append(got, u.ID)
		}
		if tok = page.NextPageToken; tok == "" || pages > 3 {
			break
		}
	}
	slices.Sort(got)
	want := []domain.UnitID{idF1, idF2, idO1}
	slices.Sort(want)
	if pages != 2 || !slices.Equal(got, want) {
		t.Errorf("listed %v over %d pages, want %v over 2", got, pages, want)
	}
	for _, req := range []ListRequest{
		{Bank: testBank, PageToken: "!!not-a-token"},
		{Bank: testBank, FactTypes: []domain.FactType{"bogus"}},
		{Bank: testBank, State: "bogus"},
		{Bank: testBank, Tags: domain.TagFilter{Groups: []domain.TagGroup{{Kind: domain.TagGroupAllOf}}}},
	} {
		if _, err := s.ListMemories(e.ctx, req); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("ListMemories(%+v) err = %v, want invalid argument", req, err)
		}
	}
	if _, err := s.GetMemory(e.ctx, testBank, idNope); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetMemory missing err = %v", err)
	}
}

// TestListMemoriesResponseBudget: a page ends early once its memories reach the response size limit, keeping at
// least one, and its token continues right after the last memory kept, so a walk lists every memory once, in the
// same order as an unlimited listing.
func TestListMemoriesResponseBudget(t *testing.T) {
	e := newCurateEnv(t)
	s := e.service(nil)
	all, err := s.ListMemories(e.ctx, ListRequest{Bank: testBank})
	if err != nil || len(all.Items) != 3 || all.NextPageToken != "" {
		t.Fatalf("unlimited listing = %+v, %v; want 3 memories on one page", all, err)
	}
	want := make([]domain.UnitID, len(all.Items))
	for i, u := range all.Items {
		want[i] = u.ID
	}
	for _, limit := range []int{600, 1} { // about one seeded memory's estimate; less than one
		s.d.MaxResponseBytes = limit
		if got := walkMemories(t, s, e); !slices.Equal(got, want) {
			t.Errorf("limit %d: walked %v, want %v, one memory per page", limit, got, want)
		}
	}
}

// walkMemories lists the test bank page by page and returns the ids, failing on a page that does not hold exactly
// one memory.
func walkMemories(t *testing.T, s *Service, e *curateEnv) []domain.UnitID {
	t.Helper()
	var got []domain.UnitID
	for tok, pages := "", 0; pages == 0 || tok != ""; pages++ {
		page, err := s.ListMemories(e.ctx, ListRequest{Bank: testBank, PageToken: tok})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || pages > 3 {
			t.Fatalf("page %d holds %d memories", pages, len(page.Items))
		}
		got, tok = append(got, page.Items[0].ID), page.NextPageToken
	}
	return got
}
