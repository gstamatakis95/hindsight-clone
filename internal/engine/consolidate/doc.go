// Package consolidate turns facts into observations: durable, deduplicated statements per observation
// scope, each citing the facts it rests on with exact quotes. It is the body of the consolidate job and
// also provides scope expansion, consolidation-strategy matching and observation clearing.
//
// # Where it sits
//
// consolidate is an engine package: it imports domain, port, prompts and recall (for recall.TokenCounter
// only) and must never import adapters, server, worker, gen, pgx, net/http, the S3 SDK or Datadog
// (depguard). It declares what it consumes: [Checkpointer] (worker.Checkpoint) and [ScopeLister]
// (port.Reader). It does not import mentalmodel: [RefreshPayload] mirrors the refresh job's payload shape.
// cmd/memd/wire.go builds the [Consolidator] for the worker's consolidate handler, the [ScopeExpander] for
// retain, the [StrategyMatcher] for config.NewResolver and the server's strategy preview, and the
// [Invalidator] for curate's ClearObservations.
//
// # Scopes
//
// A fact consolidates in every observation scope it belongs to; a scope is a tag set
// (domain.ScopeKey). [ScopeExpander.Expand] fans a unit's tags out by mode (unspecified = combined):
// combined (one scope, the full tag set), shared (the shared, untagged scope), per_tag (one per tag),
// all_combinations (every non-empty subset, at most max_combinations, default 15, else
// domain.ErrTooManyScopes, which retain reports synchronously) and custom (explicit tag sets). Untagged
// units have only the shared scope, except in custom mode. A unit's mode is its document's item-level
// observation_scopes, else the document's override default_observation_scopes, else the run config's
// default, else combined; if the mode no longer expands (a lowered cap), the unit falls back to its combined
// scope rather than being stranded.
//
// # Flow of Run
//
// [Consolidator.Run](ctx, job) takes a [Payload] ({} = automatic: every scope, bank config; with scopes =
// a manual run over units tagged with one of those tag sets) and the job's overrides:
//
//  1. Resolve the effective config; with enable_observations false the run does nothing. List the bank's
//     unpaused mental models with refresh_after_consolidation, outside any transaction.
//  2. Plan in one read transaction: up to consolidation_max_units_per_run of the oldest unconsolidated
//     facts (consolidated_at and consolidation_failed_at unset), their scopes, and which scopes already cite
//     each fact (those are skipped, so a replayed run is safe). Scopes are processed in a fixed order.
//  3. Per scope, resolve the scope's config with ForScope (the matching consolidation strategy sits between
//     bank and request) and decide its facts in LLM batches of consolidation_llm_batch_size. For each batch,
//     with no transaction open: find related observations of exactly that scope (per fact, the semantic and
//     keyword arms over port.Reader, interleaved, 10 per fact), render the CONSOLIDATE prompt with those
//     observations and some of their source facts (within consolidation_source_facts_max_tokens_per_observation
//     and consolidation_source_facts_max_tokens), and call CONSOLIDATE with a per-call schema: source_fact_ids
//     limited to the batch's ids with minItems 1, and creates.maxItems = the scope's remaining capacity
//     under max_observations_per_scope. The reply becomes create / update actions (updates only of shown
//     observations; a create repeating a shown text becomes an update of it; empty or source-less actions
//     dropped). New texts are embedded and each create is checked by the [Deduper]: the nearest observation
//     of the scope with cosine >= dedup_threshold (>= 1 disables it) goes to DEDUP_CHECK, and a merge
//     verdict turns the create into an update of that twin. Only the first update of each observation is
//     kept; if one was dropped, the batch is applied but not stamped.
//  4. Apply each batch in one short transaction: tx.Seq(bank) first (it locks the bank, so no edit or retag
//     commits between the re-read and the stamp); re-read the batch's facts and treat any whose seq or tags
//     changed, or that are gone, as not live; write each action whose cited facts are all live and whose
//     target observation still exists in the scope (observation row stamped with the seq, evidence with the
//     facts' exact text as quotes, an observation_versions row with every source); enqueue refreshes of the
//     mental models the scope reaches when something was written; then stamp consolidated_at on the facts
//     whose last scope this is, if every action landed and the facts are still cited in every scope that
//     cited them (a concurrent clear may have reset them without changing their seq).
//  5. Finish in one transaction: tx.Seq, stamp the planned facts that had no scope left to decide (with the
//     same checks), enqueue the continuation ([NewJob] with the same scopes and overrides) when
//     unconsolidated facts remain, and append CONSOLIDATION_COMPLETED.
//
// Facts changed since the plan are never quoted or stamped; the next run re-decides them.
//
// # Failures: halving, parking, circuit breaker
//
// A failing CONSOLIDATE call halves the batch and retries each half; a fact that fails alone is deferred.
// After each top-level batch with deferred facts, a health probe (one CONSOLIDATE call with no facts, on the
// scope's own binding) must succeed; if it fails the run fails with domain.ErrUnavailable and nothing is
// parked (an outage, not poison). Otherwise each deferred fact is retried alone once and those that fail
// again are parked: consolidation_failed_at is stamped in a tx.Seq transaction on facts still unchanged
// since the plan, so a poison fact cannot block the bank. Two top-level batches in a row in which every call
// failed fail the run with domain.ErrUnavailable. Any other error (reads, writes, embeddings, routing) stops
// the run and the job retries; the worker retries consolidate without an attempt cap.
//
// # Mental-model refreshes
//
// When a batch writes an observation, the same transaction enqueues refresh_mental_model ([RefreshPayload],
// serial key "mm:{id}", DedupePending) for every listed model the scope reaches: its tag groups when set,
// else its tags under its tags_match (unset = all_strict for a tagged model, any for an untagged one); the
// shared scope never reaches a tagged model. The job is due after mental_models.refresh_debounce_seconds
// (default 30) or at the end of the model's minimum refresh interval, whichever is later.
//
// # Other types
//
//   - [StrategyMatcher.Match] returns the first consolidation strategy whose pattern matches a scope; the
//     winner applies whole. A pattern with unset mode is all_strict (the shared scope never matches); a
//     pattern with no tags matches only under exact. [StrategyMatcher.Preview] evaluates draft strategies
//     against the bank's existing scopes (the first 1000) without saving.
//   - [Invalidator] clears derived observations in the caller's transaction, taking tx.Seq first.
//     ClearScopes (every scope when the list is empty) is what ObservationService.ClearObservations uses.
//     Curation and docs clear citing observations with EvidenceRepo.DeleteCiting themselves.
//   - [NewJob] builds a consolidate job: the automatic one ({}, no overrides) uses DedupePending and joins a
//     pending consolidate of the bank; one with scopes or overrides is never deduplicated. The serial key is
//     [SerialKey] ("consolidate"): one consolidation at a time per bank.
//
// # Config knobs and defaults
//
// enable_observations (true), consolidation_max_units_per_run (100), consolidation_llm_batch_size (8),
// consolidation_source_facts_max_tokens_per_observation (256), consolidation_source_facts_max_tokens (unset
// = no cap), max_observations_per_scope (unset or negative = uncapped; 0 = no creates), dedup_threshold
// (0.97), observations_mission, default_observation_scopes, consolidation_strategies, the CONSOLIDATE and
// DEDUP_CHECK bindings (overridable only by a Consolidate request or a matching strategy, never by the
// retain that triggered the job) and mental_models refresh_debounce_seconds / min_refresh_interval_seconds.
//
// # Invariants
//
// No model call or embedding runs inside Store.InTx; every write transaction takes tx.Seq(bank) first and
// stamps the observations it writes with it. The job is idempotent: a replayed run skips scopes that already
// cite a fact and never stamps a fact whose decision did not fully land. The Postgres Store.InTx re-runs a
// transaction up to 3 times in all after a deadlock (40P01) or serialization failure (40001); each body
// resets its outputs before running.
//
// # Tests
//
// run_test.go, consolidator_test.go and the stamp, settle and concurrency tests run on a pgtest database with
// fake.LLM and fake.Embedder: replay after a kill, halving down to a poison fact, parking only after a
// successful probe, outages that park nothing, the circuit breaker, edits and retags between scopes,
// cleared or invalidated co-sources, dedup merge and keep, observation caps, manual scopes, document scope
// modes, strategy precedence and refresh triggers. SPEC "Engines" (consolidation), "Jobs" (consolidate) and
// "Model routing" (async slots).
package consolidate
