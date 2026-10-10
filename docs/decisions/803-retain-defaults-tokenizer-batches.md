# 803 — Retain tuning defaults, token counting and batches

Status: accepted (WP-08)

## Context
`ServerDefaults` leaves most `RetainTuning` knobs unset: "the engine that reads them owns that default".
The spec allows a tiktoken-compatible tokenizer but none is in go.mod yet. "One tx per chunk batch" does not
size the batch.

## Decision
- **Defaults** (Hindsight's values, in `deps.go`):

  | Knob | Default |
  |---|---|
  | chunk_max_tokens | 750 (Hindsight: 3000 characters) |
  | chunk_overlap_tokens | 0 |
  | structured_chunk_max_tokens | chunk_max_tokens |
  | entity_match_threshold | 0.6, inclusive on a score rounded to 6 places |
  | temporal_link_window_hours | 24 |
  | semantic_link_k | 50 (Hindsight's ANN top_k) |
  | semantic_link_min_similarity | 0.7 |
  | temporal_link_cap | 20 (spec) |
  | extract_causal_links | true |

- **Tokens.** `RetainerDeps.Tokens` is the consumer-declared `TokenCounter`. When it is nil, `ApproxTokens`
  (ceil(runes / 4)) is used. WP-14 can wire a real tokenizer without touching retain.
- **Batches.** `RetainerDeps.BatchChunks` defaults to 4 chunks per transaction. Within a batch, chunks are
  extracted sequentially and the batch's facts are embedded in one call. Documents run in parallel across
  jobs (one per serial key). Sequential extraction keeps the work deterministic and the code small; it can be
  parallelised later without changing the transaction shape.
- **Chunker.** It is Hindsight's chunker with token budgets instead of characters:
  - a JSON array of objects is cut between turns;
  - JSONL is cut between lines;
  - a turn or line is kept whole up to `structured_chunk_max_tokens`;
  - anything else uses the recursive paragraph / line / sentence / clause / word / character split.

  Overlap applies to plain text only.

## Alternatives
- Adding a tokenizer dependency now: no consumer-side need, and WP-09 needs the same thing, so WP-14 should
  pick one.

## Spec lines involved
SPEC "RetainTuning" (proto comments), "Engines (retain)", "Jobs" (one tx per chunk batch).
