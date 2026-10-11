package postgres

import (
	"fmt"
	"memd/internal/domain"
	"strings"
)

// PredicateQuery renders p against a VALUES list of the given tag sets and returns a query selecting the
// indexes of the rows it matches, with its arguments. The external tests run it on a real database.
func PredicateQuery(p *domain.TagPredicate, tagSets [][]string) (string, []any) {
	var a sqlArgs
	rows := make([]string, len(tagSets))
	for j, tags := range tagSets {
		rows[j] = fmt.Sprintf("(%d, %s)", j, a.add(tags, "::text[]"))
	}
	sql := "SELECT coalesce(array_agg(i ORDER BY i), '{}') FROM (VALUES " + strings.Join(rows, ", ") +
		") v(i, tags) WHERE " + renderPredicate(p, "tags", &a)
	return sql, a.vals
}
