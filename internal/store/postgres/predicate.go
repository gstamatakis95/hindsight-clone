package postgres

import (
	"memd/internal/domain"
	"strconv"
	"strings"
)

// The recall arms build their SQL at run time: the tag filter is a variable tree (domain.TagPredicate) and the
// other filters are optional, which sqlc's static queries can't express. Every value is passed as a bind
// parameter through sqlArgs; only fixed SQL text and $n placeholders are ever concatenated.

// sqlArgs collects bind parameters and hands out their $n placeholders.
type sqlArgs struct {
	// vals are the parameters in placeholder order.
	vals []any
}

// add appends v and returns its placeholder followed by the explicit cast (e.g. "::text[]"), so dynamic SQL
// never depends on Postgres inferring a parameter type.
func (a *sqlArgs) add(v any, cast string) string {
	a.vals = append(a.vals, v)
	return "$" + strconv.Itoa(len(a.vals)) + cast
}

// unitFilter is what every arm filters its units by.
type unitFilter struct {
	bank  domain.BankRef
	types []domain.FactType
	tags  *domain.TagPredicate
	seq   domain.SeqRange
}

// render returns one WHERE fragment over memory_units columns prefixed with col (e.g. "u." or ""): the bank
// (tenant, org and bank), the fact types, the tag predicate and the seq range.
func (f unitFilter) render(col string, a *sqlArgs) string {
	var b strings.Builder
	b.WriteString(col + "tenant_id = " + a.add(f.bank.Tenant, "::text"))
	b.WriteString(" AND " + col + "org_id = " + a.add(f.bank.Org, "::text"))
	b.WriteString(" AND " + col + "bank_id = " + a.add(f.bank.Bank, "::text"))
	if len(f.types) > 0 {
		types := make([]string, len(f.types))
		for i, t := range f.types {
			types[i] = string(t)
		}
		b.WriteString(" AND " + col + "fact_type = ANY(" + a.add(types, "::text[]") + ")")
	}
	if f.tags != nil {
		b.WriteString(" AND (" + renderPredicate(f.tags, col+"tags", a) + ")")
	}
	if f.seq.After > 0 {
		b.WriteString(" AND " + col + "seq > " + a.add(f.seq.After, "::bigint"))
	}
	if f.seq.UpTo != nil {
		b.WriteString(" AND " + col + "seq <= " + a.add(*f.seq.UpTo, "::bigint"))
	}
	return b.String()
}

// renderPredicate renders p as a boolean SQL expression over the text[] column tags. It agrees with
// (*domain.TagPredicate).Eval: an empty and is TRUE, an empty or is FALSE, and a malformed node (unknown op,
// a not without exactly one argument) is FALSE. A nil p is TRUE.
func renderPredicate(p *domain.TagPredicate, tags string, a *sqlArgs) string {
	if p == nil {
		return "TRUE"
	}
	switch p.Op {
	case domain.PredicateTags:
		return renderLeaf(p.Tags, p.Match, tags, a)
	case domain.PredicateAnd:
		return joinArgs(p.Args, " AND ", "TRUE", tags, a)
	case domain.PredicateOr:
		return joinArgs(p.Args, " OR ", "FALSE", tags, a)
	case domain.PredicateNot:
		if len(p.Args) != 1 {
			return "FALSE"
		}
		return "NOT (" + renderPredicate(&p.Args[0], tags, a) + ")"
	default:
		return "FALSE"
	}
}

// joinArgs renders the children of an and / or node joined by op; no children renders empty.
func joinArgs(args []domain.TagPredicate, op, empty, tags string, a *sqlArgs) string {
	if len(args) == 0 {
		return empty
	}
	parts := make([]string, len(args))
	for i := range args {
		parts[i] = "(" + renderPredicate(&args[i], tags, a) + ")"
	}
	return strings.Join(parts, op)
}

// renderLeaf renders one tags leaf with domain.MatchTags semantics. Set operators (@>, <@, &&) compare the
// column as a set, so stored order and duplicates don't matter, and the GIN index on tags serves them.
//   - exact: set equality; an empty filter selects only untagged units.
//   - any / all (and unspecified or unknown modes, which MatchTags treats as any): empty filter = TRUE;
//     untagged units pass; otherwise overlap / containment.
//   - any_strict / all_strict: empty filter = TRUE; overlap / containment (an untagged unit never
//     overlaps or contains a non-empty filter).
func renderLeaf(filter []string, mode domain.TagsMatch, tags string, a *sqlArgs) string {
	f := domain.NormalizeTags(filter) // never nil: pgx would send nil as NULL
	if mode == domain.TagsMatchExact {
		p := a.add(f, "::text[]")
		return tags + " @> " + p + " AND " + tags + " <@ " + p
	}
	if len(f) == 0 {
		return "TRUE"
	}
	p := a.add(f, "::text[]")
	switch mode {
	case domain.TagsMatchAnyStrict:
		return tags + " && " + p
	case domain.TagsMatchAllStrict:
		return tags + " @> " + p
	case domain.TagsMatchAll:
		return "cardinality(" + tags + ") = 0 OR " + tags + " @> " + p
	default:
		return "cardinality(" + tags + ") = 0 OR " + tags + " && " + p
	}
}
