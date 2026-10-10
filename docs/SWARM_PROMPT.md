# memd swarm: orchestrator, worker and reviewer prompts

This file drives a swarm of coding agents that builds `memd` from `docs/SPEC.md`. It has four parts:

1. **Orchestrator prompt.** One agent. It plans, spawns workers, merges and keeps status.
2. **Worker prompt template.** One agent per work package. Fill in `{{WP_ID}}` and paste the matching card from part 4.
3. **Reviewer prompt.** Reviews every PR, then runs a final adversarial pass.
4. **Work package cards.** The build split into 15 packages with owners, dependencies and acceptance tests.

**Setup before starting**
- Create an empty Git repo with `docs/SPEC.md` (the spec export) and this file at `docs/SWARM_PROMPT.md`.
- Optional but recommended: put the Hindsight source (vectorize-io/hindsight, MIT) read-only at `third_party/hindsight`. Agents port prompts and check behaviour against it.
- Each agent works in its own copy of the repo (a clone, worktree or JuiceFS clone) on its own branch. The orchestrator merges into `main`.
- Agents need Go (1.24 or later), Docker for testcontainers, Python 3.11 or later for Alembic, `buf`, `sqlc`, `mockery` and `golangci-lint` v2.
- Agents don't need the real AI gateway. Every test uses fakes or an `httptest` server that speaks the OpenAI HTTP format.

---

## 1. Orchestrator prompt

```text
You are the orchestrator of a swarm of coding agents building memd, a Go gRPC agent-memory service that
clones Hindsight's memory engine. docs/SPEC.md is the source of truth for behaviour. AGENTS.md (created in
WP-00 from the spec's "AGENTS.md and READMEs" section) is the source of truth for how code is written.
docs/SWARM_PROMPT.md holds the worker template, the reviewer prompt and the work package cards.

YOUR JOB
- Run the work packages (WPs) in dependency order, as many in parallel as the DAG allows.
- Give each worker its prompt: the worker template from docs/SWARM_PROMPT.md part 2 with {{WP_ID}} filled
  in and its card from part 4 pasted below it. Change nothing else in the template.
- Merge finished branches into main, keep main green, keep docs/STATUS.md current, and rule on contract
  change requests.
- Write product code only when you apply an approved contract change (see below). Workers build; you
  coordinate.

DAG (a WP starts once every WP it depends on is merged into main)
  Phase 0  WP-00 contracts and scaffold                         (alone; everything depends on it)
  Phase 1  WP-01 postgres  WP-02 fakes  WP-03 gateway+s3  WP-04 config/routing/timex/prompts
           WP-05 worker runtime  WP-06 server shell  WP-07 eval harness   (all depend on WP-00 only)
  Phase 2  WP-08 retain  WP-09 recall  WP-10 consolidate  WP-11 reflect  WP-12 mental models
           WP-13 bank/curate/docs/maintenance                   (each depends on WP-02 and WP-04)
  Phase 3  WP-14 integration: cmd/memd, worker handlers, e2e, behavioural-rule matrix  (depends on all)
  Phase 4  final review (reviewer prompt, part 3) and fix-up WPs for its findings

MERGE PROTOCOL
1. A worker opens a PR from branch wp/<id>-<slug>. You run the gates on the rebased branch:
   make gen && make lint && make test, plus make e2e for WP-14 and later.
   The PR must leave no diff in gen/, schema/ or internal/mocks/.
2. Spawn a reviewer (part 3, PR mode) on the PR. Merge only when the gates pass and the reviewer reports no
   CRITICAL or HIGH findings. Send MEDIUM findings back to the same worker; record LOW ones in
   docs/STATUS.md.
3. Merge with a merge commit, never a squash. Then tell every running worker whose WP depends on the merged
   one to rebase.
4. If two PRs touch the same file outside their owned directories, one of them broke ownership. Reject it
   and ask for a contract change request instead.

CONTRACT FREEZE AND CHANGE REQUESTS
- After WP-00 merges, these paths are frozen: api/**, internal/port/**, internal/domain/**, migrations/**,
  schema/**, .golangci.yml, buf.yaml.
- A worker that needs a change writes docs/ccr/<WP>-<n>.md with: the problem, the exact proposed diff, which
  WPs it affects and why the spec allows it. It keeps working on everything else.
- You decide within one loop:
  - APPROVE: apply the diff yourself on main in one commit "ccr: <title>", regenerate, run the gates, and
    tell the affected workers to rebase.
  - REJECT: reply with the reason and the alternative.
- Approve only changes the spec supports or a small, obvious gap in it. Never approve scope the spec
  excludes. Log every ruling in docs/STATUS.md.

AMBIGUITY
Workers resolve ambiguity themselves with the simplest reading consistent with the spec, and record it in
docs/decisions/<nnn>-<slug>.md. Review those notes when they arrive. Overrule one only if it contradicts the
spec or another decision. If two decisions conflict, pick one, then tell both workers.

STATUS
Keep docs/STATUS.md with one row per WP (state, branch, PR, blockers, open CCRs, LOW findings) and a log of
merges and rulings. Update it after every event.

STOP CONDITIONS
- Stop spawning and ask the human only when a decision changes the spec's contract in a way no CCR can
  settle, or when a gate fails three times on main for reasons no worker owns.
- Done means:
  - every WP is merged;
  - make gen && make lint && make test && make e2e pass on main;
  - docs/rules.md maps every bullet in the spec's "Jobs", "Curation" and "Behavioural rules" sections to a
    passing test;
  - the final review has no CRITICAL or HIGH findings left.
```

---

## 2. Worker prompt template

```text
You are a coding agent in a swarm building memd, a Go gRPC agent-memory service. You own work package
{{WP_ID}}. Your card is at the end of this prompt.

READ FIRST, IN THIS ORDER
1. AGENTS.md at the repo root (and the AGENTS.md in any directory you own), if it exists yet. WP-00 creates
   them.
2. The SPEC.md sections your card lists under "Read". Also skim "Code standards", "Ports" and
   "Behavioural rules" in full, even if your card doesn't list them.
3. The frozen contracts you build against: api/memory/v1/*.proto, internal/port/*.go, internal/domain/*.go.
4. If third_party/hindsight exists and your card names Hindsight files, read them. They are the reference
   implementation (MIT). Copy behaviour and prompts, not Python structure.

HARD RULES
- Edit only paths your card lists under "Owns". To change a frozen contract (api/, internal/port/,
  internal/domain/, migrations/, schema/, lint config), write docs/ccr/{{WP_ID}}-<n>.md (problem, exact diff,
  affected WPs) and keep working on everything else. Never edit a frozen path yourself.
- Code standards are gates, not advice: McCabe <= 15, lines <= 120 columns, a doc comment on every exported
  identifier, every interface method and every package (doc.go). No //nolint without a reason comment. Split
  a function rather than suppress a linter.
- Every component is an interface. Put `var _ Iface = (*impl)(nil)` next to each implementation. Engines
  depend on port interfaces only. To call another engine, declare the small interface you consume in your
  own package.
- Never call an LLM, the embedder or the reranker inside Store.InTx. Call first, then write in a short
  transaction. Any transaction that changes memory content calls tx.Seq(bank) and stamps the rows it writes.
- Job handlers are idempotent: they re-read state from Postgres and tolerate running twice.
- Prompts live in internal/prompts/templates. Never put prompt text in Go.
- Dependencies: use only these, unless a CCR approves another. grpc-go, google.golang.org/protobuf,
  pgx/v5, pgvector-go, aws-sdk-go-v2 (s3), testcontainers-go, robfig/cron/v3, golang.org/x/sync,
  google/uuid, a tiktoken-compatible tokenizer, and the standard library. Tools: buf, sqlc, mockery,
  golangci-lint.
- Build nothing the spec lists as out of scope: auth, PII, images/audio/files, multilingual, ops CLI, mount,
  MCP, bank aliases, export/import/clone, append mode, client operation ids, per-tenant rate limits.

HOW TO WORK
1. Write the plan in docs/wp/{{WP_ID}}.md: the files you'll create, the interfaces you implement, and every
   acceptance test from your card, each with a test name.
2. Tests first, through interfaces, with fakes from internal/fake (or mocks from internal/mocks before WP-02
   lands). Use table-driven tests where it fits. Use an injected Clock; never call time.Now in logic.
3. Implement until your acceptance tests pass, then run make gen && make lint && make test locally. Run
   go test -race on your packages.
4. When the spec is ambiguous, take the simplest reading consistent with it, write
   docs/decisions/<nnn>-<slug>.md (context, decision, alternatives, spec lines involved) and continue. Don't
   stop to ask unless the choice would change a frozen contract (that's a CCR).
5. Commit in small, buildable steps on branch wp/{{WP_ID}}-<slug>. Rebase on main whenever the orchestrator
   says a dependency merged.

WHEN DONE
Open a PR whose description has:
- what you built, as a checklist mirroring your card's "Build" list;
- each acceptance test and its test name;
- the decision notes and CCRs you filed;
- known gaps, if any, stated plainly.

Then stop. Don't start another package unless the orchestrator assigns one. If the reviewer sends findings
back, fix them on the same branch and push.

YOUR CARD
{{PASTE THE WP CARD HERE}}
```

---

## 3. Reviewer prompt

```text
You review code for memd, a Go gRPC agent-memory service. docs/SPEC.md is the source of truth. You did not
write this code. Be adversarial: look for where it breaks the spec, races, loses data or will fail the
gates later. Don't praise and don't restyle.

PR MODE (input: a PR and its WP card)
Check, in order:
1. Correctness against the SPEC sections the card lists. Read the code; don't trust the PR description.
2. Every acceptance test on the card exists, asserts the behaviour (not just "no error") and passes.
3. Boundaries:
   - the PR only touches the card's owned paths;
   - engines import no adapter, server, worker, gen, pgx, net/http or AWS package;
   - nothing in a transaction calls a model;
   - every content-changing transaction calls tx.Seq.
4. Concurrency: job serialization per (bank, serial key), idempotent handlers, cancellation at checkpoints,
   heartbeats, and what happens when a job runs twice or a worker dies mid-job.
5. Gates: McCabe, 120 columns, doc comments, no unexplained //nolint, no prompt text in Go, no unapproved
   dependency.

Report a table: severity (CRITICAL | HIGH | MEDIUM | LOW), file:line, what is wrong, a concrete failure
scenario, and the fix. CRITICAL or HIGH blocks the merge.

FINAL MODE (input: main after WP-14)
- Review the whole repo against the whole SPEC, section by section, in the same format.
- Also check:
  - every bullet in "Jobs", "Curation" and "Behavioural rules" has a passing test in docs/rules.md;
  - the proto still passes buf lint STANDARD + COMMENTS and buf breaking against the WP-00 tag;
  - make e2e runs retain, consolidation, recall, reflect, mental-model refresh, invalidate and bank delete
    end to end.
- Group the findings into fix-up packages the orchestrator can assign.
```

---

## 4. Work package cards

Each card lists what the package owns, what to read, what it depends on, what to build and the acceptance tests. Section names refer to SPEC.md headings.

### WP-00 · Contracts and scaffold

- **Phase:** 0, alone. Everything depends on it.
- **Owns:** everything below. After merge it is frozen, apart from approved CCRs.
- **Read:** all of SPEC.md. Closely: File structure, Code standards, gRPC surface, Ports, Jobs (claim SQL and indexes), Behavioural rules (change sequence), Decisions.

**Build**
- **Repo skeleton.** Build the full tree from "File structure", with a `doc.go` (package comment) in every Go package. Add `go.mod` (module `memd`), a Makefile (gen, lint, test, e2e, migrate, up, run), `docker-compose.yml` (Postgres with pgvector 0.8 or later, pg_search and pg_trgm; MinIO), `.golangci.yml` exactly as in the spec, `.mockery.yaml`, `buf.yaml` (STANDARD + COMMENTS) and `buf.gen.yaml`.
- **Proto.** Split the spec's single proto into the files listed under `api/memory/v1/`. Add a real comment to every element so COMMENTS passes. Generate into `gen/`.
- **Domain and ports.**
  - `internal/domain`: every type the ports and engines reference, plus `EmbeddingDim = 768`, tag normalization, the length-prefixed ScopeKey encoding, error values, and enums mirroring the proto.
  - `internal/port`: exactly the spec's Ports block, plus the ids, request and hit types it references (`VectorQuery`, `TextQuery`, `TemporalQuery`, `GraphQuery`, `Hit`, `JobKind`, `DedupePolicy`, `Usage`, `ChatRequest`, ...).
- **Engine interfaces.** Put each interface from the Engines blocks in its own engine package as a type-only file, so Phase 2 packages compile against each other from day one.
- **Migrations and SQL.**
  - Alembic `0001`–`0009` as in File structure, raw SQL via `op.execute`. `memory_units.embedding vector(768)`; `banks.change_seq bigint` and `banks.deleting_at`; `seq bigint` on units, observations and events.
  - The `operations` table carries tenant_id, bank_id, kind, status, serial_key, payload, overrides, parent_id, attempt, run_at, worker_id, heartbeat_at, progress, usage, error and timestamps. Also create `lifecycle_events`, `graph_dirty`, `entity_maintenance` and `memory_units_archive`.
  - Indexes: HNSW `vector_cosine_ops`; a pg_search BM25 index on unit text; trigram on entity names; partial `consolidated_at IS NULL AND consolidation_failed_at IS NULL`; a partial pending-claim index on `(run_at) WHERE status = 'pending'`; unique `(tenant_id, bank_id, serial_key) WHERE status = 'processing'`.
  - Dump `schema/schema.sql`. Configure `sqlc.yaml` (queries are written by WP-01).
- **Contract test suites.** Write `internal/porttest/`: suites with real assertions for Store/Tx (including Seq ordering), each repo, Queue/Claimer (dedupe policies, serial keys, run_at, reclaim, cancel bank, deleting banks skipped) and Blob (including DeletePrefix). WP-01 and WP-02 make these pass. Write them from the port comments and the Jobs section.
- **Agent docs.** Root `AGENTS.md` (from the spec block), the four scoped AGENTS.md files, `CLAUDE.md` as a symlink to AGENTS.md, and README skeletons.
- **Mocks.** Generate mockery mocks for every interface.

**Accept**
- `make gen && make lint` pass and `go build ./...` succeeds.
- `buf lint` passes with STANDARD + COMMENTS.
- `alembic upgrade head` then `downgrade base` both succeed on the compose Postgres.
- The schema.sql diff check is clean.
- The porttest suites compile. They fail or skip only because there is no implementation yet, and each skip names the WP that implements it.
- Tag the merge commit `contracts-v1`.

### WP-01 · Postgres adapter

- **Owns:** `internal/adapter/postgres/**`
- **Depends on:** WP-00
- **Read:** Ports, Jobs, Curation, Behavioural rules (change sequence, bank delete), Recall pipeline, Decisions.

**Build**
- `Store`, `Tx` and `Seq`: the `UPDATE banks SET change_seq = change_seq + 1 ... RETURNING`, at most once per tx per bank, with the row lock held until commit.
- Every repo, written as sqlc queries.
- `Reader`:
  - `SearchVector` runs per query: `SET LOCAL hnsw.ef_search = 200`, `hnsw.iterative_scan = strict_order`, `max_scan_tuples`.
  - `SearchText` uses pg_search BM25.
  - `SearchTemporal`.
  - `ExpandGraph` is a recursive CTE capped by hops.
  - `BankSeq`.
- `Queue`: Enqueue with the three dedupe policies on (bank, kind, serial key), Upgrade, Reschedule, CancelBank.
- `Claimer`: the spec's claim SQL verbatim, then the processing update. On a unique-violation, retry the claim. Plus Heartbeat, Progress, Alive, Complete, Fail and ReclaimStale.

**Accept**
- Every porttest suite passes on testcontainers Postgres (same image as compose).
- Serial keys: 8 goroutines claiming 500 jobs over 20 serial keys never hold two PROCESSING rows for one key.
- Seq order: two concurrent transactions; the one that commits later always has the higher seq, and a reader that sees seq S sees every row at or below S.
- Small banks: a bank holding 0.1% of 50k rows still gets 10 semantic hits.
- Reclaim: a stale heartbeat is reclaimed, and the job fails after max attempts.

### WP-02 · Fakes

- **Owns:** `internal/fake/**`
- **Depends on:** WP-00
- **Read:** Ports, Jobs.

**Build**
- In-memory Store/Tx with Seq and real isolation semantics (a mutex per bank is fine).
- Queue/Claimer with the same claim rules.
- An LLM fake that returns scripted responses keyed by `(slot, prompt name)`, records calls, and can be told to fail, time out or return invalid JSON.
- An Embedder that returns deterministic 768-dimension vectors from a text hash, with a "similar texts give similar vectors" mode built from shared token buckets.
- A Reranker with scripted scores, a Blob, and a controllable Clock.

**Accept**
- The same porttest suites pass against the fakes.
- The fakes are documented well enough that engine authors use them without reading their code.

### WP-03 · Gateway and S3 adapters

- **Owns:** `internal/adapter/gateway/**`, `internal/adapter/s3/**`
- **Depends on:** WP-00
- **Read:** Model routing, Ports (models, blobs).

**Build**
- `Structured`: chat completions with `response_format: json_schema`. Validate against the schema, retry with the error on invalid output (bounded by the binding's `max_retries`), and honour `timeout_ms`.
- `Chat` with tools.
- `EmbedDocuments` and `EmbedQuery`: batched, adding nomic's `search_document: ` / `search_query: ` prefixes. Verify 768 dimensions.
- `Rerank`: Cohere/Jina-style body. If every score is in [0,1], pass the scores through; otherwise apply a sigmoid.
- Concurrency limits: a global semaphore plus optional per-slot semaphores. Record usage per call.
- S3 Blob over aws-sdk-go-v2 with `DeletePrefix` (list plus batched delete).

**Accept**
- An `httptest` server emulating `/v1/chat/completions`, `/v1/embeddings` and the rerank route covers: schema retry, tool calls, timeouts, prefixes, the normalization rule both ways, and that the semaphore caps in-flight calls under 100 concurrent callers.
- The S3 suite passes on a MinIO testcontainer, with DeletePrefix run over 2,500 keys.

### WP-04 · Config, routing, time, prompts

- **Owns:** `internal/config/**`, `internal/engine/routing/**`, `internal/engine/timex/**`, `internal/prompts/**`
- **Depends on:** WP-00
- **Read:** Model routing, gRPC surface (BankConfig comments, Per-request overrides), Function signatures.
- **Hindsight files** (under `third_party/hindsight/hindsight-api-slim/hindsight_api/engine/`):
  - `retain/fact_extraction.py`
  - `consolidation/prompts.py` and `consolidation/consolidator.py` (`_DEDUP_PROMPT`)
  - `reflect/prompts.py`
  - `reflect/tools_schema.py`
  - `search/temporal_extraction.py`

**Build**
- `Merge`: field-by-field; `models` merged per slot; other lists replaced. FieldMask apply and clear.
- `ConfigResolver` with strategy precedence: server < tenant < bank < strategy < request.
- `ValidateOverrides`: a per-RPC allowlist that returns INVALID_ARGUMENT on anything else.
- `ModelRouter`.
- `timex`: rules first, `hasTemporalCue`, and the TIME_PARSE fallback only when a cue is found but no rule matches.
- Prompt templates ported from Hindsight:
  - extraction modes concise / verbose / verbatim / custom (keep Hindsight's who / what / when / where / why dimensions);
  - consolidation decide;
  - dedup;
  - reflect system and final;
  - delta and structured delta;
  - single-shot synthesize, written in the same style.

  `Version` = a content hash. Keep the MIT notice in `templates/NOTICE`.

**Accept**
- Merge precedence table tests, including that a request setting only RERANK keeps the bank's other slots.
- Allowlist tests for every RPC.
- A corpus of 60 or more query time expressions (relative, absolute, ranges, none) with expected windows, and a check that no-cue queries make zero LLM calls.
- Every template renders with a fixture.

### WP-05 · Worker runtime

- **Owns:** `internal/worker/**`. `handlers.go` holds stubs only; WP-14 fills them.
- **Depends on:** WP-00
- **Read:** Jobs, Behavioural rules (operations), Function signatures (worker).

**Build**
- Runner: poll every 500 ms; 10 slots with 2 reserved for consolidate; dispatch by kind; heartbeat every 15 s.
- Retry policy per kind: 60 s × attempt, 3 attempts; consolidate retries indefinitely with capped exponential backoff and skips the retry if another consolidate is already pending for the bank. `ErrRetryAt` and `ErrPermanent`.
- `Checkpoint` (progress plus Alive → `ErrCancelled`) and `OperationControl` (cancel, retry).
- `RunMaintenance` every 60 s: reclaim stale jobs, cron refreshes, consolidation reconcile, prune terminal operations and events. No leader.

**Accept**
- With the fake Claimer and Clock:
  - reserved slots hold under a retain flood;
  - retries are scheduled at the right times;
  - a cancelled job stops at its next checkpoint;
  - a worker killed mid-job gets its job reclaimed;
  - three concurrent maintenance loops produce no duplicate enqueues.

### WP-06 · Server shell

- **Owns:** `internal/server/**`
- **Depends on:** WP-00
- **Read:** gRPC surface, Process model (tenancy), Per-request overrides.

**Build**
- Interceptors: tenant from the `x-tenant-id` header, and a default deadline. A missing tenant returns INVALID_ARGUMENT (write a decision note).
- `toStatus`: map every domain error to a gRPC code (NOT_FOUND, FAILED_PRECONDITION for a deleting bank, INVALID_ARGUMENT, UNAVAILABLE, RESOURCE_EXHAUSTED).
- `convert.go`: proto ↔ domain for every message, including FieldMask.
- Every service server, delegating to engine interfaces. Health service.

**Accept**
- A round-trip conversion test for every message (fuzz with random proto values).
- Every RPC is registered and reachable over bufconn with mocks.
- The error mapping table passes.

### WP-07 · Eval harness

- **Owns:** `eval/**`
- **Depends on:** WP-00
- **Read:** Architecture, Operations (eval barrier), Decisions.

**Build**
- LongMemEval and LoCoMo loaders.
- A runner over the gRPC client: one bank per conversation; sync Retain with timestamps; wait until `BankStats.pending_operations_by_kind` is empty; then Recall / Reflect per question.
- An LLM judge through the gateway.
- A per-category report (JSON plus Markdown), and a flag to run the same harness against stock Hindsight's HTTP API for comparison.
- Golden fixtures (`eval/golden/*.yaml`) that CI runs against `memd all` with the fake gateway.

**Accept**
- Runs end to end against a stub server.
- Golden fixtures run in under 2 minutes.
- The report includes per-category accuracy, token usage per slot, and p50/p95 latency.

### WP-08 · Retain engine

- **Owns:** `internal/engine/retain/**`
- **Depends on:** WP-02, WP-04
- **Read:** Feature scope, gRPC surface (retain messages), Engines (retain), Behavioural rules (retain idempotency, banks and documents), Jobs.
- **Hindsight files:** `retain/fact_extraction.py`, `retain/entity_processing.py`, `retain/link_creation.py`, `retain/chunk_storage.py`, `retain/orchestrator.py`

**Build**
- Ingestor, sync and async:
  - validate overrides and scope fan-out (ALL_COMBINATIONS over `max_combinations` returns INVALID_ARGUMENT before anything is stored);
  - store blobs at `{tenant}/{bank}/docs/{doc}/{hash}`;
  - run the Retainer inline, or enqueue `retain_document` children under a `retain_batch` parent.
- Chunker that keeps structured lines and chat turns whole.
- Extraction modes (chunks mode makes no LLM call), fact type by speaker, entity hints merged in, labels.
- Entity resolution: trigram plus co-occurrence plus temporal scoring.
- Linker: temporal up to `temporal_link_cap`; semantic `semantic_link_k` at or above the min similarity; causal.
- Content + overrides hash no-op; replacing a changed document in the first batch's tx; chunk resume.
- `seq` stamping. Enqueue a deduped consolidate unless `skip_consolidation` is set or observations are disabled.

**Accept**
- An unchanged re-retain makes zero LLM calls.
- A crash after chunk 3 of 6, then retry, re-extracts only chunks 4–6.
- Replacement deletes the old units' observations and resets their co-sources.
- Fan-out rejection stores nothing.
- Timeless and timestamp handling.
- Entity merge versus split cases.
- Link caps hold.
- If ExtractedFact needs who/where/why fields to match Hindsight's verbose mode, file a CCR.

### WP-09 · Recall engine

- **Owns:** `internal/engine/recall/**`
- **Depends on:** WP-02, WP-04
- **Read:** Recall pipeline, gRPC surface (recall messages), Engines (recall plus filters).
- **Hindsight files:** `search/retrieval.py`, `fusion.py`, `recall_boost.py`, `reranking.py`, `tags.py`, `tag_resolution.py`, `graph_retrieval.py`

**Build**
- Parse: query embedding, time window, type filters, tags or tag groups compiled to one predicate, fuzzy tag resolution.
- The four retrievers run concurrently; per-arm floors from `min_scores`.
- RRF with k = 60.
- Rerank the top `reranker_max_candidates`.
- Multiplicative boosts: recency α 0.2, temporal α 0.2, proof α 0.1, all from config.
- Budgets 100 / 300 / 1000, fixed or adaptive with the clamp.
- `prefer_observations`.
- Packing to `max_tokens`, skipping items that don't fit.
- Includes (chunks, source facts, entities) within their own token budgets.
- The trace.

**Accept**
- Golden recall tests on fake data:
  - fusion order;
  - each boost isolated;
  - tag group logic (all_of / any_of / none_of, strict variants);
  - min_scores at the arm and result level;
  - prefer_observations backfill;
  - the budget clamp;
  - the packing edge case;
  - include budgets reported as truncated.
- No-cue queries make no TIME_PARSE call.

### WP-10 · Consolidation engine

- **Owns:** `internal/engine/consolidate/**`
- **Depends on:** WP-02, WP-04
- **Read:** Engines (consolidation), Jobs, Curation, Behavioural rules, Model routing (strategy precedence).
- **Hindsight files:** `consolidation/consolidator.py`, `consolidation/prompts.py`

**Build**
- `Consolidator.Run`: take up to `consolidation_max_units_per_run` units (scoped for manual runs). For each unit, decide every scope it belongs to; skip a scope that already cites the unit; stamp the unit in the same tx as its last scope's writes.
- Decide and apply happen outside and inside the tx respectively.
- `observation_versions` and unique evidence with exact quotes.
- Dedup: cosine at or above `dedup_threshold`, then DEDUP_CHECK.
- The per-scope observation cap.
- LLM batches of `consolidation_llm_batch_size`; halve a failing batch; park a unit that fails alone (`consolidation_failed_at`).
- `More` triggers a continuation.
- Trigger refreshes for stale models with `refresh_after_consolidation`.
- ScopeExpander (every mode), StrategyMatcher and preview, Invalidator.

**Accept**
- A unit in 3 scopes, killed after scope 2, then replayed: no duplicate observations, and it is stamped once.
- Halving down to a single poison unit.
- Continuation at the cap.
- A strategy overriding the bank but not the request.
- The dedup merge and keep paths.
- Every scope mode expands correctly.

### WP-11 · Reflect engine

- **Owns:** `internal/engine/reflect/**`
- **Depends on:** WP-02, WP-04
- **Read:** gRPC surface (reflect), Engines (reflect), Behavioural rules (reflect).
- **Hindsight files:** `reflect/agent.py`, `reflect/tools.py`, `reflect/tools_schema.py`, `reflect/prompts.py`, `directives/`

**Build**
- Tools: `search_mental_models`, `read_mental_models`, `search_observations`, `recall`, `expand`, `done`. Declare the small interfaces you need for recall and pages.
- The loop: `max_iterations` scaled by budget; must retrieve before done; citations it never retrieved are dropped.
- A malformed tool call goes back to the model.
- Strict failure: UNAVAILABLE on retrieval errors, fail on an empty answer, fail on a model that can't drive tools; context overflow synthesizes from what it has.
- `max_total_tokens` → RESOURCE_EXHAUSTED.
- Directives selected by tags (`apply_all`, `extra_directives`).
- PromptBuilder over the effective config, with disposition.
- The structured-output pass, the trace and usage.

**Accept**
- One scripted-LLM test per failure rule, plus: citation filtering, the iteration cap per budget, extra directives added (never removing stored ones), and a structured-output error surfaced in `structured_output_error`.

### WP-12 · Mental model engine

- **Owns:** `internal/engine/mentalmodel/**`
- **Depends on:** WP-02, WP-04
- **Read:** Behavioural rules (mental models), gRPC surface (mental models), Engines (mental models plus RefreshRunner and RefreshScheduler), Jobs.
- **Hindsight files:** `mental_model_refresh.py`, `reflect/delta_ops.py`, `reflect/structured_doc.py`, `reflect/prompts.py` (DELTA and STRUCTURED_DELTA)

**Build**
- ChangeDetector over seq and retracted sources.
- Synthesizers: agentic (via a consumer-declared Reflector) and single-shot (recall, then SYNTHESIZE or MENTAL_MODEL_DELTA).
- Delta ops on section and block anchors, with the reject-and-retry-once rule; the document is never replaced by a partial one.
- RefreshRunner:
  - effective mode and strategy, with fallback reasons;
  - reads BankSeq first and sets `last_seq_seen` on success;
  - `source_ids` accumulate across deltas and are pruned on full refresh;
  - history entries for versions and failures;
  - pause on failure, unpause only on a manual success.
- RefreshScheduler: debounce, interval floor, cron; queued-refresh upgrade semantics.
- Dry run writes nothing and returns a diff.
- Page search, folders, clear.

**Accept**
- A late-committing writer is picked up by the next refresh.
- An untagged write never stales a tagged model.
- A delta with all ops rejected keeps the document.
- Nothing in scope means no LLM call.
- Pause and manual unpause.
- Joining a queued refresh with `force_full` upgrades it.
- Both strategies produce a version through the same runner.

### WP-13 · Bank, curation, documents, maintenance engines

- **Owns:** `internal/engine/bank/**`, `internal/engine/curate/**`, `internal/engine/docs/**`, `internal/engine/maintenance/**`
- **Depends on:** WP-02, WP-04
- **Read:** Curation, Behavioural rules (banks and documents), Jobs (delete_bank, graph_maintenance), gRPC surface (bank, documents, memory units).
- **Hindsight files:** `graph_maintenance.py`, `memories/`

**Build**
- Bank engines:
  - templates;
  - directives;
  - BankDeleter: in one tx, set the tombstone, cancel pending jobs and enqueue delete; then the batched delete job, `Blob.DeletePrefix`, and finally the bank row;
  - stats via indexed counts;
  - prompt preview.
- Curation:
  - MemoryLister (both tables, keyset pagination);
  - Curator: edit, invalidate and restore with exactly the in-tx effects in the Curation table;
  - Rederiver.
- Retagger: a set-equal update is a no-op.
- GraphMaintainer over `graph_dirty` and `entity_maintenance` with a 240 s budget and re-enqueue.

**Accept**
- Invalidate: recall stops returning the citing observations immediately, and models that cited them get a refresh queued.
- Restore round-trip.
- Bank delete: queued jobs never resurrect data, writes fail during the delete, and a write afterwards creates a fresh bank.
- The retag no-op.
- Graph maintenance resumes after its budget runs out.

### WP-14 · Integration

- **Owns:** `cmd/memd/**`, `internal/worker/handlers.go`, `test/e2e/**`, `docs/rules.md`, README content
- **Depends on:** every WP above
- **Read:** all of SPEC.md.

**Build**
- `wire.go`, the only composition root, narrowing adapters to ports.
- Modes: api, worker, all.
- Config validation, including the 768-dimension check against the gateway.
- Handler bodies for every job kind.
- Full e2e on docker compose with an emulated gateway: retain (sync and async), consolidation, recall, reflect, mental-model create and refresh with both strategies, invalidate and restore, retag, reprocess, bank delete, cancel and retry, and a worker crash and reclaim.
- `docs/rules.md`: every bullet in Jobs, Curation and Behavioural rules mapped to a test name, with missing tests added.

**Accept**
- `make e2e` is green.
- Every rule is mapped to a passing test.
- The README quick start works from a clean checkout.
