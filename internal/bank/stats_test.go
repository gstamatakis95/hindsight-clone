package bank

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"slices"
	"testing"
)

// TestListTagsPaging: the prefix is normalized like a tag, pages chain through their tokens to the end, and a
// malformed token is INVALID_ARGUMENT.
func TestListTagsPaging(t *testing.T) {
	e := newEnv(t)
	s := e.svc(DeleteConfig{})
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	e.tx(t, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(e.ctx, b); err != nil {
			return err
		}
		seq, err := tx.Seq(e.ctx, b)
		if err != nil {
			return err
		}
		var units []domain.Unit
		for i, tags := range [][]string{{"a"}, {"team:x"}, {"team:x", "team:y"}, {"team:z"}} {
			units = append(units, domain.Unit{
				ID: pgtest.ID[domain.UnitID]("u" + string(rune('1'+i))), Bank: b, Type: domain.FactTypeWorld,
				Text: "fact", Tags: tags, Seq: seq,
			})
		}
		return tx.Units().Insert(e.ctx, units)
	})
	var got []string
	tok := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging never ends")
		}
		page, err := s.ListTags(e.ctx, ListTagsRequest{Bank: b, Prefix: " TEAM:", PageSize: 2, PageToken: tok})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range page.Tags {
			got = append(got, tc.Tag)
		}
		if tok = page.NextPageToken; tok == "" {
			break
		}
	}
	if want := []string{"team:x", "team:y", "team:z"}; !slices.Equal(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
	if _, err := s.ListTags(e.ctx, ListTagsRequest{Bank: b, PageToken: "!!"}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Errorf("bad token: %v", err)
	}
}
