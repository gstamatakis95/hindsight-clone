// Package mentalmodel keeps mental models (knowledge pages) up to date: synthesized markdown documents that
// each answer one source query over a tag and fact-type scope of the bank. It holds the refresh pipeline,
// staleness detection, refresh scheduling and queuing, failure reconciliation, the MentalModelService admin
// RPCs and page search.
//
// # Where it sits
//
// mentalmodel is an engine package: it imports domain, the boundary packages, prompts, recall and reflect (for their
// request types) and must never import adapters, server, gen, pgx, net/http, the S3 SDK or Datadog (depguard);
// recall and reflect never import it. It declares the small interfaces it consumes: [Reflector], [Projector]
// (reflect.Agent), [Recaller] and [TagCompiler] (recall.Engine) and the store.Reader subsets in reads.go
// ([ScopeReader], [ModelLister], [CronSource], [FailureSource]). cmd/memd/wire.go builds:
//
//   - [NewRefreshRunner] with both synthesizers, for the worker's refresh_mental_model handler ([RunJob])
//     and the DryRunRefresh RPC;
//   - [NewAdmin] for the MentalModelService RPCs (SearchPages is served by recall.Engine.SearchPages);
//   - [NewTriggers] (its cron sweep runs in the worker's maintenance tick) and
//     [NewRefreshFailureReconciler] (also run by the tick).
//
// # Staleness and the watermark
//
// Every write that changes memory content stamps its rows with the bank's change seq. A model is stale when
// a live unit or observation with seq > last_seq_seen matches its scope (tags, tag groups, fact types), or
// when one of its source_ids is no longer a live unit (invalidated, or replaced by a reprocess or upsert).
// Untagged writes never make a tagged model stale, even when its refresh reads untagged memories.
// [ChangeDetector.NeedsRefresh] reads BankSeq first, then checks both conditions with no model call.
//
// The scope: tags_match defaults to all_strict for a tagged model and any for an untagged one, tag groups
// override flat tags, and fuzzy tag leaves are resolved to the bank's exact tags (recall's tag compiler)
// before the scope query, so staleness sees exactly what recall would retrieve.
//
// # Refresh pipeline
//
// [RefreshRunner.Run](bank, id, opts) is the one pipeline behind the refresh job and DryRunRefresh:
//
//  1. Plan: load the model; read BankSeq = S (the snapshot bound: everything the refresh reads has seq <= S,
//     so a writer that commits late carries a seq above S and is seen next time); resolve the config as
//     server < tenant < bank < model overrides < trigger knobs < call overrides; pick the [Synthesizer] by
//     strategy (call, else trigger, else mental_models.default_strategy, else agentic) and the mode (trigger,
//     else mental_models.default_mode, else full). A requested delta runs in full with a recorded reason
//     when the call forces full, the model has no content yet, the source query or scope changed (the
//     watermark was reset), or a cited source was retracted.
//  2. Pre-checks with no model call: an automatic run (not manual, not forced, not a dry run) that starts
//     inside the model's minimum refresh interval is re-parked to the end of that interval and writes
//     nothing; an automatic run that finds the model fresh (a duplicate or racing trigger) writes nothing; a
//     scope or window holding nothing completes as NO_NEW_FACTS and only advances last_refreshed_at.
//  3. Draft. Agentic ([NewAgenticSynthesizer]): the reflect loop over the scope, bounded to the snapshot,
//     answering as a document, with the model excluded from its own page tools; a delta runs the loop over
//     the window only and integrates the memories it cites with the delta call. Single-shot
//     ([NewSingleShotSynthesizer]): one recall over the scope and window (observations first, the trigger's
//     budget, default mid), then one synthesize call (full) or one delta call (delta). Both
//     return [ErrNoNewFacts] before any model call when retrieval finds nothing.
//  4. Delta edits: the delta call proposes edit ops over the document's sections and blocks (ids derived
//     from content), applied at once. One retry shows the model the rejected ops when none
//     applied or any named an unknown id. If every op is still rejected the refresh fails and the document
//     stays ([ErrAllOpsRejected]); a document is never replaced by a partial one. A delta that changes more
//     than mental_models.max_delta_ratio of the document (0 = no limit) is redone in full.
//  5. With trigger.response_schema_json set and changed content, the [Projector] (STRUCTURED_OUTPUT) projects
//     the page onto the schema; a failure fails the refresh. The page is embedded for SearchPages (a failure
//     is only a trace warning).
//  6. Write in one short transaction: tx.Seq(bank) first (it locks the bank row and serializes every
//     read-modify-write of model rows), re-read the model and fail with domain.ErrConflict if it changed
//     since the plan (another version, cleared, query or scope edited; the job retries), then set
//     last_refreshed_at, last_seq_seen = S and is_stale false; a full refresh replaces source_ids (a delta
//     accumulates them); changed content moves the old content to history (capped at
//     mental_models.history_max_entries per kind), writes the new version and appends a
//     MENTAL_MODEL_REFRESHED event stamped with the seq. A manual success clears auto_refresh_paused.
//
// A dry run stops before step 6 and writes nothing, not even usage. A failed run writes nothing and the
// document stays; [RunJob] treats a model or bank deleted before the job ran as done.
//
// # Scheduling and queuing
//
// A model has at most one queued refresh (serial key jobs.RefreshSerialKey "mm:{id}", so refreshes of one
// model never overlap). jobs.EnqueueRefresh queues a refresh in the caller's transaction (which must have
// taken tx.Seq first) or joins the queued one and upgrades it: force_full OR-ed, strategy and overrides
// replaced when given, manual set; a manual request also releases a parked refresh (run_at = now).
//
// [RefreshScheduler.Decide] (outside any transaction) skips a paused or fresh model and otherwise parks the
// refresh until the later of now + refresh_debounce_seconds (default 30) and last_refreshed_at +
// min_refresh_interval_seconds (trigger value over config; default 0), as domain.RefreshDueAt computes it.
// jobs.QueueModelRefresh does the same inside a transaction without a config resolver: it reads the model's
// overrides over the bank's own config, skips paused or deleted models, and runs a never-refreshed model now
// and in full; bank templates (first builds) and curation (refreshes after an invalidate) use it. [Triggers]
// enqueues automatic refreshes through the scheduler: EnqueueDueCronRefreshes serves each refresh_cron
// schedule (5 fields, UTC) whose latest fire time within the last 5 minutes has no refresh yet; it is what
// the maintenance tick runs. Consolidation enqueues its refreshes in its own apply transaction.
//
// # Failures and pausing
//
// Job retries are not failures. Once a refresh operation ends FAILED for good,
// [RefreshFailureReconciler.ReconcileFailedRefreshes] (maintenance tick) sets auto_refresh_paused and writes
// a REFRESH_FAILED history entry and event, with a reason classified from the error (delta ops all skipped,
// empty candidate, structured output, retrieval, unexpected). Each model is recorded in its own tx.Seq
// transaction, and failures that predate the model, were followed by a success, or are already recorded are
// skipped, so the reconcile is idempotent. Automatic triggers skip a paused model until a manual refresh
// succeeds; there is no automatic resume.
//
// # Admin and page search
//
// [Admin] serves Create (validates the id, lowercase alphanumerics and hyphens, normalizes tags and folder,
// creates the bank on first write, and queues the first build as a manual full refresh in the same
// transaction), Get (computes is_stale at full detail), List (page size 50, max 500), Update (applies a pure
// [SpecPatch]; a changed source query or scope resets last_seq_seen so the next refresh runs in full),
// Refresh (manual; joins and upgrades a queued one), Clear (empties content, structured output, sources
// and the watermark, keeping the cleared content as a history entry), History, Delete and DeleteFolder
// (recursive; an empty path is rejected). Create, Update, Refresh and Clear take tx.Seq first.
//
// The single-shot synthesize prompt carries the bank's directives that domain.SelectDirectives picks for the model's
// scope filter: the filter the agentic strategy hands reflect, so both strategies apply the same directives.
//
// # Config knobs
//
// mental_models: default_mode (full), default_strategy (agentic), refresh_debounce_seconds (30),
// min_refresh_interval_seconds (0), history_max_entries (50), max_delta_ratio (unset = no limit) and
// max_content_tokens; the reflect knobs and the REFLECT, MENTAL_MODEL and STRUCTURED_OUTPUT
// bindings. A model's stored overrides and its trigger's reflect token knobs (recall_max_tokens,
// search_observations_*) are config levels above the bank; the trigger's mode, strategy, interval floor,
// budget and response schema are read from the trigger directly.
//
// # Invariants
//
// No model call or embedding runs inside DB.InTx; every model-row write takes tx.Seq(bank) first and
// re-reads the row. The Postgres DB.InTx re-runs a transaction up to 3 times in all after a deadlock
// (40P01) or serialization failure (40001); every body resets its outputs. A refresh job that runs twice
// after a reclaim either finds the model fresh or rewrites the same state.
//
// # Tests
//
// Tests run on a pgtest database with fake.LLM, fake.Embedder and clock.Manual: the runner (modes, fallbacks,
// snapshot bound, no-LLM pre-checks, floor re-park, conflicts, dry runs), the planner (delta retry and
// rejection), markdown splitting and edit application, diffs, scheduling and queuing, cron, failure
// reconciliation, admin RPCs and page search. SPEC "Behavioural rules" (Mental models), "Engines" (mental
// models), "Jobs" (refresh_mental_model) and "Model routing" (MENTAL_MODEL).
package mentalmodel
