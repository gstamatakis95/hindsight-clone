package arms

import (
	"context"
	"memd/internal/recall"
	"memd/internal/store"
)

// Text is the "text" arm: BM25 full-text search over unit text.
type Text struct{ s store.Search }

var _ recall.Arm = (*Text)(nil)

// NewText returns the text arm over s.
func NewText(s store.Search) *Text { return &Text{s: s} }

// Name implements recall.Arm.
func (*Text) Name() string { return "text" }

// Description implements recall.Arm.
func (*Text) Description() string {
	return "Full-text search: BM25 keyword relevance over each memory's text."
}

// Retrieve implements recall.Arm: SearchText with the min_scores floor (default none).
func (a *Text) Retrieve(ctx context.Context, q recall.Query) ([]recall.Hit, error) {
	var floor float64
	if m := q.ArmSettings(a.Name()).MinScore; m != nil {
		floor = *m
	}
	hs, err := a.s.SearchText(ctx, store.TextQuery{
		Bank: q.Bank, Text: q.Text, Types: q.Types, Tags: q.Tags, MinScore: floor, Seq: q.Seq, Limit: q.Limit,
	})
	return hits(hs), err
}
