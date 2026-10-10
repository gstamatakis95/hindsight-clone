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
- **No LLM call:** before drafting, `NewestSeq` over the window with the model's read scope (untagged
  units included where the scope reads them, e.g. `tags_match any`; only staleness excludes them, decision
  1203). Zero = NO_NEW_FACTS with no model call; the run succeeds and only `last_refreshed_at` advances
  (content, `source_ids` and `last_seq_seen` stay, SPEC 2002).
- **Automatic runs re-check:** a run that is not manual, not `force_full` and not a dry run re-checks at
  start. Inside the interval floor (trigger's `min_refresh_interval_seconds`, else the config's, from
  `last_refreshed_at` by the runner's clock) it re-parks itself (decision 1204) and reports NO_NEW_FACTS
  without writing the model, so the floor does not slide and the watermark holds.
  When `ChangeDetector` finds it fresh (a duplicate or racing trigger) it writes nothing at all, not even
  `last_refreshed_at`. Neither makes a model or retrieval call. A run with at most 5 s of the floor left is
  not floored (clock-skew tolerance).
- **Delta with no ops** (or ops that change nothing): content unchanged, no version, outcome NO_NEW_FACTS,
  watermark advanced.
- **Version write** (one short transaction, no model call): `tx.Seq` first (every path, NO_NEW_FACTS
  too), so the bank row lock serializes it with the failure reconcile and Update/Clear; then re-read the
  model. If its version, content or `last_seq_seen` moved since the run read it (cleared, another version),
  or its source query, read scope (`scopeChanged`: tags and fact types as sets, tag groups by value) or
  `response_schema_json` changed (an edit during a first build leaves `last_seq_seen` at 0, so only the
  spec shows it), fail with `domain.ErrConflict` (the job retries) instead of committing a draft of the old
  scope. Every success sets `last_refreshed_at`, clears `auto_refresh_paused` on a manual run and stores
  `last_trace` when `keep_trace`. Nothing in scope (SPEC 2002) stops there: content, version,
  `source_ids` (even dead ones) and `last_seq_seen` stay. Otherwise the watermark advances; on changed
  content a HISTORY_KIND_VERSION entry with the replaced content (only when there is some: none for the
  first build, nor for the rebuild after a Clear, which recorded the content itself), version + 1,
  structured output, page embedding (kept from before if embedding failed), MENTAL_MODEL_REFRESHED event.
  A full refresh replaces `source_ids` with the draft's (pruned). A full run whose retrieval came back empty
  while the scope was not (synthesizer ErrNoNewFacts) keeps the content and keeps only the sources that
  are still live, so kept content keeps its provenance and a retracted source stops re-staling the model.
  A delta merges. Consequence of the SPEC 2002 ruling: a model whose every source was retracted keeps the
  dead ids, so it stays stale and is re-run (a cheap no-call run) at each interval floor until new
  in-scope facts arrive or it is cleared.
- **Structured output:** when the trigger has `response_schema_json`, the final page (after delta ops) is
  projected through the consumer-declared `Projector` (reflect.StructuredProjector); a failed projection
  fails the refresh (`structured_output_failed`), as in Hindsight.
- **Page embedding** failures are a trace warning, not a failure.

## Alternatives
- Advancing `last_seq_seen` too when nothing is in scope: saves rescanning an empty window, but the
  literal SPEC 2002 reading (only `last_refreshed_at`) never moves the watermark past a unit a narrower
  check missed.
- `max_delta_ratio` as a pre-check on the window size: needs an extra count query and says nothing about
  what the delta did.

## Spec lines involved
Behavioural rules (Watermark, delta, no LLM call, dry run), gRPC surface (RefreshTrace, DryRunRefresh,
MentalModelConfig.max_delta_ratio, keep_trace).
