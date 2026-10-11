package recall

import (
	"cmp"
	"context"
	"fmt"
	"memd/internal/domain"
	"slices"
	"strings"
)

// tagVocabulary lists a bank's tags with their counts, for fuzzy tag leaves; store.Reader satisfies it.
type tagVocabulary interface {
	// TagCounts returns up to limit (<= 0 = all) tags of b's source with how many items carry each, ordered
	// by tag, keeping tags that start with prefix and sort after after. Unknown or tombstoned bank:
	// domain.ErrNotFound.
	TagCounts(
		ctx context.Context, b domain.BankRef, source domain.TagSource, prefix, after string, limit int,
	) ([]domain.TagCount, error)
}

// bankTags returns the distinct tags of b's live units, most used first (ties by tag), read through v.
func bankTags(ctx context.Context, v tagVocabulary, b domain.BankRef) ([]string, error) {
	counts, err := v.TagCounts(ctx, b, domain.TagSourceMemories, "", "", 0)
	if err != nil {
		return nil, fmt.Errorf("tag counts: %w", err)
	}
	slices.SortFunc(counts, func(x, y domain.TagCount) int {
		if c := cmp.Compare(y.Count, x.Count); c != 0 {
			return c
		}
		return strings.Compare(x.Tag, y.Tag)
	})
	out := make([]string, len(counts))
	for i, c := range counts {
		out[i] = c.Tag
	}
	return out, nil
}
