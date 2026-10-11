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
		{Bank: testBank, Types: []domain.FactType{"bogus"}},
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
