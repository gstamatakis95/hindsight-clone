# 1203 — Staleness scope: untagged units, tag groups and fuzzy leaves

## Context
SPEC: "a model is stale if a unit or observation with seq above `last_seq_seen` matches its own scope
(tags, tag groups, fact types) ... Untagged writes never make a tagged model stale, even when its refresh
reads untagged memories." Tags default to `all_strict` for a tagged model; tag groups override flat tags.
Fuzzy tag leaves need a trigram lookup against the bank's tags (recall's TagFilterCompiler, WP-09).

## Decision
- The scope compiles to a `port.SQLPredicate` in the engine (`ResolvedScope.predicate`). For staleness
  (and the delta window check) a scoped model's predicate is AND-ed with "has at least one tag"
  (`not(exact [])`), so untagged units never stale it even under `tags_match any / all`. An untagged model
  with `exact []` keeps the untagged scope.
- Fuzzy leaves are compiled as exact names for staleness. Recall and reflect still receive the
  `domain.TagFilter` and resolve fuzzy leaves themselves, so only the staleness check is affected (a
  fuzzy-only match can be missed until the next exact-name or cron trigger).
- Retraction: any `source_ids` entry that `GetUnits` no longer returns (archived or replaced) makes the
  model stale.

## Alternatives
- Depending on recall's TagFilterCompiler: an extra engine dependency and a DB round trip per check for a
  rare case. Deferred.

## Spec lines involved
Behavioural rules (Staleness), gRPC surface (RefreshTrigger.tags_match, tag_groups).
