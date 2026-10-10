# 1004 — Refresh triggers after consolidation, and strategy pattern matching

## Context
- **Triggers.** A consolidation "triggers refreshes of stale models" that have `refresh_after_consolidation`.
  The refresh payload belongs to WP-12, and the port can't list models (CCR WP-10-1).
- **Patterns.** A strategy's `ScopePattern` carries `tags` and a `tags_match`. Hindsight's patterns are
  fnmatch globs matched by "all" (containment) or "exact".

## Decision
- **Refresh triggers.**
  - **Which models, and when.** The run lists the bank's models once, outside any tx, and keeps those
    with `refresh_after_consolidation` that are not `auto_refresh_paused`. Every apply tx that writes an
    observation then enqueues a refresh for each of them its scope reaches, in the same tx as the write
    (after `tx.Seq`). A run that fails or is killed after a scope committed has therefore already queued
    that scope's refreshes (review H4); later batches join the queued refresh.

    Cron models are skipped, since the two triggers are exclusive.
  - **Reach.** A scope reaches a model by the model's tag groups (AND-ed) when they are set. Otherwise it
    goes by the model's tags under its `tags_match`, which defaults to `all_strict` when the model is
    tagged and `any` when it isn't. The shared scope never reaches a tagged model.
  - **Staleness.** Not checked separately: a changed scope has a new seq above the model's watermark. A
    refresh that finds nothing new completes without an LLM call.
  - **Job.** `refresh_mental_model` with serial key `mm:{model_id}`, payload `{"model_id": "..."}` and no
    flags or overrides, using `DedupePending`. A queued refresh is joined, not upgraded. It runs after
    `refresh_debounce_seconds` (default 30) or at the end of the model's interval floor
    (`last_refreshed_at + min_refresh_interval_seconds`), whichever is later.
  - **WP-12** must accept this payload shape. `RefreshPayload` is exported for it.
- **Strategy patterns.** A pattern matches a scope by `domain.MatchTags(scope tags, pattern tags, mode)`.
  - Unset mode means `all_strict`. That is Hindsight's "all": the scope holds every pattern tag, others
    are allowed, and the shared scope never matches.
  - A pattern with no tags matches only under `exact`, and then only the shared scope, so an empty
    pattern never claims everything.
  - There are no globs. The spec's `ScopePattern` documents plain tags, and glob support can be added
    without a contract change.
  - **Precedence.** The first strategy in list order with a matching pattern wins whole. The config
    resolver (decision 402) slots it between bank and request, so a request override beats the strategy.
- **Preview.** It examines up to 1000 scopes from a `ScopeLister`. `Complete` is false when there are
  more. Each draft lists the scopes it wins and those an earlier draft shadows.

## Alternatives
- **Leave triggers to WP-12 behind a consumer interface.** Rejected: the card puts triggers in WP-10. The
  selection is consolidation's; only the payload shape is shared.
- **fnmatch globs in patterns.** Deferred: not in the spec.

## Spec lines involved
Jobs table (consolidate "triggers refreshes of stale models"; refresh_mental_model serial key `mm:{id}`,
debounce and parking), Behavioural rules (Mental models: staleness, untagged writes, interval floor,
paused), Model routing (strategy precedence), ConsolidationStrategy / ScopePattern.
