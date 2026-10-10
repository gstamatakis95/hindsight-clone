# 121 — Keyword arm: pg_search BM25 query form

## Context

The keyword arm is BM25 through pg_search 0.26 (`memory_units_bm25` over id, text, context and literal
tenant/bank/fact_type columns). User query text is free-form and can contain quotes, colons or other query
syntax.

## Decision

- Match with the `|||` operator (match disjunction) on `text` and on `context`, with the raw query text as a
  bind parameter. `|||` tokenizes the value with the field's tokenizer instead of parsing it as a query, so
  punctuation in user text never errors. Tenant and bank (and the other filters) are in the same WHERE.
- Score = `paradedb.score(id)`, best first, ties by id; `MinScore` is applied after the LIMIT on the sorted
  rows. An empty or blank query returns no hits (no error).

## Alternatives

- `paradedb.parse()` / query-string syntax: richer, but user text with `:` or quotes raises errors.
- Native `tsvector` with `ts_rank_cd` (Hindsight's default): not what the spec decided.

## Spec lines involved

SPEC "Recall pipeline" (keyword arm), "Decisions: Keyword search"; decision 004 "Indexes" (BM25).
