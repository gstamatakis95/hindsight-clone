package postgres

import (
	"memd/internal/store"
	"strings"

	"github.com/google/uuid"
)

// graphMaxHops is the deepest expansion ExpandGraph runs; the port bounds MaxHops to 1-2 (decision 123).
const graphMaxHops = 2

// clampHops bounds the hop count to [1, graphMaxHops].
func clampHops(h int) int {
	return min(max(h, 1), graphMaxHops)
}

// graphSQL builds the graph query (decision 123): walk follows the bank's links forward (from -> to) from
// the seeds for up to MaxHops hops, multiplying the link weights along each path, restricted to Kinds at
// every step. Every link is bound to the query's tenant and bank, at the anchor and at each step. A reached
// unit scores the best (max) weight over its paths. Seeds are never hits; reached units must be live units of
// the bank and pass Types, Tags and Seq (intermediate units on a path are not filtered).
func graphSQL(q store.GraphQuery, seeds []uuid.UUID) (string, []any) {
	var a sqlArgs
	tenant, bank := a.add(q.Bank.Tenant, "::text"), a.add(q.Bank.Bank, "::text")
	seedArg := a.add(seeds, "::uuid[]")
	links := "l.tenant_id = " + tenant + " AND l.bank_id = " + bank
	if len(q.Kinds) > 0 {
		kinds := make([]string, len(q.Kinds))
		for i, k := range q.Kinds {
			kinds[i] = string(k)
		}
		links += " AND l.kind = ANY(" + a.add(kinds, "::text[]") + ")"
	}
	where := unitFilter{bank: q.Bank, types: q.Types, tags: q.Tags, seq: q.Seq}.render("u.", &a)
	var b strings.Builder
	b.WriteString("WITH RECURSIVE walk(unit_id, score, hops) AS (")
	b.WriteString("SELECT l.to_unit_id, l.weight, 1 FROM memory_links l WHERE " + links)
	b.WriteString(" AND l.from_unit_id = ANY(" + seedArg + ")")
	b.WriteString(" UNION ALL SELECT l.to_unit_id, w.score * l.weight, w.hops + 1 FROM walk w")
	b.WriteString(" JOIN memory_links l ON l.from_unit_id = w.unit_id AND " + links)
	b.WriteString(" WHERE w.hops < " + a.add(clampHops(q.MaxHops), "::int"))
	b.WriteString("), best AS (SELECT unit_id, max(score) AS score FROM walk WHERE unit_id <> ALL(" + seedArg + ")")
	b.WriteString(" GROUP BY unit_id)")
	b.WriteString(" SELECT u.id, b.score FROM best b JOIN memory_units u ON u.id = b.unit_id WHERE " + where)
	b.WriteString(" ORDER BY b.score DESC, u.id LIMIT " + a.add(q.Limit, "::int"))
	return b.String(), a.vals
}
