# WP-04 · Config, routing, time, prompts

Branch `wp/WP-04-config-prompts`, rebased on main (86e9caa). `make gen` leaves no diff. `make lint`
(BREAKING_AGAINST=contracts-v1) and `make test` (`go test -race ./...`) pass.

## Built
- [x] **`Merge`** (`internal/config/merge.go`):
  - field by field, through one reflection walker keyed on proto field names;
  - nested messages merge per field;
  - `models` merges per slot, and the whole binding is replaced (ruling R4);
  - other lists and maps are replaced;
  - the result never aliases its inputs.
- [x] **FieldMask apply and clear** (`mask.go`):
  - `ApplyMask(base, patch, paths)`: a masked empty list becomes an explicit clear, and an empty mask
    merges;
  - `ResetPaths(cfg, paths)`: inherit again, or reset everything when no paths are given;
  - `SetPaths` and `LeafPaths`.
- [x] **`ConfigResolver`** (`resolver.go`): `NewResolver(port.Store, StrategyMatcher, server)`.
  - Precedence is server < tenant < bank < strategy < request.
  - `ForScope` matches against the merged strategy list, request included.
  - `StrategyLevel` builds the strategy level.
  - An unknown or tombstoned bank is NOT_FOUND.
- [x] **`ValidateOverrides(rpc, o)`** (`validate.go`): a per-RPC allowlist of paths and slots, returning
  INVALID_ARGUMENT for anything else.
  - It covers Retain, ReprocessDocument, Recall, Reflect, Consolidate, UpdateMemory, RefreshMentalModel,
    MentalModelSpec and BankConfig.
  - It also rejects EMBED, unspecified, unknown and duplicate slots, empty model names, and strategy slots
    outside CONSOLIDATE / DEDUP_CHECK.
  - `PreviewRPC(op)` serves PreviewPrompt.
- [x] **`ServerDefaults(env)`** (`defaults.go`): the defaults the spec states, plus `MEMD_MODEL` and
  `MEMD_MODEL_<SLOT>` bindings.
- [x] **`ModelRouter`** (`internal/engine/routing`): `NewRouter()`.
- [x] **`timex`**:
  - rules first (`parseRules`);
  - `hasTemporalCue`;
  - the TIME_PARSE fallback only when a cue is found and no rule matches, with the router resolved lazily;
  - `Normalize` for extracted facts.
- [x] **Prompt templates ported from Hindsight** (`internal/prompts/templates`, 36 files):
  - extraction modes `extract_concise`, `extract_verbose`, `extract_verbatim` and `extract_custom`, plus
    `extract_user`, keeping the who / what / when / where / why dimensions;
  - consolidation decide: `consolidate_system` / `_user`;
  - dedup: `dedup_system` / `_user`;
  - reflect system and final: `reflect_system`, `reflect_final`, `reflect_final_user`,
    `reflect_done_request`, and the `reflect_tools` tool declarations;
  - delta and structured delta: `mm_delta_*`, `mm_structured_delta_*`, `mm_refresh_context`;
  - single-shot synthesize: `synthesize_system` / `_user`;
  - extras: `structured_output_system` / `_user` (the STRUCTURED_OUTPUT projection) and
    `time_parse_system` / `_user`.
  - `Version` is a content hash of the template plus its schema. `templates/NOTICE` carries the MIT notice.

## Acceptance tests
| Accept item | Test(s) | Result |
|---|---|---|
| Merge precedence table | `internal/config`: `TestMergePrecedence`, `TestEffectivePrecedence`, `TestForScopeStrategyBetweenBankAndRequest` | pass |
| …a request setting only RERANK keeps the bank's other slots | `TestMergeRequestRerankOnlyKeepsBankSlots` (+ `TestMergeBindingReplacedWhole`) | pass |
| Merge isolation | `TestMergeDoesNotAliasInputs`, `TestEffectiveConcurrentNoAliasing` (-race) | pass |
| FieldMask apply / clear | `TestApplyMask`, `TestMaskedClearOverridesLowerLevel`, `TestApplyMaskPrunesEmptyMessages`, `TestResetPaths`, `TestMaskUnknownPath`, `TestSetPathsAndLeafPaths` | pass |
| Allowlist tests for every RPC | `TestValidateOverridesPathMatrix` (every leaf path × every RPC), `TestValidateOverridesSlotMatrix` (every slot × every RPC), `TestValidateOverridesBankConfigAllowsEveryPath`, `TestValidateOverridesBadSlots`, `TestValidateOverridesExamples`, `TestPreviewRPC` | pass |
| ModelRouter | `internal/engine/routing`: `TestResolve` | pass |
| ≥60 query time expressions (relative, absolute, ranges, none) with expected windows | `internal/engine/timex`: `TestQueryWindowCorpus` (114 rows: 41 relative, 22 named, 15 absolute, 11 range, 4 non-UTC, 21 none) | pass |
| No-cue queries make zero LLM calls | `TestNoCueMakesNoLLMCall`, plus every corpus row asserts 0 `Resolve` / 0 `Structured`; `TestFallbackOnlyWhenCueAndNoRule` | pass |
| Every template renders with a fixture | `internal/prompts`: `TestEveryTemplateRendersWithFixture` (typed structs, unknown fields rejected), `TestRegistryMatchesTemplates`, `TestEveryTemplateRendersWithZeroData`, `TestBranchVariants`, `TestSchemasPairWithTemplates`, `TestReflectToolAssets` | pass |
| Server defaults | `TestServerDefaults`, `TestServerDefaultsNoModels` | pass |

## Decisions and CCRs
- **400:** `TimeNormalizer` methods take `cfg domain.BankConfig`, so per-request TIME_PARSE overrides
  reach the fallback. This is an engine-package interface change; the mock was regenerated. WP-08 and
  WP-09 call it with their resolved config.
- **401:** the per-RPC override allowlists and the readings of the spec table.
- **402:**
  - the resolver takes `port.Store`, because `port.Reader` has no config reads;
  - unknown and tombstoned banks are NOT_FOUND;
  - the FieldMask, reset and empty-mask semantics;
  - ServerDefaults' environment variables.
- **403:** timex window semantics: half-open windows, Monday weeks, northern seasons, US m/d dates, and
  the cue rules.
- **404:** prompt layout: one message per template, a schema named after its system template, tool JSON
  assets, and typed data structs.
- **CCR WP-04-1 (proposed):** drop `omitempty` on the clearable list and map fields of `BankConfig` and
  `ObservationScopes`. Without it, an explicit list clear is lost on the jsonb round trip, and the
  tenant's list comes back.

## Known gaps
- **CCR WP-04-1 is not applied.** Until it is, a list cleared on a bank or tenant reverts after
  persistence (WP-01/WP-06).
- **`Version` covers only the named template and its schema.** Editing just a user template does not
  change the version recorded on the call.
- **timex false positives:** "I have 2000 cats" gives year 2000, and "this spring water" gives a season.
  Each only narrows the temporal arm.
- **timex is weekday-strict:** "what did I do Tuesday", with no qualifier, gets no window.
- **The markdown `mm_delta_*` pair** has no consumer in the spec (`Synthesizer.Delta` returns ops). It
  is built as asked.
- **PromptPreviewer can't attribute blocks to fields:** each template renders a whole message, so it
  can't attribute individual blocks to BankConfig fields without splitting templates further (WP-13).
- **ServerDefaults leaves knobs unset** where the spec gives no default (chunk sizes, link windows,
  recency horizon, and others). The reading engine owns those defaults.

## Notes for downstream WPs
- **WP-08:**
  - Build fact text as `what | When: … | Involving: … | why`.
  - Validate labels against config.
  - Drop entities when `entities_allow_free_form=false`.
  - Use `extract_concise` when custom mode has an empty prompt.
- **WP-10:**
  - The consolidate response is `{creates, updates}`. Fill `Evidence.Quote` from the unit text.
  - Dedup returns `{action: merge|keep, text, reason}`.
- **WP-11:**
  - Offer exactly the tools whose flags are set.
  - `reflect_final`, `synthesize` and `mm_delta` are free text, so use `Chat` with no tools.
- **WP-12:**
  - Structured-delta ops decode straight into `domain.DeltaOp`.
  - The `Rejected` field drives the one allowed retry.
