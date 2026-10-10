# 1202 — Refresh modes, fallbacks, watermark and the version write

## Context
SPEC fixes the watermark (read BankSeq = S first, `last_seq_seen = S` on success), the source-id rule
(accumulate on delta, prune on full), "no LLM call when nothing is in scope" and lists fallback reasons only
by example (`no_baseline_content | source_query_changed | ...`). `max_delta_ratio` has no stated meaning.

## Decision
- **Order:** read the model, then `BankSeq` = S, then merge config (server < tenant < bank < model
  overrides < trigger knobs < call overrides). The window is `(0, S]` for full and `(last_seq_seen, S]`
  for delta.
- **Fallback to full** (`mode_fallback_reason`): `force_full`; `no_baseline_content` (empty content);
  `source_query_changed` (UpdateMentalModel reset `last_seq_seen` to 0 because the source query or the read
  scope changed); `sources_retracted` (a cited source is no longer live, so the document must drop it);
  `max_delta_ratio_exceeded` (after a delta, the share of the old document's blocks that are gone exceeds
  `max_delta_ratio`; the runner then runs the full synthesis).
- **No LLM call:** before drafting, `NewestSeq` over the window (delta: with untagged units excluded for a
  tagged model, decision 1203). Zero = NO_NEW_FACTS with no model call; the run still succeeds, so
  `last_refreshed_at` and `last_seq_seen` advance (no in-scope unit lies in the skipped window, so
  advancing loses nothing and saves rescanning).
- **Delta with no ops** (or ops that change nothing): content unchanged, no version, outcome NO_NEW_FACTS,
  watermark advanced.
- **Version write** (one short transaction, no model call): re-read the model; if its version, content or
  `last_seq_seen` moved since the run read it (cleared, re-scoped, or another version), fail with
  `domain.ErrConflict` (the job retries) instead of overwriting. On changed content: `tx.Seq`, a
  HISTORY_KIND_VERSION entry with the replaced content (skipped for the very first build), version + 1,
  structured output, page embedding (kept from before if embedding failed), MENTAL_MODEL_REFRESHED event.
  A full refresh replaces `source_ids` (pruned, even to empty when nothing is in scope); a delta merges.
  `last_trace` is stored when `keep_trace`.
- **Structured output:** when the trigger has `response_schema_json`, the final page (after delta ops) is
  projected through the consumer-declared `Projector` (reflect.StructuredProjector); a failed projection
  fails the refresh (`structured_output_failed`), as in Hindsight.
- **Page embedding** failures are a trace warning, not a failure.

## Alternatives
- Only advancing `last_refreshed_at` on NO_NEW_FACTS (literal reading): the next check would rescan the
  same empty window forever. Advancing is equivalent and cheaper.
- `max_delta_ratio` as a pre-check on the window size: needs an extra count query and says nothing about
  what the delta did.

## Spec lines involved
Behavioural rules (Watermark, delta, no LLM call, dry run), gRPC surface (RefreshTrace, DryRunRefresh,
MentalModelConfig.max_delta_ratio, keep_trace).
