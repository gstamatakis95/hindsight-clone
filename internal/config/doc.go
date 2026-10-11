// Package config builds the effective domain.BankConfig: the server defaults, the level merge, the
// port.ConfigResolver that reads the stored levels, and the per-RPC override validation.
//
// # Where it sits
//
// config sits beside the engines: it imports domain and port only and never an adapter, server, worker or
// engine package. It declares the one engine piece it needs, [StrategyMatcher], so
// consolidate.StrategyMatcher satisfies it without config importing consolidate. cmd/memd/wire.go builds the
// server level with [ServerDefaults] and the resolver with [NewResolver]; engines consume the resolver as
// port.ConfigResolver; the server validates request overrides with [ValidateOverrides]; retain's Ingestor
// and the server's FieldMask code use [Merge].
//
// # Levels and merge
//
// A config is assembled from levels, least to most specific:
//
//	server < tenant < bank < strategy < request < item
//
// server comes from [ServerDefaults]; tenant and bank are the stored overrides; strategy is the
// consolidation strategy matching a scope ([StrategyLevel]; consolidation only); request is an RPC's
// overrides; item is a retain item's overrides. [Merge] combines them:
//
//   - a set field replaces the field below it; "set" means a non-nil pointer, slice or map, or a non-zero
//     plain string or number, so a non-nil empty list is an explicit clear that shadows
//     inherited values;
//   - nested messages (pointers to structs such as recall, retain, reflect, mental_models) merge field by
//     field;
//   - models merge per slot: the most specific level that binds a slot supplies its whole binding, and
//     binding one slot never clears another (SPEC "Model routing");
//   - every other list or map is replaced, never appended.
//
// Merge deep-copies: the result shares no memory with the inputs, so the server defaults held by the
// resolver cannot be changed through a result.
//
// # Server defaults
//
// [ServerDefaults](env) returns the defaults the spec's BankConfig comments state:
//
//   - disposition 3/3/3; extraction_mode concise; auto_consolidation, enable_observations and
//     entities_allow_free_form true; dedup_threshold 0.97;
//   - consolidation_llm_batch_size 8, consolidation_source_facts_max_tokens_per_observation 256,
//     consolidation_max_units_per_run 100;
//   - default_observation_scopes: mode combined, max_combinations 15;
//   - retain: temporal_link_cap 20, entity_match_threshold 0.6;
//   - recall: budget_function fixed, budgets 100 / 300 / 1000 (low / mid / high), budget_min 20, budget_max
//     2000, reranker_max_candidates 300, rerank true, rrf_k 60, recency_alpha 0.2, temporal_alpha 0.2,
//     proof_alpha 0.1;
//   - reflect: max_iterations 10, search_observations_max_tokens 5000, search_observations_include_entities
//     true, max_answer_tokens 4096;
//   - mental_models: default_mode full, default_strategy agentic, refresh_debounce_seconds 30,
//     min_refresh_interval_seconds 0, history_max_entries 50.
//
// Knobs whose default the spec leaves open stay unset; the engine that reads a knob owns its default (each
// engine's package doc lists those).
//
// Model bindings come from the environment. [EnvModel] (MEMD_MODEL) binds every LLM slot (EXTRACT,
// TIME_PARSE, CONSOLIDATE, DEDUP_CHECK, REFLECT, STRUCTURED_OUTPUT, MENTAL_MODEL_DELTA, SYNTHESIZE);
// [EnvModelPrefix]+SLOT (MEMD_MODEL_REFLECT, ...) binds one slot and wins over MEMD_MODEL. RERANK is bound
// only by MEMD_MODEL_RERANK, and without it recall.rerank defaults to false so recall skips the
// cross-encoder instead of failing; a tenant or bank that binds RERANK must also set recall.rerank true.
// EMBED is server-wide and never part of a BankConfig. A slot with no model stays unbound. ServerDefaults
// validates its own result with ValidateOverrides(RPCBankConfig, ...). cmd/memd refuses to start unless
// EXTRACT and REFLECT are bound at this level.
//
// # Resolver
//
// [NewResolver](store, matcher, server) returns the port.ConfigResolver:
//
//   - Effective(ctx, bank, overrides...) merges server < tenant < bank < overrides (in order).
//   - ForScope(ctx, scope, request) merges server < tenant < bank < strategy < request, where strategy is
//     the first consolidation strategy whose pattern matches the scope, taken from the merged list with the
//     request level included, so a Consolidate call can bring its own strategies. A nil matcher disables
//     strategies.
//
// Both read the tenant config and the bank row in one short read-only Store.InTx (port.Reader has no config
// reads), so callers must not call them from inside an open transaction. An unknown or
// tombstoned bank is domain.ErrNotFound (the read rule); write paths call Banks().Ensure first, which
// creates the bank or reports domain.ErrBankDeleting. The resolver holds no other state and is safe for
// concurrent use.
//
// # Override validation
//
// [ValidateOverrides](rpc, o) rejects with domain.ErrInvalidArgument any field the RPC cannot use, so an
// override is never silently ignored (SPEC "Per-request overrides"). The RPC names are the
// RPC* constants; the allowlists are:
//
//   - [RPCRetain], [RPCReprocessDocument]: retain_mission, extraction_mode, custom_extraction_prompt,
//     entity_labels, entities_allow_free_form, default_observation_scopes, retain.*; slots EXTRACT,
//     TIME_PARSE.
//   - [RPCRecall]: recall.*; slots TIME_PARSE, RERANK.
//   - [RPCReflect]: reflect_mission, disposition, reflect.*, recall.*; slots REFLECT, STRUCTURED_OUTPUT,
//     RERANK.
//   - [RPCConsolidate]: observations_mission, max_observations_per_scope, the consolidation_* knobs,
//     dedup_threshold, consolidation_strategies; slots CONSOLIDATE, DEDUP_CHECK.
//   - [RPCUpdateMemory]: retain.entity_match_threshold; slots EXTRACT, TIME_PARSE.
//   - [RPCRefreshMentalModel], [RPCMentalModelSpec]: reflect_mission, disposition, reflect.*,
//     mental_models.*; slots REFLECT, SYNTHESIZE, MENTAL_MODEL_DELTA, STRUCTURED_OUTPUT.
//   - [RPCBankConfig] (CreateBank, UpdateBankConfig, UpdateTenantConfig, bank templates): every field.
//
// Everywhere it also rejects a binding for EMBED or the unspecified slot, an unknown slot, a slot bound
// twice in one level, a binding with no model name, a consolidation strategy binding a slot other than
// CONSOLIDATE or DEDUP_CHECK, and a disposition trait outside 0..5 (0 = unset). An unknown rpc is
// domain.ErrInvalidArgument too. [PreviewRPC] maps a PreviewPrompt operation to the RPC whose overrides it
// previews. The server runs the RPCBankConfig check on CreateBank, CreateBankFromTemplate and the
// Update*Config writes, against the patched level, so an invalid binding or trait never reaches storage;
// ResetBankConfig is not checked because it only unsets fields and must be able to repair a stored config.
//
// # FieldMask helpers
//
// Paths are proto field names, which are also the json tags of domain.BankConfig, dotted into nested
// messages ("recall.rrf_k"); lists and maps are leaves and "models" is one path. ValidateOverrides checks
// the leaf paths a config sets against the RPC's allowlist. The Update*Config and ResetBankConfig RPCs apply
// FieldMasks with the server's patch builders (internal/server), which treat an absent mask as the request's
// populated leaves and merge models per slot under it.
//
// # Tests
//
// Table tests per file: defaults_test.go (spec defaults and environment bindings), merge_test.go (set
// semantics, per-slot models, no aliasing), mask_test.go, resolver_test.go (against the generated mockport
// Store: precedence, the strategy level, unknown and tombstoned banks, concurrent use) and validate_test.go
// (the path and slot matrices, bad slots, disposition range, PreviewRPC). SPEC "Model routing", "Per-request
// overrides" and the BankConfig comments in "gRPC surface" govern this package.
package config
