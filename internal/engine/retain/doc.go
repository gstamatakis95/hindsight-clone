// Package retain turns documents into memory units: the api side of the Retain RPC ([Ingestor]), the
// retain_document job ([Retainer]), ReprocessDocument ([Reprocessor]), and the pieces both use: chunking,
// extraction, entity and label resolution, and linking.
//
// # Where it sits
//
// retain is an engine package: it imports domain, the boundary packages, jobs and prompts and must never import
// adapters, server, gen, pgx, net/http, the S3 SDK or Datadog (depguard). It declares what it consumes from other
// engines: [DateNormalizer] (timex.TimeNormalizer), [ScopeExpander] (consolidate.ScopeExpander), [ConfigMerger]
// (config.Merge) and [TokenCounter]; its jobs go through the jobs catalogue. Other engines reuse its pieces: curate
// (EntityResolver, Linker), maintenance (Linker) and docs (Reprocessor). cmd/memd/wire.go builds everything; the
// server's Retain RPC calls [Ingestor.Ingest], the retain_document job handler calls [Retainer.ProcessDocument],
// and DocumentService.ReprocessDocument reaches [Reprocessor.Reprocess] through docs.
//
// # Ingest: the Retain RPC
//
// [Ingestor.Ingest] takes an [IngestRequest] (items, document tags, request overrides, skip_consolidation,
// run_async) and:
//
//  1. Resolves the bank's effective config. An unknown bank is not created yet: validation uses the tenant
//     level, and the write transaction creates the bank.
//  2. Plans each item without storing anything: merges request < item overrides, checks that custom mode
//     has a custom_extraction_prompt, normalizes document + item tags, checks the observation-scope fan-out
//     (ALL_COMBINATIONS over max_combinations is domain.ErrTooManyScopes, before anything is stored),
//     generates a document id when the item has none, and pins the content hash, blob key, timestamp (now
//     from clock.Clock when unset; none when timeless) and the resolved chunk sizes in a jobs.RetainPayload.
//     A document id that appears twice in one request is domain.ErrInvalidArgument.
//  3. In one read transaction, rejects a tombstoned bank (domain.ErrBankDeleting, so no blob is written for
//     a deleting bank) and marks an item Unchanged when its document's last completed retain had the same
//     content hash and no-op hash ([NoopHash]: the overrides plus the item's observation scopes) and
//     force_reextract is not set. An unchanged item writes nothing and makes no model call.
//  4. Stores each changed item's original text in the blob store under its content-addressed key, outside
//     any transaction (a repeated Put of the same key is harmless).
//  5. In one write transaction: Banks().Ensure, tx.Seq(bank) (taken before Enqueue for lock order), upsert
//     each changed document (which clears extracted_at), and for run_async enqueue one retain_document job
//     per changed document (serial key "doc:{id}", DedupeNone, the merged overrides on the job), under a
//     retain_batch parent when more than one document changed. The maintenance tick settles the
//     parent once its children are terminal.
//  6. A sync retain then runs [Retainer.ProcessDocument] inline for each changed document, in item order,
//     and returns units created and per-slot usage. If a newer retain took the document over meanwhile,
//     the sync call fails with [ErrSuperseded] (domain.ErrConflict, ABORTED; the client may retry).
//
// # Retainer: retain_document
//
// [Retainer.ProcessDocument] is the job handler and the inline runner. The flow:
//
//  1. Decode the payload and resolve the effective config with the job's overrides.
//  2. Read the document and its committed chunk hashes in a short transaction. Unchanged (same content and
//     no-op hash with extracted_at set, no force_reextract) is a no-op.
//  3. Ownership: the run owns the document while the document's blob key and stored overrides are the
//     run's. If either changed, a newer retain took over and the run stops as Superseded (no error for a
//     job). A change of tags alone never supersedes: the run adopts the document's current tags.
//  4. Read the pinned blob (its content hash must match the payload, else domain.ErrFailedPrecondition) and
//     chunk it with the pinned sizes. Each chunk's stored hash is salted with the run key (the job id,
//     stable across retries; a fresh id for an inline run), so committed chunks identify this run's own
//     work. Committed hashes are trusted only while the document is in flight and the run's first chunk is
//     among them; then those chunks are skipped and the run resumes exactly after them.
//  5. For each batch of [DefaultBatchChunks] (4) chunks: with no transaction open, extract each chunk (one
//     EXTRACT call), normalize dates through the DateNormalizer against the item timestamp (timeless items
//     get no dates), and embed all facts in one call. Then one short transaction: tx.Seq(bank), re-check
//     ownership under the lock (a newer retain, or a rewrite of a chunk this run committed, aborts the run as
//     Superseded; changed tags are adopted so this batch's units carry them), replace the document's old
//     units if no earlier batch did, insert the chunks, resolve labels and entities, insert the units stamped
//     with the seq, attach entities, and link.
//  6. Complete in a final transaction: tx.Seq, the ownership check, the replacement if no batch did it (blank
//     text has no chunks), Documents().Complete (content hash, no-op hash, extracted_at), a
//     RETAIN_COMPLETED event, and the bank's consolidate job ({}, serial key "consolidate", DedupePending)
//     unless skip_consolidation is set or enable_observations / auto_consolidation is false.
//
// Before each batch the run reports progress through its jobs.Run (stage "retain_chunks"); a cancelled job or
// context stops it with domain.ErrCancelled. A sync retain runs under jobs.Inline, which reports nothing.
//
// Replacement (the first batch's transaction): reopen the document if another retain of the same content
// completed it meanwhile (clear extracted_at by re-upserting it as read under the lock, so a run that fails
// after replacing never leaves a partial document stamped complete), list the old units without writing,
// delete the observations citing them and reset their co-sources, delete the units (their link neighbours
// go to graph_dirty, their entities to entity_maintenance) and enqueue graph_maintenance ({}, serial key
// "graph", DedupePendingOrRunning).
//
// # Extraction, entities and links
//
// [Extractor] makes one EXTRACT call per chunk through ai.Router and ai.LLM.Structured, with the
// prompt of the mode's [ExtractionStrategy]: concise (default), verbose, verbatim (the chunk text is
// the fact; the call adds dates, entities and labels), custom (custom_extraction_prompt as guidelines;
// a blank prompt falls back to concise) and chunks (each chunk is one fact, no call). A fact's text is
// Hindsight's "what | When: ... | Involving: ... | why"; a fact without "what" is dropped; fact_type
// "assistant" becomes experience, "world" world, and anything else falls back to fact_kind, then world.
// Causal parents (at most 2, only earlier facts of the same chunk) are kept when extract_causal_links is on
// (default on).
//
// [EntityResolver] maps free-form mentions plus caller hints to entities inside the batch transaction, with
// no model call: trigram candidates are gated (same kind when both are known, a similarity floor, word-by-word
// agreement, distinct numbers never merge), scored as 0.5 x name similarity + 0.3 x co-occurrence with the
// fact's other entities + 0.2 x temporal proximity (7-day linear falloff), an identical name scoring 1, and
// merged into the best candidate at or above entity_match_threshold (inclusive), else a new entity is
// created. resolve_entities=false takes hint names literally (exact case-insensitive match only).
// [LabelResolver] keeps only labels declared in entity_labels (allowed values matched case-insensitively and
// stored in the declared spelling), upserts one label entity per (key, value) and returns the "key:value"
// tags of labels declared with write_to_tags, which are added to the unit's tags.
//
// [Linker] adds edges for new or re-derived facts in the caller's transaction: semantic links to the nearest
// facts with cosine >= semantic_link_min_similarity, up to semantic_link_k per unit; temporal links to the
// facts nearest in time within temporal_link_window_hours, up to temporal_link_cap per unit, weight falling
// linearly from 1 to 0.3 (timeless units get none); and causal links cause -> effect, uncapped. Temporal and
// semantic links are symmetric and written both ways; only the linked unit's side is capped.
// [Linker.LinkWith] takes the caller's effective config; [Linker.Link] reads the stored tenant < bank tuning.
//
// chunkWith splits text into indexed chunks with their SHA-256 and token count: a JSON array of objects is
// cut between turns and JSONL between lines (a turn or line stays whole up to structured_chunk_max_tokens),
// anything else at the most preferred separator present (paragraph, line, sentence, clause, word, character)
// and packed greedily, with chunk_overlap_tokens of overlap. Tokens are counted with the injected
// [TokenCounter] (cmd/memd wires tiktoken cl100k_base); approxTokens, ceil(runes / 4), is the default.
//
// # Reprocess
//
// [Reprocessor.Reprocess] makes no model call. In one transaction it takes tx.Seq(bank) first (which locks
// the bank and fails on a tombstoned one), reads the document (unknown = domain.ErrNotFound), replaces its
// stored overrides when given, rebuilds a forced payload from the stored blob key, tags and item scopes, and
// the context, metadata, mention time and timeless flag of the newest never-curated unit (entity hints are not
// stored and are lost), re-upserts the document and enqueues retain_document (serial key "doc:{id}",
// DedupeNone). Re-extraction replaces the units, which resets their curation.
//
// # Config knobs and defaults
//
// The run reads, from the effective BankConfig: extraction_mode, custom_extraction_prompt, retain_mission,
// entity_labels, entities_allow_free_form (default true), default_observation_scopes, enable_observations
// and auto_consolidation (default true), the EXTRACT and TIME_PARSE bindings, and RetainTuning. Engine
// defaults where the server level leaves a knob unset: chunk_max_tokens [DefaultChunkMaxTokens] (750),
// chunk_overlap_tokens 0, structured_chunk_max_tokens = chunk_max_tokens, entity_match_threshold
// [DefaultEntityMatchThreshold] (0.6), temporal_link_window_hours [DefaultTemporalLinkWindowHours] (24),
// temporal_link_cap [DefaultTemporalLinkCap] (20), semantic_link_k [DefaultSemanticLinkK] (50),
// semantic_link_min_similarity [DefaultSemanticLinkMinSimilarity] (0.7), extract_causal_links true. Chunk
// sizes are pinned in the payload at ingest, so a config change cannot move chunk boundaries under a
// resuming job.
//
// # Invariants
//
//   - No model call (extract, time parse, embed) ever runs inside DB.InTx; each batch is prepared first
//     and committed in one short transaction.
//   - Every write transaction calls tx.Seq(bank) first and stamps the units it writes with that seq; Seq also
//     row-locks the bank, which serializes ownership checks against other retains and retags.
//   - Idempotent jobs: a retried job resumes after the chunks it committed itself, and replaying a completed
//     job is a no-op. Overlapping runs of one document never leave stale units: a superseded run stops, and
//     a document whose units are being replaced is in flight until a run completes it.
//   - The Postgres DB.InTx re-runs a batch transaction up to 3 times in all after a deadlock (40P01), a
//     serialization failure (40001), or an Attach of an entity that graph maintenance pruned after the
//     resolver read it (a conflict); the re-run resolves the mentions afresh. If every attempt loses,
//     domain.ErrConflict surfaces: a job retries it, a sync Retain returns ABORTED.
//
// # Tests
//
// Tests run on a pgtest database, fake.Blob, fake.LLM and fake.Embedder: chunking shapes and budgets, extraction per
// mode, entity scoring and merge gates, labels, link caps, the no-op path (no LLM call), fan-out rejection,
// a deleting bank, async batches, crash and resume (only remaining chunks re-extracted, no facts lost on a
// changed document), overlapping and superseding runs, retags between batches, partial replacement leaving
// the document in flight, and reprocess payloads. SPEC "Engines" (retain), "Jobs" (retain_document,
// retain_batch) and "Behavioural rules" (Banks and documents).
package retain
