# 1104 — Reflect tool defaults, scoping and token estimates

## Context
The tool declarations (`reflect_tools.json`, ported by WP-04) name defaults; SPEC adds ReflectConfig knobs.
No tokenizer is in the module (the allowed tiktoken-compatible one isn't added yet).

## Decision
- **Token estimate:** one token per four bytes, used only for read_mental_models' page budget and the
  "first page in full up to 4000 tokens" rule of search_mental_models. Good enough for budgets that are soft
  by design; swap in the shared tokenizer when one lands.
- **recall:** `max_tokens` argument clamped to [1000, 16000], else `recall_max_tokens`, else 2048; budget
  `recall_budget`, else MID; types = the request's internal `Types` minus observations, else world +
  experience; the request's tags, query time, seq range, recall include and overrides pass through.
- **search_observations:** recall over observations only; `max_tokens` clamped as above, else
  `search_observations_max_tokens` (default 5000); entity state when `search_observations_include_entities`
  (default true). Each observation carries `is_stale` (recall's per-memory flag); the top-level `is_stale` is
  true when any is.
- **search_mental_models:** `max_results` default 5. Hits are read with `ReadPages` and filtered by the
  request's tag filter and exclusions (PageReader has no tag filter); the best visible page comes in full when
  it is at most ~4000 tokens, the rest as a snippet (the hit's, else the first 280 characters) with
  `content_chars`.
- **read_mental_models:** `max_tokens` default 6000 (clamped as above); pages in request order; the first page
  is always read; a later page that would cross the budget, a missing page or one outside the tag filter goes
  to `not_read`. With `ExcludeMentalModels` the page tools are not offered at all.
- **expand:** units and, for observations, their source facts via `Reader.GetUnits`, filtered by the request's
  tag filter and seq range; chunks once CCR WP-11-2 lands; unknown ids get a per-item error.
- **Tool set:** `ReflectConfig.enabled_tools` (nil = all) filters the retrieval tools; `done` is always
  offered; if the filter leaves no search tool, recall is offered so the agent can still retrieve.

## Spec lines involved
ReflectConfig; Engines > reflect (tools); Dependencies (tiktoken-compatible tokenizer).
