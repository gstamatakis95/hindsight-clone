# 120 — Semantic arm: HNSW settings and query shape

## Context

SPEC "Recall pipeline" sets `hnsw.ef_search = 200` and `hnsw.iterative_scan = strict_order` so small banks on
the one shared HNSW index still get enough hits; the card adds `max_scan_tuples` without a value.

## Decision

- Every `SearchVector` runs in a transaction (the caller's `Tx`, or a short read-only one for `Store.Read()`)
  that first sets, transaction-locally (`set_config(..., true)`): `hnsw.ef_search = 200`,
  `hnsw.iterative_scan = strict_order`, `hnsw.max_scan_tuples = 100000` (5x pgvector's default of 20,000,
  so a bank holding 0.1% of a large index is still found before the scan gives up).
- Query shape: an inner `ORDER BY embedding <=> $q LIMIT n` with the bank, type, tag and seq filters in its
  WHERE (index-friendly), and `MinSimilarity` applied in the outer query on `1 - distance`. The inner rows are
  already nearest-first, so the result equals filtering first. Score = cosine similarity. Units with a NULL
  embedding never match. A query embedding of the wrong dimension is `ErrInvalidArgument`.
- `TestAcceptSmallBankVector` loads 50,000 random units with 50 (0.1%) in the searched bank and checks 10
  hits on the default plan and on a plan forced through the HNSW index; with iterative scan off the forced
  plan returns 0 hits, which shows the setting is what makes it work.

## Alternatives

- Per-bank partial HNSW indexes: deferred by the spec ("Decisions: Deferred").
- `relaxed_order` iterative scan: cheaper but can return slightly out-of-order results; the spec says strict.

## Spec lines involved

SPEC "Recall pipeline" (~2017-2019), "Decisions: Embedding"; `internal/port/queries.go` (VectorQuery).
