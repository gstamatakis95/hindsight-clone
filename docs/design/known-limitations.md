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
- **Workaround:** always send `document_id`, use `run_async` (one operation per document), or RetainBatch (a status
  per item).

### A redelivery that differs only outside the no-op hash is absorbed
- **What:** the no-op hash covers the content, the overrides and the observation scopes only. An item that repeats
  a completed document with a new context, timestamp or metadata is unchanged, and a RetainBatch item that repeats a
  document in flight joins the running retain, whose payload keeps the earlier values (a different tag set does not
  join; it queues a new retain).
- **Impact:** those fields can't be changed by re-sending the same content.
- **Workaround:** `force_reextract`, or ReprocessDocument; RetagDocument for tags.

### The RetainBatch backlog gate is approximate
- **What:** the gate counts pending `retain_document` rows before the call writes anything, without a lock, and does
  not count processing ones. Concurrent calls that all pass it can overshoot the limit by their own sizes.
- **Impact:** the backlog can exceed `ingest.max_pending_per_bank` or `max_pending_per_org` by up to one batch per
  concurrent caller.
- **Workaround:** none needed; set the limits with that headroom in mind.

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

### Models whose every source was retracted stay stale
- **What:** they keep the dead source ids and re-run (with no model call) at each interval floor until new
  in-scope facts arrive.
- **Workaround:** a manual full refresh once the scope has facts again, or delete and recreate the model.

### Missed cron fires are skipped
- **What:** a fire time missed by more than 5 minutes (service down, ticks stalled) is skipped until the next one.
  Two racing ticks can enqueue one extra refresh, which costs one no-call run.

## Org deletion

DeleteOrg removes everything memd stores for an org ([storage.md](storage.md#deleting-an-org)); these copies are
outside its reach.

### Postgres keeps dead tuples, WAL and backups
- **What:** deleted rows stay in table and index pages (HNSW, BM25 segments, GIN) until VACUUM, in the WAL, and in
  every backup, PITR archive, replica and snapshot until their retention expires.
- **Impact:** the data is unreachable through memd but physically present for a while.
- **Workaround:** autovacuum (or `VACUUM` on the large tables for strict deadlines); backup retention within the
  legal deadline; after a restore, re-run DeleteOrg for every org whose hash matches a receipt completed after the
  backup was taken (keep the receipts table outside the backup set).

### Versioned buckets keep old blob versions
- **What:** with object versioning or replication on, deleting a key leaves its noncurrent versions, and the
  listing that verifies the org prefix does not see them.
- **Workaround:** a deployment requirement: no versioning, or a noncurrent-version expiry rule within the deadline.

### Broker topics, gateway and provider logs, Datadog and container logs
- **What:** ingest topics and dead-letter topics keep message content until broker retention; the AI gateway and
  model providers may log or retain prompts; Datadog keeps span tags (`memd.tenant`, `memd.org`, `memd.bank`),
  operation ids and error text; container stdout and stderr keep error text with ids.
- **Workaround:** retention within the deadline on each; gateway content logging off and provider zero-retention
  agreements; ids that are not personal data.

### A crash between a blob write and its clean-up can leave one blob
- **What:** a retain that passed its org check before the tombstone writes its blob, and deletes it again when its
  write is refused. A process crash between the two, after the org's blob sweep, leaves that blob behind. The blob
  grace (`deletion.blob_grace`, at least the RPC deadline) makes this rare.
- **Workaround:** none built; list `{tenant}/{org}/` for orgs with a completed receipt to find such a blob.

### The receipt is pseudonymous, not anonymous
- **What:** a receipt names its org by `sha256(tenant || 0x00 || org)`. A short, guessable org id can be found by
  hashing candidates.
- **Workaround:** use org ids that are not personal data.

### Verification scans the join tables
- **What:** the final check counts the org's rows in every table with `org_id`; `unit_entities`,
  `entity_cooccurrences`, `memory_links` and `observation_evidence` have no index starting with `(tenant_id,
  org_id)`, so their counts are sequential scans.
- **Impact:** the last step of a delete reads those tables in full once (no locks that block writers).

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

### Every ListTags page aggregates the whole source
- **What:** each page groups every tag of the source (memories, mental models or documents) and then cuts the page
  after the token, so a page costs as much as the whole listing.
- **Impact:** slow tag listings on banks with very many tagged memories.
- **Workaround:** narrow with `prefix`; use a large `page_size`.

### Operations echo their overrides, outside the response size limit
- **What:** an Operation carries its `effective_overrides`, and ListOperations pages and RetainBatch responses (up to
  `ingest.max_batch_items` operations) do not end early by size the way the listings of large items do.
- **Impact:** many operations with large overrides (long missions or custom prompts) can make a response pass the
  client's 4 MiB receive limit.
- **Workaround:** a smaller `page_size` or smaller batches; raise the client's receive limit.

### Mental-model history pages by offset
- **What:** ListMentalModelHistory pages with an offset token, so a version written while a client pages can repeat
  one entry at a page boundary (never skip one); the history is at most `history_max_entries` per kind.
- **Workaround:** dedupe on `created_at` when paging a model that is refreshing.

### Reflect does not force a tool sequence
- **What:** the loop requires *some* retrieval first, not Hindsight's mental models → observations → recall
  order; `directive_compliance` is not checked.

## Queue ingestion

### A failing Kafka record holds back its partition
- **What:** Kafka commits one offset per partition, so while a record waits out its Nacks, nothing after it in its
  partition is committed, even records of other keys that were acked. Once the partition buffers
  `MaxBuffered` records (default 10 000) behind it, fetching that partition pauses for every key.
- **Impact:** a crash or rebalance meanwhile redelivers the acked records after the stuck one (retain absorbs them as
  unchanged, joined or stale); a long-failing record stalls its partition.
- **Workaround:** dead-letter after a bounded number of attempts, and give the topic enough partitions that one
  stuck partition is a small share of the traffic.

### Kafka delivery attempts restart after a rebalance
- **What:** Kafka keeps no delivery count, so `Message.Attempt` counts this consumer's deliveries since it was
  assigned the partition, and starts again at 1 after a rebalance or a restart. A rebalance between a dead-letter
  produce and its settlement also puts the record on the dead-letter topic twice.
- **Impact:** a record that keeps failing across restarts is retried more than the maximum attempts before it is
  dead-lettered; dead-letter consumers may see duplicates.
- **Workaround:** none needed for correctness; deduplicate dead letters by their `memd-dlq-topic`,
  `memd-dlq-partition` and `memd-dlq-offset` headers.

### No consumer lag metric
- **What:** memd exports no consumer-group lag (the end offset minus the committed offset per partition).
- **Workaround:** read it from the brokers' tooling (kafka-consumer-groups, rpk) or the broker's own metrics; the
  `memd.ingest.received` and `memd.ingest.acked` rates show the consumer's throughput.

### A queued message re-creates a deleted bank
- **What:** a message for a bank being deleted is retried (`BANK_DELETING` is nacked with backoff), so once the
  delete finishes, its retry creates the bank afresh, as a write over the API does.
- **Workaround:** stop producing for a bank before deleting it, or delete the whole org (an org delete drops its
  messages).

### Messages for a re-used org id can be dropped
- **What:** a message for an org that is not live and has a deletion receipt is acked and dropped. A producer that
  starts publishing for a re-used org id before the org is created again loses those messages.
- **Workaround:** create the org (OrgService.CreateOrg) before producing for it.

### An unpinned source trusts the envelope's tenant
- **What:** without `MEMD_INGEST_TENANT`, every envelope names its own tenant and the consumer believes it.
- **Workaround:** pin the source, or make the broker's publish permissions the tenant boundary (a topic per tenant).

### A source without a delivery count never dead-letters for attempts
- **What:** `ingest.consumer.max_attempts` relies on `Message.Attempt`; a source that reports 0 (unknown) retries a
  failing message forever, with the backoff capped at `backoff_max`. Both built-in sources report it.

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
