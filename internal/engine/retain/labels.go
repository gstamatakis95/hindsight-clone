package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// LabelResolver maps extracted key:value labels to exact-match label entities.
type LabelResolver interface {
	// Resolve upserts one label entity per (key, value) and returns their ids plus the "key:value" tags
	// of labels declared with write_to_tags, to add to the unit's tags.
	Resolve(ctx context.Context, tx port.Tx, b port.BankRef, labels []domain.Label) ([]port.EntityID, []string, error)
}
