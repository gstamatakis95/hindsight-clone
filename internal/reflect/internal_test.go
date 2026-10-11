package reflect

import (
	"errors"
	"fmt"
	"memd/internal/domain"
	"slices"
	"testing"
)

func TestValidCitations(t *testing.T) {
	st := &agentState{
		RetrievedUnits:  map[domain.UnitID]domain.Unit{"u1": {ID: "u1"}, "o1": {ID: "o1"}},
		RetrievedModels: map[domain.MentalModelID]struct{}{"mm": {}},
	}
	got := validCitations(st, []string{"u1", "x", "mm", "u1", "o1", "mm-never"})
	if !slices.Equal(got, []string{"u1", "mm", "o1"}) {
		t.Errorf("got %v", got)
	}
}

func TestDocumentMarkdown(t *testing.T) {
	d := doneDocument{Sections: []docSection{
		{Heading: "", Level: 2, Blocks: []string{"Intro."}},
		{Heading: "Deep", Level: 9, Blocks: []string{" A ", ""}},
		{Heading: "Top", Level: 0, Blocks: nil},
	}}
	want := "Intro.\n\n###### Deep\n\nA\n\n# Top"
	if got := d.markdown(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestIsContextOverflow: only errors wrapping domain.ErrContextOverflow (the gateway's classification) are
// overflows; message text alone no longer counts, and plain UNAVAILABLE is not an overflow.
func TestIsContextOverflow(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("gateway: status 400: %w", domain.ErrContextOverflow), true},
		{fmt.Errorf("chat: %w", fmt.Errorf("gateway: %w", domain.ErrContextOverflow)), true},
		{fmt.Errorf("%w: gateway: status 400: maximum context length is 8192 tokens", domain.ErrUnavailable), false},
		{fmt.Errorf("%w: gateway: status 429: too many tokens per minute", domain.ErrUnavailable), false},
		{errors.New("prompt is too long"), false},
	} {
		if got := isContextOverflow(c.err); got != c.want {
			t.Errorf("%v: got %v, want %v", c.err, got, c.want)
		}
	}
}
