// Package config builds the effective domain.BankConfig: the server defaults, the level merge, the Resolver
// that reads the stored levels, config validation and the FieldMask patch helpers.
//
// # Where it sits
//
// config imports domain, store and ai only and never an adapter, grpcapi, jobs, library or service. It owns
// consolidation strategy matching ([MatchStrategy]), which the resolver and the strategy preview share. cmd/memd
// builds the server level with [ServerDefaults] from the server config file and the resolver with [NewResolver];
// services and libraries take the concrete *[Resolver]; grpcapi validates every
// config it is given with [Validate] and patches stored levels with [ApplyPaths] and [ResetPaths]; memory's
// Retain uses [Merge] to combine a request's and an item's overrides.
//
// # Levels and merge
//
// A config is assembled from levels, least to most specific:
//
//	server < tenant < bank < strategy < request < item
//
// server is the defaults block of the server config file over the built-in defaults ([ServerDefaults]); tenant
// and bank are the stored levels; strategy is the consolidation strategy matching a scope ([StrategyLevel];
// consolidation only); request is an RPC's overrides message converted to a level; item is a retain item's
// overrides. [Merge] combines them:
//
//   - a set field replaces the field below it; "set" means a non-nil pointer, slice or map, or a non-zero
//     plain string or number, so a non-nil empty list is an explicit clear that shadows inherited values;
//   - the phase groups (retain, observations, recall, reflect, mental_models, models) and other nested
//     messages merge field by field;
//   - a model binding is atomic: the level that supplies it supplies all of it;
//   - every list or map is replaced, never appended.
//
// Model routing. Before merging, each level's models are flattened: every chat-model slot (domain.LLMSlots)
// the level leaves unset takes the level's models.default. So for each slot the most specific level that yields
// a binding, its own or its default, wins: models.default on a bank swaps every chat step of that bank, even
// one the server file binds by name, and models.extract on a request changes one step. models.rerank and
// models.embed are never filled by a default. ai.Router reads the merged result.
//
// Merge deep-copies: the result shares no memory with the inputs, so the server level held by the resolver
// cannot be changed through a result.
//
// # Server defaults
//
// [ServerDefaults](file) merges the server config file's defaults block over the built-in defaults:
//
//   - retain: extraction_mode concise, entities_allow_free_form true, default_observation_scope_modes combined
//     with max_combinations 15, temporal_link_cap 20, entity_match_threshold 0.6;
//   - observations: enabled and auto_consolidation true, dedup_threshold 0.97, llm_batch_size 8,
//     source_facts_max_tokens_per_observation 256, max_units_per_run 100, reconcile_grace_seconds 600;
//   - recall: budget_function fixed, budgets 100 / 300 / 1000 (low / mid / high), budget_min 20, budget_max
//     2000, reranker_max_candidates 300, rerank true, rrf_k 60, recency_alpha 0.2, temporal_alpha 0.2,
//     proof_alpha 0.1;
//   - reflect: disposition 3/3/3, max_iterations 10, search_observations_max_tokens 5000,
//     search_observations_include_entities true, max_answer_tokens 4096;
//   - mental_models: default_mode full, default_strategy agentic, refresh_debounce_seconds 30,
//     min_refresh_interval_seconds 0, history_max_entries 50;
//   - models: embed = [DefaultEmbedModel]; nothing else.
//
// Knobs without a built-in server default stay unset; the package that reads a knob owns its default. A chat
// slot the file binds neither by name nor through models.default stays unbound, and ai.Router rejects calls
// on it (cmd/memd refuses to start unless EXTRACT and REFLECT are bound). Without models.rerank,
// recall.rerank defaults to false so recall skips the cross-encoder instead of failing; the file asking for
// rerank without a binding is an error. The result must pass [ValidateServer].
//
// # Resolver
//
// [NewResolver](store, server) returns the Resolver:
//
//   - Effective(ctx, bank, overrides...) merges server < tenant < bank < overrides (in order).
//   - ForScope(ctx, scope, request) merges server < tenant < bank < strategy < request, where strategy is
//     the first consolidation strategy whose pattern matches the scope, taken from the merged list with the
//     request level included, so a Consolidate call can bring its own strategies ([MatchStrategy]).
//
// Both read the tenant config and the bank row in one short read-only DB.InTx (store.Reader has no config
// reads), so callers must not call them from inside an open transaction. An unknown or tombstoned bank is
// domain.ErrNotFound (the read rule); write paths call Banks().Ensure first, which creates the bank or reports
// domain.ErrBankDeleting. The resolver holds no other state and is safe for concurrent use.
//
// # Validation
//
// What a request may override is fixed by its overrides message (RetainOverrides, RecallOverrides, ...), so
// there is no per-RPC allowlist here. [ValidateServer] checks a level: each disposition trait in 0..5 (0 =
// unset), consolidation strategy names unique, observations.reconcile_grace_seconds not negative, and every set
// model binding naming a model. [Validate] adds the rule for every level below the server: models.embed is
// rejected, because every stored embedding shares one vector space. Failures are domain.ErrInvalidArgument. The
// server runs Validate on request overrides, CreateBank, templates and the patched level of every
// Update*Config and UpdateMentalModel; a reset is not checked, so it can always repair a stored level.
// [CheckArms] checks recall.arms and recall.arm_boosts against the recall arms the server registered; config
// never imports recall, so cmd/memd passes the names (for the server config file) and grpcapi checks every
// RecallConfig a request carries with it.
//
// # FieldMask helpers
//
// Paths are proto field names, which are also the json tags of the domain structs, dotted into nested
// messages ("observations.dedup_threshold"); lists, maps and model bindings are leaves ("models.extract"), and
// no path goes inside a binding. [ApplyPaths] copies masked paths from a patch into a stored level (an unset
// value clears, an empty list is an explicit clear); [ResetPaths] unsets paths so they inherit again, needs at
// least one path, and [ResetAll] ("*") resets everything.
//
// # Tests
//
// defaults_test.go (spec defaults, the file over them, rerank), merge_test.go (precedence, the model-routing
// rule across levels, no aliasing), resolver_test.go (on real Postgres through pgtest: precedence, the strategy
// level, unknown and tombstoned banks, concurrent use) and validate_test.go. The FieldMask helpers are exercised
// through the gRPC API's patch tests (internal/grpcapi/fieldmask_test.go).
package config
