# 005 — Port types the spec references but doesn't define

## Context
The card asks for "exactly the spec's Ports block, plus the ids, request and hit types it references".
The block references several types it never defines: `BankRepo`, `MentalModelRepo`, `SQLPredicate`,
the query and hit types, `JobKind`, `DedupePolicy`, `Usage`, the chat types, and a per-kind max-attempts
rule. The porttest suites need concrete semantics for all of them. This note lists what WP-00 chose, so
any downstream CCR starts from an honest baseline.

## Decision
- **Identity types are aliases of domain types.** `port.BankRef`, `ScopeKey`, `UnitID`, `DocumentID`,
  `ChunkID`, `EntityID`, `OperationID`, `MentalModelID`, `DirectiveID`, `ModelSlot`, `ModelBinding`,
  `Usage` and `JobKind` (= `domain.OperationKind`) all alias domain types. `domain.Unit` and
  `domain.Document` must carry their bank and ids, and domain may not import port. With aliases,
  `port.UnitID(x)` and `port.BankRef{Tenant, Bank}` still read exactly as in the spec.
- **`BankRepo`** has Get, Ensure, Create, SetConfig, MarkDeleting, Delete, TenantConfig and
  SetTenantConfig.
  - Ensure implements "a write to an unknown bank creates it". It returns `ErrBankDeleting` for a
    tombstoned bank.
  - Tenant config lives here because `Tx` has no tenant repo and TenantService needs storage.
- **`MentalModelRepo`** has Get, Create, Update, Delete, AppendHistory (with a per-kind `keep` cap) and
  History.
- **`Tx.Seq`** returns `ErrNotFound` for an unknown bank and `ErrBankDeleting` for a tombstoned one.
  Writes therefore fail with FAILED_PRECONDITION during a delete, and queued jobs can't resurrect data.
  Calling Seq twice in one tx returns the same value.
- **`SQLPredicate`** is a backend-neutral boolean tree (`tags` leaf / `and` / `or` / `not`) over
  normalized tag names, with an `Eval` method that serves as the reference semantics.
  - The spec's name is kept. The Postgres adapter renders the tree to one WHERE fragment, and the fake
    evaluates it in memory.
  - Engines never see SQL text, which keeps depguard's intent.
- **Query types** (`VectorQuery`, `TextQuery`, `TemporalQuery`, `GraphQuery`) carry Bank, Types, Tags
  (`*SQLPredicate`), Limit, the arm floor (`MinSimilarity` / `MinScore`) and a `SeqRange` (After, UpTo).
  - The `SeqRange` is what lets a mental-model refresh read "a snapshot holding everything at or below S"
    and "only what changed since last_seq_seen" without a later port change.
  - `Hit` is (ID, Score), and a higher score is better.
- **Jobs** (amended by decision 012, orchestrator ruling, review round 1).
  - Dedupe matches (bank, kind, serial key) only among joinable rows (`operations.joinable`): rows
    enqueued with a dedupe policy and no overrides, plus every refresh_mental_model row (R1).
  - `Attempt` counts earlier failed or reclaimed runs and is 0 on the first run.
  - `domain.OperationKind.MaxAttempts()` returns 3 for every kind and 0 (unbounded) for consolidate.
  - ReclaimStale fails a job when attempt+1 >= max; the runner caps `Fail` itself (R10').
  - Upgrade shallow-merges the payload (booleans OR-ed) and replaces overrides only when given (R2).
  - Reschedule acts on PENDING rows only and returns `ErrFailedPrecondition` otherwise (R3).
  - Progress, Complete and Fail are fenced to PROCESSING rows (R7). Cancelling a processing row only
    records the request, so it keeps its serial key until the handler stops (R8').
  - `Alive` is true only while PROCESSING without a cancel request, and stays true for a delete_bank
    job of a tombstoned bank.
- **Chat types** follow the OpenAI chat-completions shape: `ChatMessage`, `ToolSpec`, `ToolCall` (raw
  JSON arguments, possibly malformed), `ChatResponse` and `ChatRole`. `StructuredRequest` and
  `ChatRequest` also carry a `Name` (prompt template name), which the scripted LLM fake keys on
  ("(slot, prompt name)" in WP-02's card).
- **Write-side ports the spec clearly requires were added** in review round 1 (decision 012):
  `Tx.Directives()` / `DirectiveRepo`; `UnitRepo.Update`, `SetDocumentTags`, `AppendAudit` and
  `RecordObservationVersion`; `EntityRepo.PendingMaintenance` and `PruneOrphans`; `LinkRepo.Count`,
  `Dirty` and `ClearDirty`; `BankRepo.DeleteBatch`; `Queue.Get`, `List`, `Cancel`, `Retry` and `Prune`;
  `EventRepo.Prune`.
- **Reader** is still exactly the spec's six methods. Expected CCRs for API-path reads:
  - document get / list (WP-13, WP-06);
  - memory list over both tables and observation history (WP-13);
  - stats and tag counts (WP-13);
  - directive selection by tags (WP-11, WP-13; today via `DirectiveRepo.List` in a tx);
  - scope listing and per-scope unconsolidated counts (WP-10);
  - mental-model list / search / page reads and the max seq in a model's scope (WP-12);
  - the reprocess operation id on archive and audit rows of replaced units (WP-08).

## Alternatives
- Copy the spec's `type BankRef struct{...}` into port and duplicate it in domain. Rejected: domain types
  could not reference it, and two identical structs need conversions everywhere.
- Make SQLPredicate a SQL string plus args. Rejected: the fake can't evaluate it, and engines would build
  SQL.
- Add the API-path read methods to Reader now. Rejected: the card says "exactly the spec's Ports block";
  CCRs keep those additions reviewed.

## Spec lines involved
Ports block (`Tx.Banks()`, `Tx.MentalModels()`, `port.SQLPredicate` in TagFilterCompiler), Jobs
(claim SQL, dedupe column, retries, reclaim), Behavioural rules (bank delete, change sequence, mental
model watermark).
