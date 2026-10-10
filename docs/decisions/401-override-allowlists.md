# 401 — Per-RPC override allowlists

## Context
SPEC "Per-request overrides" says fields an RPC can't use are rejected with INVALID_ARGUMENT, never
silently ignored. Its table describes what each override "reaches" in prose. `config.ValidateOverrides(rpc, o)`
needs an exact path and slot list per RPC.

## Decision
Paths are FieldMask paths (proto names, dotted into nested messages). A message entry such as `recall`
allows every path under it.

| RPC constant | Paths | Slots |
|---|---|---|
| `Retain`, `ReprocessDocument` | retain_mission, extraction_mode, custom_extraction_prompt, entity_labels, entities_allow_free_form, default_observation_scopes, retain | EXTRACT, TIME_PARSE |
| `Recall` | recall | TIME_PARSE, RERANK |
| `Reflect` | reflect_mission, disposition, reflect, recall | REFLECT, STRUCTURED_OUTPUT, RERANK |
| `Consolidate` | observations_mission, max_observations_per_scope, consolidation_source_facts_max_tokens, consolidation_source_facts_max_tokens_per_observation, consolidation_llm_batch_size, consolidation_max_units_per_run, dedup_threshold, consolidation_strategies | CONSOLIDATE, DEDUP_CHECK |
| `UpdateMemory` | retain.entity_match_threshold | EXTRACT, TIME_PARSE |
| `RefreshMentalModel`, `MentalModelSpec` | reflect_mission, disposition, reflect, mental_models | REFLECT, SYNTHESIZE, MENTAL_MODEL_DELTA, STRUCTURED_OUTPUT |
| `BankConfig` (CreateBank, Update(Bank/Tenant)Config, templates) | everything | every bindable slot |

These rules apply to every RPC, `BankConfig` included:
- Rejected bindings: EMBED (server-wide), an unspecified or unknown slot, the same slot bound twice in one
  level, and a binding with an empty model name.
- A consolidation strategy may bind only CONSOLIDATE and DEDUP_CHECK.

PreviewPrompt validates its overrides against the RPC its operation previews, via `config.PreviewRPC(op)`:
- retain → Retain
- consolidation → Consolidate
- reflect → Reflect
- delta and synthesize → RefreshMentalModel

Readings of the prose, simplest consistent choice each:
- **Retain "labels"** covers `entity_labels` and `entities_allow_free_form`. The latter decides whether
  extraction may emit non-label entities.
- **Retain also allows `default_observation_scopes`.** It is read only at retain, and AGENTS.md requires
  knobs to be overridable per request unless the spec says otherwise.
- **Reflect "missions"** is `reflect_mission` only. Reflect never reads the retain or observations
  missions, so accepting them would mean silently ignoring them.
- **Reflect "nested recall config"** is the whole `recall` message.
- **Consolidate "caps"** covers every consolidation size knob. It excludes `enable_observations` and
  `auto_consolidation`, which are bank switches, not knobs of one run.
- **UpdateMemory "entity resolution"** is `retain.entity_match_threshold`.
- **RefreshMentalModel "reflect and mental-model config"** also takes `reflect_mission` and
  `disposition`, because the agentic refresh runs the reflect loop. "Refresh strategy" is
  `mental_models.default_strategy`.

## Alternatives
- Silently drop unknown fields. Rejected: the spec forbids it.
- Allow every mission on Reflect. Rejected: it accepts fields that have no effect.

## Spec lines involved
Per-request overrides (paragraph and table), Model routing (slot → request override column), Function
signatures (`ValidateOverrides(rpc, o)`), BankConfig comments.
