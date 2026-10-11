# 1203 — Staleness scope: untagged units, tag groups and fuzzy leaves

## Context
SPEC: "a model is stale if a unit or observation with seq above `last_seq_seen` matches its own scope
(tags, tag groups, fact types) ... Untagged writes never make a tagged model stale, even when its refresh
reads untagged memories." Tags default to `all_strict` for a tagged model; tag groups override flat tags.
Fuzzy tag leaves need a trigram lookup against the bank's tags (recall's TagFilterCompiler, WP-09).

**Update (WP-16):** the follow-up below is done. Fuzzy leaves are no longer dropped.
- `scopeQuery` resolves a scope that has a fuzzy leaf against the bank's tags. It reads them with
  `ScopeReader.TagCounts` (port.Reader) and compiles them with recall's `TagFilterCompiler`, so staleness and
  the pre-check see exactly the units recall retrieves.
- The trigram floor is `recall.FuzzyTagMinSimilarity`. The runner's pre-check uses the model's effective
  config. The `ChangeDetector`, which has no config, uses recall's default (0.45).
- Cost: one `TagCounts` read per check of a fuzzy-scoped model. The bullet below on dropping the tag
  predicate, and its over-triggering cost, are superseded.
- Test: `TestFuzzyScopeResolvesBankTags`.

## Decision
- The scope compiles to a `port.SQLPredicate` in the engine (`ResolvedScope.predicate`). For staleness
  (`ChangeDetector.NeedsRefresh`, also the re-check an automatic run makes when it starts) a scoped model's
  predicate is AND-ed with "has at least one tag" (`not(exact [])`), so untagged units never stale it even
  under `tags_match any / all`. An untagged model with `exact []` keeps the untagged scope. The runner's
  no-LLM pre-check reads the model's read scope instead (untagged units included where the scope reads
  them), so a manual or forced refresh never skips memories its retrieval would use (decision 1202).
- A scope with a fuzzy leaf anywhere in its tag groups drops the tag predicate, in both the pre-check and
  the detector; fact types, the seq window and the untagged rule (for staleness) stay. Recall and reflect
  still receive the `domain.TagFilter` and resolve fuzzy leaves themselves. Cost: such a model looks stale
  after every tagged write, so each trigger runs a full refresh (for the agentic strategy, a whole reflect
  loop), bounded only by the debounce and the interval floor. Compiling fuzzy names as exact ones instead
  would miss real matches (a model could never build).
- **Follow-up:** once CCR WP-09-1 (`Reader.BankTags`) lands, resolve fuzzy names to the bank's exact tags
  before compiling, so the predicate is precise again and the over-triggering goes away.
- Retraction: any `source_ids` entry that `GetUnits` no longer returns (archived or replaced) makes the
  model stale.

## Alternatives
- Depending on recall's TagFilterCompiler: an extra engine dependency and a DB round trip per check for a
  rare case. Deferred.
- Compiling fuzzy leaves as exact names: cheaper, but a fuzzy-only scope then never looks stale and its
  pre-check always says "nothing in scope".

## Spec lines involved
Behavioural rules (Staleness), gRPC surface (RefreshTrigger.tags_match, tag_groups).
