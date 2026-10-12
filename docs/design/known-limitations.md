# Known limitations

Each entry says what happens, its impact, and the workaround. None of them loses committed facts unless the entry
says so.

## Retain and documents

### Sync retains longer than the call deadline never complete
- **What:** an inline (sync) run never resumes; the client's retry is a new run that re-extracts every chunk. A
  document whose extraction takes longer than the deadline (`api.rpc_deadline`, 120 s, when the client sets none)
  fails every time.
- **Impact:** large documents can't be retained synchronously; partial memories stay until a run completes.
- **Workaround:** `run_async` (the job resumes from its committed chunks) or a longer client deadline.

### A multi-item sync retain that fails part-way reports only an error
- **What:** all changed documents are upserted (marked in flight) first, then extracted one by one. If item k
  fails, items before it are committed, later items stay in flight with no memories, and the response is an error
  with no per-item result. Items sent without a `document_id` got generated ids the client never sees.
- **Impact:** a retry of items without ids creates new documents; the earlier in-flight rows remain.
- **Workaround:** always send `document_id`, or use `run_async` (one operation per document).

### Old content blobs are kept until the bank is deleted
- **What:** each distinct content of a document is stored under its own key (`.../docs/{id}/{content_hash}`);
  re-retaining changed content never deletes the previous object (`blob.Store` has no single-key delete). A
  retain that races DeleteBank can also leave one orphan blob after the delete's prefix sweep.
- **Impact:** object storage grows with every content change.
- **Workaround:** none in the API; a bucket lifecycle rule or an out-of-band sweep of keys not referenced by
  `documents.blob_key`.

### Reprocess drops entity hints
- **What:** entity hints and `resolve_entities` are not stored, so a reprocess runs without them. Replacing a
  document's memories writes no audit rows; the curation history of replaced memories is their earlier audit and
  archive rows.
- **Impact:** hint-driven entity links may differ after reprocess.
- **Workaround:** re-retain with the original hints (and `force_reextract`) instead of reprocessing.

### Reprocess requests never coalesce
- **What:** each ReprocessDocument enqueues its own `retain_document` (no dedupe).
- **Impact:** repeated calls queue repeated full re-extractions (they run one at a time per document).
- **Workaround:** check for a pending operation before reprocessing again.

### Retag edge cases
- **What:** (a) a tag that is both a document tag and a label tag (`key:value`) can't be told apart; removing it
  from the document removes it from the memories. (b) If a content-changing retain failed or is still pending, a
  later retag leaves `old ∪ new` document tags on the memories that retain left behind until a retain or reprocess
  of the document completes.
- **Impact:** (a) a lost label tag; (b) memories visible in a tag scope the user removed.
- **Workaround:** avoid label values equal to document tags; reprocess the document after a failed retain.

### Restoring a memory archived before a reprocess duplicates it
- **What:** restore moves the archived memory back next to the document's re-extracted memories.
- **Impact:** the same fact can appear twice.
- **Workaround:** invalidate the duplicate.

## Jobs and operations

### Shutdown releases and reclaims spend attempts
- **What:** a job cut off by shutdown is released with an immediate retry that still counts as an attempt; a
  reclaim after a crash counts too. Three rolling deploys or crashes during one capped job end it FAILED.
- **Impact:** a long job can fail without a real error. `consolidate` is uncapped; `delete_bank` is resumed by the
  maintenance tick.
- **Workaround:** RetryOperation; keep `terminationGracePeriodSeconds` ≥ 50 s (60 s recommended) so handlers
  finish within the grace.

### Terminal writes carry no worker token
- **What:** Complete and Fail are fenced by status and by a prior `Alive` check, not by worker id. A stale worker
  whose job was reclaimed and claimed elsewhere can land a terminal write in the gap between its `Alive` check and
  the write.
- **Impact:** very narrow; handlers are idempotent, so at worst a status is recorded by the wrong run.
- **Workaround:** none needed in practice; heartbeat self-fencing makes the case rare.

### Consolidate can occupy every free slot
- **What:** 2 slots are reserved for consolidate, but consolidate may also use any free slot.
- **Impact:** with many banks consolidating, a worker can run only consolidation for a while.
- **Workaround:** more worker replicas or `MEMD_WORKER_SLOTS`.

### A permanently failing bank delete re-queues every tick
- **What:** the tick re-enqueues `delete_bank` for any tombstoned bank without an active one, with no backoff.
- **Impact:** one FAILED operation per tick until the cause is fixed (pruned after `retention.operations`). The
  bank stays tombstoned: writes FAILED_PRECONDITION, reads NOT_FOUND.
- **Workaround:** fix the cause (usually object-store access); the next run resumes where it stopped.

### Bank deletion removes blobs one key at a time
- **What:** the delete lists a page of keys under the bank prefix and removes each listed key with its own
  `DeletePrefix` call, so the fence is re-checked between pages; `blob.Store` has no bounded bulk delete.
- **Impact:** on S3 each key costs a list and a delete request, so deleting a bank with many documents is slow.
- **Workaround:** none needed for correctness; the delete is resumable and runs in the background.

### Delete completion is visible only as NOT_FOUND
- **What:** the DeleteBank operation can reach COMPLETED while a continuation still runs, and every operation of
  the bank vanishes with the bank.
- **Impact:** a client that waits for COMPLETED may act before the delete has finished.
- **Workaround:** poll GetOperation with the returned id until it is NOT_FOUND.

### retain_batch parents are coarse
- **What:** a parent settles within one tick after its last child; a child retried after the parent settled does
  not reopen it; the parent carries no usage (its children do).
- **Workaround:** list the children (ListOperations) for usage and current status.

### Progress is coarse for refresh, rederive and retag
- **What:** these handlers report one start checkpoint; there is no progress between the turns of a long agentic
  refresh. Cancellation still reaches them through the heartbeat.

### The tick resolves the config of every bank with a waiting fact
- **What:** the consolidation reconcile resolves the effective config (tenant, org and bank read) of every bank that has
  an unconsolidated fact, once per tick, to apply its `auto_consolidation`, `enabled` and grace settings.
- **Impact:** with very many such banks the tick does one short read per bank per minute on every worker.
- **Workaround:** none needed at current scales.

## Consolidation and observations

### Dedup checks creates only
- **What:** an *updated* observation that drifts into a near-duplicate of another observation is not merged.
- **Impact:** occasional near-duplicate observations in a scope.
- **Workaround:** ClearObservations for the scope with `reconsolidate`.

### Counts can include facts waiting for a rederive
- **What:** a fact without an embedding (an edited or restored fact waiting for its rederive, or one whose rederive
  failed) is skipped by consolidation, but a retag job resets its stamps like every other memory of the document.
  It is then counted in `unconsolidated_memories` and scope counts, and makes observations of its scope `stale`.
- **Impact:** counts can stay above zero while nothing is consolidatable, until the rederive writes the embedding
  (a stranded fact is retried by the tick every 10 minutes).

### Entity co-occurrence counts only grow
- **What:** co-occurrence counts are bumped on attach and never decremented when memories are removed (pruned
  entities lose their rows).
- **Impact:** a slightly inflated co-occurrence signal in entity resolution.

## Mental models

### A refresh queued before a pause still runs
- **What:** pausing marks the model; an automatic refresh already queued is not cancelled and the run does not
  check the pause.
- **Impact:** one extra automatic refresh after a failure.

### In-transaction refresh scheduling ignores tenant- and org-level knobs
- **What:** refreshes queued from inside another transaction (invalidate, edit, CreateBank from a template) take
  debounce and interval floor from the model's schedule and the bank's own config, then the defaults, skipping
  the tenant and org levels.
- **Impact:** a tenant- or org-level `mental_models.refresh_debounce_seconds` or `min_refresh_interval_seconds`
  does not apply to those refreshes.

### Orgs can't be deleted yet
- **What:** OrgService.DeleteOrg answers UNIMPLEMENTED. An org, once created, stays, and so does its config level.
- **Workaround:** delete the org's banks one by one with DeleteBank; the empty org row remains.

### Models whose every source was retracted stay stale
- **What:** they keep the dead source ids and re-run (with no model call) at each interval floor until new
  in-scope facts arrive.
- **Workaround:** a manual full refresh once the scope has facts again, or delete and recreate the model.

### Missed cron fires are skipped
- **What:** a fire time missed by more than 5 minutes (service down, ticks stalled) is skipped until the next one.
  Two racing ticks can enqueue one extra refresh, which costs one no-call run.

## Recall, reflect and parsing

### Time-cue false positives
- **What:** "I have 2000 cats" reads as the year 2000; "this spring water" as a season.
- **Impact:** a stray window narrows the temporal arm and the temporal boost; it never filters results.
- **Workaround:** pass an explicit time window.

### An empty tag-group leaf is no filter
- **What:** an empty non-exact leaf matches everything (Hindsight makes an empty `any_strict` leaf match nothing).
- **Workaround:** omit empty leaves.

### List RPCs take only flat tag filters
- **What:** ListMemories, ListDocuments, ListMentalModels and ListDirectives accept a `TagFilter` with flat tags
  only; groups there are INVALID_ARGUMENT.
- **Workaround:** use Recall (or several listings) for boolean tag logic.

### Reflect does not force a tool sequence
- **What:** the loop requires *some* retrieval first, not Hindsight's mental models → observations → recall
  order; `directive_compliance` is not checked.

## Out of scope

memd matches Hindsight's memory engine except for these, which it does not provide:

- Hard delete of a single memory or document (invalidate a memory, or delete the bank).
- Bank aliases and clone; an export / import of memories (templates carry config, directives and mental-model
  specs, not memories).
- An append update mode for documents, client-supplied operation ids, and provider batch APIs for extraction.
- Authentication, per-tenant rate limits, and security or PII scanning.
- Images, audio, files and multilingual handling; agent tooling such as an ops CLI, mounts or an MCP server.

## Platform

- **Prompt versions are not recorded** on operations or calls: a template edit is visible only in the code
  history.
- **Embedding and rerank usage** is not attributed to operations or responses; only LLM chat and structured usage
  is. With Datadog metrics on, the token counters see every call.
- **One embedding model** for the deployment (768 dimensions); changing it requires re-embedding everything,
  which memd does not do.
- **One shared HNSW index** for all banks; per-bank partial indexes are not built. Stats and tag counts are COUNT
  queries without counter tables. Per-tenant rate limits are not built.
- **Config writes move `last_memory_write_at`** (they take the bank lock through the change sequence). It is
  display-only.
- **A stored config that no longer validates** blocks every update of that level until ResetBankConfig or
  ResetTenantConfig removes the bad field.
- **Stored config shapes are not migrated.** A change to the stored JSON shape of a config, override or refresh
  setting comes with no data migration: recreate the database.
