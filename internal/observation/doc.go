// Package observation turns facts into observations: durable, deduplicated statements per observation scope,
// each citing the facts it rests on with exact quotes. [Service] serves ObservationService (manual
// consolidation, scope listing, an observation's history, clears and the consolidation-strategy preview),
// runs the consolidate job ([Service.RunConsolidateJob]) and the maintenance sweep that queues consolidation
// for banks with facts left waiting ([Service.ReconcileConsolidation]).
//
// # Where it sits
//
// observation is a feature package. It imports domain, the boundaries (store, ai, clock), config, jobs and
// prompts, and never another feature, an adapter, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It
// reports progress through the jobs.Run it runs under, and enqueues its continuation, graph_maintenance and
// the mental-model refreshes it triggers through the jobs catalogue, so it never imports mentalmodel.
// cmd/memd/wire_services.go builds the Service, hands it to grpcapi's ObservationHandler, registers
// RunConsolidateJob for the consolidate kind and gives the maintenance tick its consolidation_reconcile step.
//
// # Scopes
//
// A fact consolidates in every observation scope it belongs to; a scope is a tag set
// (domain.ScopeKey). domain.ExpandScopes fans a unit's tags out by mode (unspecified = combined):
// combined (one scope, the full tag set), shared (the shared, untagged scope), per_tag (one per tag),
// all_combinations (every non-empty subset, at most max_combinations, default 15, else
// domain.ErrTooManyScopes, which retain reports synchronously) and custom (explicit tag sets). Untagged
// units have only the shared scope, except in custom mode. A unit's mode is its document's item-level
// observation_scope_modes, else the document's override default_observation_scope_modes, else the run config's
// default, else combined; if the mode no longer expands (a lowered cap), the unit falls back to its combined
// scope rather than being stranded.
//
// # The consolidate job
//
// [Service.RunConsolidateJob] takes a jobs.ConsolidatePayload ({} = automatic: every scope, bank config; with
// scopes = a manual run over units tagged with one of those tag sets) and the job's overrides, and records
// the result keys processed, changed_scopes and more plus the model usage on the operation:
//
//  1. Resolve the effective config; with observations.enabled false the run does nothing. List the bank's
//     unpaused mental models scheduled after_consolidation, outside any transaction.
//  2. Plan in one read transaction: up to observations.max_units_per_run of the oldest unconsolidated
//     facts (consolidated_at and consolidation_failed_at unset), their scopes, and which scopes already cite
//     each fact (those are skipped, so a replayed run is safe). Scopes are processed in a fixed order.
//  3. Per scope, resolve the scope's config with ForScope (the matching consolidation strategy sits between
//     bank and request) and decide its facts in LLM batches of observations.llm_batch_size. For each batch,
//     with no transaction open: find related observations of exactly that scope (per fact, the vector and
//     text searches of store.Reader, interleaved, 10 per fact), render the CONSOLIDATE prompt with those
//     observations and some of their source facts (within source_facts_max_tokens_per_observation and
//     source_facts_max_tokens), and call CONSOLIDATE with a per-call schema: source_fact_ids limited to the
//     batch's ids with minItems 1, and creates.maxItems = the scope's remaining capacity under
//     max_observations_per_scope. The reply becomes create / update actions (updates only of shown
//     observations; a create repeating a shown text becomes an update of it; empty or source-less actions
//     dropped). New texts are embedded and each create is checked for a near-duplicate: the nearest
//     observation of the scope with cosine >= dedup_threshold (>= 1 disables it) goes to DEDUP_CHECK, and a
//     merge verdict turns the create into an update of that twin. Only the first update of each observation
//     is kept; if one was dropped, the batch is applied but not stamped.
//  4. Apply each batch in one short transaction: tx.Seq(bank) first (it locks the bank, so no edit or retag
//     commits between the re-read and the stamp); re-read the batch's facts and treat any whose seq or tags
//     changed, or that are gone, as not live; write each action whose cited facts are all live and whose
//     target observation still exists in the scope (observation row stamped with the seq, evidence with the
//     facts' exact text as quotes, an observation_versions row with every source); enqueue refreshes of the
//     mental models the scope reaches when something was written; then stamp consolidated_at on the facts
//     whose last scope this is, if every action landed and the facts are still cited in every scope that
//     cited them (a concurrent clear may have reset them without changing their seq).
//  5. Finish in one transaction: tx.Seq, stamp the planned facts that had no scope left to decide (with the
//     same checks), enqueue the continuation when unconsolidated facts remain, and append
//     CONSOLIDATION_COMPLETED.
//
// Facts changed since the plan are never quoted or stamped; the next run re-decides them. A run's
// continuation is jobs.EnqueueScopedConsolidate: the automatic one ({}, no overrides) uses DedupePending and
// joins a pending consolidate of the bank; one with scopes or overrides is never deduplicated. The serial key
// is "consolidate": one consolidation at a time per bank. The job never re-enqueues itself from the handler.
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
// the run and the job retries; the job runner retries consolidate without an attempt cap.
//
// # Mental-model refreshes
//
// When a batch writes an observation, the same transaction enqueues refresh_mental_model (jobs.EnqueueRefresh: payload
// jobs.RefreshPayload with the model id only, serial key "mm:{id}", DedupePending) for every listed model the scope
// reaches (domain.MentalModelSpec.ScopeFilter: its tag groups when set, else its tags under the scope's match mode,
// unset = all_strict for a tagged model, any for an untagged one); the shared scope never reaches a tagged model. The
// job is due at domain.RefreshDueAt: after mental_models.refresh_debounce_seconds (default 30; an unset or non-positive
// value counts as the default here) or at the end of the model's minimum refresh interval, whichever is later.
//
// # RPCs
//
//   - [Service.Consolidate] queues a manual consolidation. It is a write: Banks().Ensure, then tx.Seq, then
//     enqueue. Without scopes or overrides it is the automatic job ({}, DedupePending); with either it carries
//     a jobs.ConsolidatePayload and/or the overrides and uses DedupeNone (jobs.EnqueueScopedConsolidate).
//   - [Service.ListObservationScopes] pages the bank's observation scopes with counts, keyed by the scope's
//     canonical string; the shared (untagged) scope's empty key maps to the cursor "0", which sorts before
//     every tagged key, so paging past it never restarts the listing. Page size 0 is 100, the cap is 1000.
//   - [Service.ListObservationHistory] pages an observation's versions newest first, keyed by version (unique
//     per observation); page size 0 is 100, the cap is 1000, and an id with no versions is domain.ErrNotFound.
//   - [Service.ClearObservations] takes a [ClearRequest] with exactly one of Scopes, MemoryID and All
//     (domain.ErrInvalidArgument otherwise; the API also rejects an empty scope list and all = false, so a
//     clear never defaults to every scope). For scopes or all it takes tx.Seq, deletes every observation of
//     the scopes (EvidenceRepo.DeleteScopes, or DeleteAllObservations for all) and resets their sources,
//     enqueues graph_maintenance and, when Reconsolidate is set, enqueues consolidation of the same scopes
//     (the automatic job for all). For a memory it deletes the observations citing that live fact (absent =
//     domain.ErrNotFound), resets their co-sources and makes the fact itself consolidate again (rewrites it
//     with the seq, which clears its stamps) unless it has no embedding yet because its rederive is pending.
//     [ClearResult] reports the count and the queued operation. Clears do not queue mental-model refreshes;
//     affected models go stale through the change seq.
//   - [Service.PreviewConsolidationStrategies] evaluates draft strategies against one page of the bank's existing
//     scopes (paged as ListObservationScopes pages them) without saving: per strategy, the page's scopes
//     config.MatchStrategy would let it win and those an earlier strategy shadows. The winner of a scope applies whole; a pattern with unset mode is
//     all_strict (the shared scope never matches); a pattern with no tags matches only under exact.
//
// # Maintenance sweep
//
// [Service.ReconcileConsolidation] runs as the tick's consolidation_reconcile step. It lists the banks with
// a fact waiting for consolidation (store.Maintenance.UnconsolidatedBanks) and, for each bank whose effective
// observations.auto_consolidation and observations.enabled are not false and whose oldest waiting fact is
// older than observations.reconcile_grace_seconds (default 10 minutes), enqueues the automatic consolidate
// with jobs.EnqueueAutoConsolidate (DedupePendingOrRunning) in its own short transaction. It takes no change
// seq, skips banks that are gone or being deleted, and returns how many jobs it inserted.
//
// # Config knobs and defaults
//
// The observations group: enabled (true), auto_consolidation (true), reconcile_grace_seconds (600),
// max_units_per_run (100), llm_batch_size (8), source_facts_max_tokens_per_observation (256),
// source_facts_max_tokens (unset = no cap), max_observations_per_scope (unset or negative = uncapped; 0 = no
// creates), dedup_threshold (0.97), mission and strategies; retain.default_observation_scope_modes; the CONSOLIDATE
// and DEDUP_CHECK bindings (overridable only by a Consolidate request or a matching strategy, never by the
// retain that triggered the job) and mental_models refresh_debounce_seconds / min_refresh_interval_seconds.
//
// # Invariants
//
// No model call or embedding runs inside DB.InTx; every write transaction takes tx.Seq(bank) first and
// stamps the observations it writes with it. The job is idempotent: a replayed run skips scopes that already
// cite a fact and never stamps a fact whose decision did not fully land. The Postgres DB.InTx re-runs a
// transaction up to 3 times in all after a deadlock (40P01) or serialization failure (40001); each body
// resets its outputs before running.
//
// # Tests
//
// The tests run on a pgtest database with fake.LLM and fake.Embedder: replay after a kill, halving down to a
// poison fact, parking only after a successful probe, outages that park nothing, the circuit breaker, edits
// and retags between scopes, cleared or invalidated co-sources, dedup merge and keep, observation caps, manual
// scopes, document scope modes, strategy precedence, refresh triggers, the job's result keys, the clears
// (counts, seq order, graph_maintenance, reconsolidation, a fact waiting for its rederive), scope paging, the
// strategy preview, history paging and the reconcile sweep's config filter and grace. Design:
// docs/design/consolidation.md.
package observation
