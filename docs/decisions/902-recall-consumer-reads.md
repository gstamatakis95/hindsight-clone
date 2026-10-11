# 902 — Recall's reads beyond port.Reader, tags and includes

## Context
Four recall features need reads that `port.Reader` doesn't have, and the port is frozen:
- fuzzy tag leaves need the bank's tag vocabulary;
- include.chunks needs chunk rows;
- include.entities needs entity state with observation ids;
- `Memory.stale` needs per-scope unconsolidated facts.

**Update (WP-16):** the CCR batch landed, so the consumer interfaces are gone. `RecallerDeps.Lookups` is
removed and `RecallerDeps.Reader` serves everything. `TagVocabulary` is now the `TagCounts` method, read with
`TagCounts(b, memories, "", "", 0)` and sorted by count descending, then tag. include.chunks and
include.entities read `GetChunks` and `EntityStates`. `Memory.Stale` is set for returned observations from one
`StaleScopes` call over their distinct scopes. The temporal arm sends the query embedding with
`MinSimilarity = 0.1` (CCR WP-09-2). Decision 1601 has the details.

## Decision (original)
- **Consumer interfaces.** Recall declares `TagVocabulary`, `ChunkReader` and `EntityStateReader` (together
  `Lookups`). Their signatures are exactly what CCR WP-09-1 proposes for `port.Reader`, so the Postgres reader
  satisfies them once the CCR lands.
  - `RecallerDeps.Lookups` is optional. Without it, a fuzzy leaf fails with FAILED_PRECONDITION (it never
    silently falls back to exact), and chunks and entities attach nothing.
  - Source facts need only `GetUnits` (Unit.SourceIDs).
- **Fuzzy resolution** copies Hindsight's tag_resolution.py:
  - trigram similarity (pg_trgm's algorithm, in Go) on the value part, only within the same `namespace:`;
  - an exact hit wins;
  - a token with no match stays as itself, so the leaf is unsatisfiable rather than "no filter";
  - any = the union of expansions; all = an AND of per-token unions; exact = an OR over the cross product,
    where more than 32 branches is INVALID_ARGUMENT;
  - tokens are normalized and deduplicated first.
- **Compiled predicates** always carry normalized tags and an explicit mode. Flat tags default to ANY (the
  Recall RPC default). Group leaves default to ANY_STRICT (the TagLeaf comment).
- **Includes:**
  - Chunks fill in result order. The chunk that crosses the budget is cut and marked truncated, and the walk
    stops there (as in Hindsight).
  - Source facts skip what doesn't fit and set source_facts_truncated.
  - Hindsight's filtering of chunks by the document's tags (#5030) is not built.
- **Stale** is not built because it isn't on the card. `Memory.Stale` stays false until CCR WP-09-1's
  StaleScopes read exists.
- **Token counting** is injected through `TokenCounter`. No tokenizer dependency is added here; WP-14 wires
  one.

## Alternatives
- Reaching the tx-only repos through `Store.InTx`: recall would open transactions on the API path and still
  lack these queries.

## Spec lines involved
Ports (Reader: "every query the API path needs"); TagLeaf.Resolve; RecallInclude; Memory.stale; Engines
(TagFilterCompiler, Includer signatures).
