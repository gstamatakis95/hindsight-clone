// Package memory is MemoryService: it turns documents into memory units (Retain), answers queries over them
// (Recall, Reflect), lists and curates individual units without hard deletes (ListMemories, GetMemory,
// UpdateMemory, InvalidateMemory, RestoreMemory), runs the retain_document and rederive_memory jobs, and
// owns two steps of the maintenance tick (RederiveStranded, SettleRetainBatches).
//
// Its one exported type, [Service], is built by [New] from [Deps]. The request and result types the gRPC
// handlers convert to and from are [RetainRequest], [RetainItem], [RetainResult], [RetainedDocument],
// [ListRequest], [Page], [UpdateRequest] and [RestoreRequest]; everything else is unexported.
//
// # Where it sits
//
// memory is a feature package. It imports domain, the boundaries (store, ai, blob, clock), config, jobs, prompts
// and the libraries (recall.Engine, reflect.Agent, timex.Normalizer, linking), and never another feature
// package, grpcapi, maintenance, an adapter, gen, pgx, net/http, the S3 SDK or Datadog (depguard). Its effects
// on other features go through the jobs catalogue: retain and curation enqueue consolidate, graph_maintenance
// and refresh_mental_model jobs inside their own transactions. cmd/memd/wire_memory.go builds the Service,
// hands it to grpcapi's MemoryHandler and MemoryUnitHandler, registers [Service.RunRetainJob] and
// [Service.RunRederiveJob] with the job runner, and gives it to the maintenance tick.
//
// Internally the Service delegates to four unexported parts, each with its own dependencies: the ingestor
// (the API side of Retain), the retainer (one document: the job and the inline run), the curator
// (UpdateMemory) and the rederiver (the rederive_memory job). Recall, ListRecallArms and Reflect delegate to the
// recall engine and the reflect agent unchanged.
//
// # Retain
//
// [Service.Retain] takes a [RetainRequest] (items, document tags, request overrides, skip_consolidation,
// run_async) and:
//
//  1. Resolves the bank's effective config. An unknown bank is not created yet: validation uses the tenant
//     level, and the write transaction creates the bank.
//  2. Plans each item without storing anything: merges request < item overrides (config.Merge), checks that
//     custom mode has a custom_extraction_prompt, normalizes document + item tags, checks the
//     observation-scope fan-out (ALL_COMBINATIONS over max_combinations is domain.ErrTooManyScopes, before
//     anything is stored), generates a document id when the item has none, and pins the content hash, blob
//     key, timestamp (now from clock.Clock when unset; none when timeless) and the resolved chunk sizes in a
//     jobs.RetainPayload. A document id that appears twice in one request is domain.ErrInvalidArgument.
//  3. In one read transaction, rejects a tombstoned bank (domain.ErrBankDeleting, so no blob is written for
//     a deleting bank) and marks an item Unchanged when its document's last completed retain had the same
//     content hash and no-op hash (the overrides plus the item's observation scopes) and force_reextract is
//     not set. An unchanged item writes nothing and makes no model call.
//  4. Stores each changed item's original text in the blob store under its content-addressed key, outside
//     any transaction (a repeated Put of the same key is harmless).
//  5. In one write transaction: Banks().Ensure, tx.Seq(bank) (taken before Enqueue for lock order), upsert
//     each changed document (which clears extracted_at), and for run_async enqueue one retain_document job
//     per changed document (serial key "doc:{id}", DedupeNone, the merged overrides on the job), under a
//     retain_batch parent when more than one document changed.
//  6. A sync retain then runs the retainer inline (jobs.Inline, which reports nothing) for each changed
//     document, in item order, and returns units created and per-slot usage. If a newer retain took the
//     document over meanwhile, the sync call fails with [ErrSuperseded] (domain.ErrConflict, ABORTED; the
//     client may retry).
//
// # retain_document
//
// [Service.RunRetainJob] runs the retainer and records its result (document_id, units_created, unchanged,
// superseded, chunks_total, chunks_skipped) and EXTRACT usage on the jobs.Run. The retainer:
//
//  1. Decodes the payload and resolves the effective config with the job's overrides.
//  2. Reads the document and its committed chunk hashes in a short transaction. Unchanged (same content and
//     no-op hash with extracted_at set, no force_reextract) is a no-op.
//  3. Ownership: the run owns the document while the document's blob key and stored overrides are the
//     run's. If either changed, a newer retain took over and the run stops as superseded (no error for a
//     job). A change of tags alone never supersedes: the run adopts the document's current tags, and the
//     retag_document job queued behind it on the same "doc:" key retags the units it already wrote.
//  4. Reads the pinned blob (its content hash must match the payload, else domain.ErrFailedPrecondition) and
//     chunks it with the pinned sizes. Each chunk's stored hash is salted with the run key (the job id,
//     stable across retries; a fresh id for an inline run), so committed chunks identify this run's own
//     work. Committed hashes are trusted only while the document is in flight and the run's first chunk is
//     among them; then those chunks are skipped and the run resumes exactly after them.
//  5. For each batch of 4 chunks: with no transaction open, extracts each chunk (one EXTRACT call),
//     normalizes dates through the timex.Normalizer against the item timestamp (timeless items get no dates),
//     and embeds all facts in one call. Then one short transaction: tx.Seq(bank), the ownership re-check under
//     the lock (a newer retain, or a rewrite of a chunk this run committed, aborts the run as superseded;
//     changed tags are adopted so this batch's units carry them), the replacement of the document's old units
//     if no earlier batch did it, the chunks, labels and entities (linking.LabelResolver, linking.Resolver),
//     the units stamped with the seq, their entities, and their links (linking.Linker).
//  6. Completes in a final transaction: tx.Seq, the ownership check, the replacement if no batch did it (blank
//     text has no chunks), Documents().Complete (content hash, no-op hash, extracted_at), a RETAIN_COMPLETED
//     event, and the bank's consolidate job ({}, serial key "consolidate", DedupePending) unless
//     skip_consolidation is set or observations.enabled / observations.auto_consolidation is false.
//
// Before each batch the run reports progress (stage "retain_chunks"); a cancelled job or context stops it with
// domain.ErrCancelled.
//
// Replacement (the first batch's transaction): reopen the document if another retain of the same content
// completed it meanwhile (clear extracted_at by re-upserting it as read under the lock, so a run that fails
// after replacing never leaves a partial document stamped complete), list the old units without writing,
// delete the observations citing them and reset their co-sources, delete the units (their link neighbours go
// to graph_dirty, their entities to entity_maintenance) and enqueue graph_maintenance.
//
// retain_batch parents are never run: [Service.SettleRetainBatches], a tick step, ends a parent once all its
// children are terminal.
//
// # Extraction and chunking
//
// The extractor makes one EXTRACT call per chunk through ai.Router and ai.LLM.Structured, with the prompt of
// the mode's strategy: concise (default), verbose, verbatim (the chunk text is the fact; the call adds dates,
// entities and labels), custom (custom_extraction_prompt as guidelines; a blank prompt falls back to concise)
// and chunks (each chunk is one fact, no call). A fact's text is Hindsight's "what | When: ... | Involving:
// ... | why"; a fact without "what" is dropped; fact_type "assistant" becomes experience, "world" world, and
// anything else falls back to fact_kind, then world. Causal parents (at most 2, only earlier facts of the same
// chunk) are kept when extract_causal_links is on (default on).
//
// Chunking splits text into indexed chunks with their SHA-256 and token count: a JSON array of objects is cut
// between turns and JSONL between lines (a turn or line stays whole up to structured_chunk_max_tokens),
// anything else at the most preferred separator present (paragraph, line, sentence, clause, word, character)
// and packed greedily, with chunk_overlap_tokens of overlap. Tokens are counted with [Deps].Tokens (cmd/memd
// wires tiktoken cl100k_base); without one, ceil(runes / 4).
//
// Defaults where the server level leaves a knob unset: chunk_max_tokens 750, chunk_overlap_tokens 0,
// structured_chunk_max_tokens = chunk_max_tokens, entity_match_threshold 0.6 (linking), extract_causal_links
// true, entities_allow_free_form true. Chunk sizes are pinned in the payload at retain, so a config change
// cannot move chunk boundaries under a resuming job.
//
// # Listing
//
// [Service.ListMemories] pages units newest first (created_at, then id) with filters on fact types, a text
// substring, document, entity (live units only), state (live, archived or both), a time range on a chosen
// timestamp, and tags. Page size 0 means 100; larger sizes are capped at 1000. The page token is an opaque
// keyset cursor; a malformed token, an unknown fact type or state is domain.ErrInvalidArgument. No total is
// computed. [Service.GetMemory] reads one unit, live or archived. An unknown or tombstoned bank is
// domain.ErrNotFound.
//
// # Curation
//
// Observations are never curated directly; they re-derive from their sources. Curation never hard-deletes a
// fact: invalidating moves it to the archive and restoring moves it back. [Service.UpdateMemory] takes an
// [UpdateRequest]; [Service.InvalidateMemory] and [Service.RestoreMemory] are UpdateMemory with only State
// set. Validation before any transaction: state invalidated with edits, no state and no edits, a fact type
// other than world or experience, empty text, and clear_occurred together with dates are
// domain.ErrInvalidArgument. Then, still outside the transaction, it reads what the transaction will need: for
// a restore, the archived unit's snapshot entity names (graph maintenance may have pruned those entities while
// the unit was archived); for an entity change, the effective retain.entity_match_threshold with the request
// overrides; for an invalidate, the mental models whose source_ids cite the unit. Then one DB.InTx:
//
// Invalidate (state invalidated):
//
//  1. tx.Seq(bank); read the live fact (absent = domain.ErrNotFound; an observation = INVALID_ARGUMENT);
//  2. delete the observations citing it and reset their co-sources (before the archive, which drops the
//     evidence rows);
//  3. UnitRepo.Archive, which snapshots its entities and causal links, deletes its links and entity rows and
//     queues its link neighbours in graph_dirty and its entities in entity_maintenance;
//  4. an invalidate audit row with the before snapshot and reason;
//  5. enqueue consolidate ({}, DedupePending), graph_maintenance and one refresh per citing mental model
//     (jobs.QueueModelRefresh);
//  6. a memory_curated event stamped with the seq.
//
// No job follows; the response is the unit with state invalidated, no embedding and no operation id.
//
// Restore and/or edit (state valid, or edit fields):
//
//  1. tx.Seq(bank); with state valid, UnitRepo.Restore moves an archived unit back (reattaching the entities
//     and causal links that still exist) and stamps it consolidated; state valid on a live unit is no state
//     change, and with no edits nothing is written and the operation id is empty;
//  2. edits write the new values with edited_at, the seq and a cleared embedding (the unit leaves semantic
//     search until the rederive), stamp the unit consolidated, delete the observations citing it (resetting
//     their co-sources) and enqueue graph_maintenance; a new occurrence date clears Timeless;
//  3. entity changes are committed here, not in the job: the unit's attachments are detached and the new
//     names (or a restore's snapshot names) are resolved with linking.Resolver (no model call) and attached;
//     resolve_entities=false takes edited names literally;
//  4. enqueue rederive_memory (jobs.EnqueueRederive: the unit id only; serial key "doc:{document}" so it never
//     overlaps a retain of that document, or "unit:{id}" for a unit without one; DedupeNone; the request
//     overrides ride on the job);
//  5. restore and edit audit rows (before and after snapshots) and the memory_curated event.
//
// Stamping an edited or restored unit consolidated keeps consolidation from deciding on it while its embedding
// is empty; the rederive's write clears the stamp. Resolving entities in the RPC transaction means an older
// rederive retried after a newer edit can never re-apply stale names.
//
// # rederive_memory
//
// [Service.RunRederiveJob] reports its one progress write (a cancelled job stops there), then the rederiver
// reads the unit and embeds its text outside any transaction, then in one short transaction: tx.Seq(bank) (a
// gone or tombstoned bank fails; it never creates a bank), re-read the unit, and do nothing if it is gone
// (archived or replaced) or its text changed since the embedding was computed (the newer edit's rederive is
// serialized behind it). Otherwise it writes the embedding with the seq (which clears the consolidation
// stamps), deletes and rebuilds the unit's temporal and semantic links, and enqueues graph_maintenance and
// consolidate. Entity attachments are left as the RPC committed them. A malformed payload is
// domain.ErrInvalidArgument; an error after ctx ended is domain.ErrCancelled. Running it twice only repeats
// the same writes.
//
// [Service.RederiveStranded], a tick step, re-enqueues rederive_memory for facts whose rederive ended FAILED or
// CANCELLED and left them without an embedding, 10 minutes after it ended.
//
// # Invariants
//
//   - No model call (extract, time parse, embed) ever runs inside DB.InTx; each batch is prepared first and
//     committed in one short transaction.
//   - Every transaction that changes memory content calls tx.Seq(bank) first, before its reads and its
//     enqueues, and stamps the units it writes with that seq; Seq also row-locks the bank, which serializes
//     ownership checks against other retains and retags.
//   - Idempotent jobs: a retried retain resumes after the chunks it committed itself, replaying a completed
//     one is a no-op, and a rederive run twice repeats the same writes. Overlapping runs of one document never
//     leave stale units: a superseded run stops, and a document whose units are being replaced is in flight
//     until a run completes it.
//   - Every transaction body resets its outputs: the Postgres DB.InTx re-runs it up to 3 times in all after a
//     deadlock (40P01), a serialization failure (40001), or an Attach of an entity that graph maintenance
//     pruned after the resolver read it (a conflict); the re-run resolves the mentions afresh. If every
//     attempt loses, domain.ErrConflict surfaces: a job retries it, an RPC returns ABORTED.
//
// # Tests
//
// Tests run on a pgtest database, fake.Blob, fake.LLM, fake.Embedder and clock.Manual: chunking shapes and
// budgets, extraction per mode, the no-op path (no LLM call), fan-out rejection, a deleting bank, async
// batches, crash and resume (only remaining chunks re-extracted, no facts lost on a changed document),
// overlapping and superseding runs, retags and reprocesses between batches (committed through the store and
// the jobs catalogue, as the document feature commits them), partial replacement leaving the document in
// flight, the retain_document result keys, paging, invalidate, restore (and restore after an entity prune),
// edits, rederive skips, a retried older rederive keeping newer entities, cancellation, graph_maintenance on
// edit and rederive, and the stranded-fact sweep. SPEC "Engines" (retain), "Curation", "Jobs"
// (retain_document, retain_batch, rederive_memory) and "Behavioural rules" (Banks and documents).
package memory
