package bank

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
)

func TestTagsPaging(t *testing.T) {
	src := &statsStub{tags: []domain.TagCount{
		{Tag: "a", Count: 1}, {Tag: "team:x", Count: 2}, {Tag: "team:y", Count: 3}, {Tag: "team:z", Count: 4},
	}}
	r := NewStatsReader(src)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	var got []string
	tok := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("paging never ends")
		}
		page, err := r.Tags(t.Context(), ListTagsRequest{Bank: b, Prefix: "TEAM:", PageSize: 2, PageToken: tok})
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
	if want := []string{"team:x", "team:y", "team:z"}; len(got) != 3 || got[0] != want[0] || got[2] != want[2] {
		t.Errorf("tags = %v, want %v", got, want)
	}
	if src.calls[0] != "memories|team:||3" {
		t.Errorf("first source call = %q (default source memories, normalized prefix, limit+1)", src.calls[0])
	}
	if _, err := r.Tags(t.Context(), ListTagsRequest{Bank: b, PageToken: "!!"}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Errorf("bad token: %v", err)
	}
	if _, err := r.Tags(t.Context(), ListTagsRequest{Bank: b, PageSize: 5000}); err != nil ||
		src.calls[len(src.calls)-1] != "memories|||1001" {
		t.Errorf("page size not clamped: %v %q", err, src.calls[len(src.calls)-1])
	}
}

func TestStats(t *testing.T) {
	src := &statsStub{stats: domain.BankStats{Documents: 3, ChangeSeq: 7}}
	st, err := NewStatsReader(src).Stats(t.Context(), port.BankRef{Tenant: "t", Bank: "b"})
	if err != nil || st.Documents != 3 || st.ChangeSeq != 7 {
		t.Errorf("stats = %+v %v", st, err)
	}
}
