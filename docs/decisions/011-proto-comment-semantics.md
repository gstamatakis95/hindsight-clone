# 011 — Behaviour the proto comments state where the spec is silent

## Context
buf COMMENTS requires a comment on every element, so writing them forced choices the spec leaves open.
These comments sit in a frozen contract, so each choice is recorded here. The matching domain doc
comments say the same thing.

## Decision
- **Unset enums.**
  - `TIME_FIELD` / `DOC_TIME_FIELD` mean created_at.
  - `TAG_SOURCE` means memories.
  - `TagLeaf.RESOLVE` means exact names.
  - `HISTORY_KIND` means both kinds.
  - `MEMORY_STATE` means no state filter in ListMemories (both tables, as "ListMemories reads both
    tables; state selects one" implies) and "unchanged" in UpdateMemory. An unset fact type in
    UpdateMemory is also "unchanged".
  - `PROMPT_OPERATION` is INVALID_ARGUMENT.
  - `OperationStatus` / `OperationKind` mean no filter in ListOperations.
- **Ranges and ordering.**
  - List time ranges are start-inclusive and end-exclusive (`domain.TimeWindow` is half-open too).
  - Mental-model history is newest first; observation versions are oldest first.
  - `ListEventsResponse.last_seq` is the value to pass as `after_seq` on the next poll.
- **Tag matching** follows decision 006 (Hindsight): plain modes admit untagged units, `_STRICT` modes
  don't, EXACT is set equality, and repeated tag_groups are AND-ed.
- **Recall.**
  - Budgets are per-arm candidate caps: `budget_low/mid/high` default to 100 / 300 / 1000, and the
    adaptive cap scales with `max_tokens`, clamped to [`budget_min`, `budget_max`] = [20, 2000].
    These match the spec's "Four retrievers (each capped at 100 / 300 / 1000 by budget)",
    `ParsedQuery.Limit` and `BudgetLimit(cfg, b, maxTokens)`.
  - `strategy_boosts` is an RRF weight per arm, 1 when missing.
  - `reranker_max_candidates` defaults to 300 (the pipeline diagram's "top 300").
  - An explicit time window on Recall disables query time parsing.
- **Retain.** `RetainItem.tags` are added to the request's `document_tags`.
- **Mental models and observations.**
  - `max_delta_ratio` is the largest share of a page a delta refresh may change before falling back to
    a full refresh.
  - `PreviewConsolidationStrategiesResponse.complete = false` means the preview stopped before checking
    every scope.
  - `UpdateDocumentResponse.operation` is unset when the update is a set-equal no-op.

- **Review round 1** (orchestrator rulings; see decision 012):
  - ModelBinding merges per slot as a whole binding. Unset binding fields mean the gateway / adapter
    default, not an inherited value (R4).
  - tags and tag_groups are mutually exclusive on Recall and Reflect. Only RefreshTrigger /
    ResolvedScope tag_groups override a model's tags (R5).
  - ListEvents' limit is soft and never splits a seq group. `last_seq` is the highest returned seq, or
    the request's after_seq when the page is empty (R6).
  - Unset `TagsMatch` behaves as ANY unless a field documents another default (TagLeaf and
    ListDocuments: ANY_STRICT; RefreshTrigger on a tagged model: ALL_STRICT).
  - Every `overrides` comment lists exactly the reach in SPEC 898-906; other fields are
    INVALID_ARGUMENT.
  - `Bank.deleting` is kept as the spec lists it, though reads of a deleting bank return NOT_FOUND, so
    clients normally never see it set.

## Alternatives
Leave the comments vague. Rejected: buf COMMENTS only checks presence, but vague comments in a frozen
contract would push each choice down to the implementing WP, so several WPs could each make it
differently.

## Spec lines involved
gRPC surface (ListMemoriesRequest, ListDocumentsRequest, RecallConfig, RetainItem, MentalModelConfig,
PreviewConsolidationStrategiesResponse, ListEventsResponse); Curation ("ListMemories reads both
tables"); Recall pipeline (budget caps, rerank top 300); Function signatures (`BudgetLimit`).
