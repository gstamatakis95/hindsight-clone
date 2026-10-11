package retain

import (
	"memd/internal/domain"
	"memd/internal/store"
	"testing"
)

func TestLabelResolverDropsEmptyValues(t *testing.T) {
	e := newEnv(t, 0)
	defs := []domain.EntityLabel{{Key: "note", Type: domain.LabelTypeText, WriteToTags: true}}
	labels := []domain.Label{
		{Key: "note", Value: "N/A"},
		{Key: "note", Value: " none "},
		{Key: "note", Value: "NULL"},
		{Key: "note", Value: "n/a"},
		{Key: "note", Value: "  "},
		{Key: "note", Value: "kept"},
	}
	var ids []domain.EntityID
	var tags []string
	e.inTx(func(tx store.Tx) error {
		var err error
		ids, tags, err = NewLabelResolver().Resolve(e.ctx, tx, testBank, defs, labels)
		return wrap(err)
	})
	if len(ids) != 1 || len(tags) != 1 || tags[0] != "note:kept" {
		t.Errorf("ids %v tags %v, want only note:kept", ids, tags)
	}
}
