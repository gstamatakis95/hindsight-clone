# WP-14b PR: integration

Branch `wp/WP-14b-integration`, rebased on main `c8eebee` (WP-17 merged). The plan is in `docs/wp/WP-14b.md`.

## Built
- [x] **`wire.go`, the only composition root.**
  - Builds the adapters (Postgres pool, S3, gateway) and narrows them to a `ports` struct of interfaces.
  - Builds every engine once and hands them to the gRPC server, the handlers and the maintenance tick.
- [x] **Modes `api`, `worker`, `all`.**
  - Graceful stop on SIGINT and SIGTERM.
  - gRPC reflection is registered.
  - Readiness probes: a Postgres ping and a gateway reachability check (cached, no model call).
- [x] **Config validation.** Env + flags (decision 1450), the decision 1411 names, one `MEMD_STALE_AFTER`
  value, heartbeat below 3/4 of StaleAfter. The 768-dimension embedding check fails startup.
- [x] **Handler bodies for every job kind** (decision 1451).
- [x] **Full e2e on docker compose with the emulated gateway:**
  - retain, sync and async;
  - consolidation;
  - recall and reflect;
  - mental-model create and refresh, agentic and single-shot;
  - invalidate, restore and edit;
  - retag and reprocess;
  - bank delete;
  - cancel and retry;
  - a worker crash with reclaim.
- [x] **`docs/rules.md`.** Every bullet and table row of SPEC "Jobs", "Curation" and "Behavioural rules" is
  mapped to existing test names, checked by a script. Tests added where a rule had none:
  - no model call inside a tx;
  - every curation calls tx.Seq (e2e, change_seq);
  - the edit flow (e2e);
  - retain_batch aggregation.

**Carried wiring items**
- [x] `store.Read()` is passed wherever an engine takes the batch reads:
  - recall's Reader;
  - reflect `DefaultTools(rc, pages, r)`;
  - WP-12 Scope, Models, Cron and Failure sources;
  - WP-13 sources (lister, stats, documents, scopes, citing models, archived);
  - `MaintenanceDeps.Consolidation`.
- [x] Worker:
  - one StaleAfter;
  - delete_bank uncapped;
  - consolidate never re-enqueues;
  - delete_bank and graph_maintenance re-enqueue;
  - the graph re-check;
  - RefreshFailureReconciler and CronRefresher (Triggers) in RunMaintenance;
  - the stranded-fact rederive step.

  The `handlers.go` stub comment is gone.
- [x] Curator gets Resolver, Config, Archived, Citing and the Refresh queuer.
- [x] The bank deleter gets the blob store as `DeleterConfig.Blobs` and the Claimer as its JobFence.
- [x] Rederive gets the embedder and linker.
- [x] The ObservationClearer is `consolidate.NewInvalidator()`.
- [x] The RefreshQueuer is wired into the curator and templates; `retain.NewReprocessor` into documents.
- [x] `reflect.NewStructuredProjector` is the Projector.
- [x] Server:
  - admin types are aliased (no adapters);
  - CreateBankFromTemplate calls `CreateNamed` and returns `mental_model_builds`;
  - `IngestResult.Usage` is copied into `RetainResponse.usage`;
  - `ValidateOverrides` is wired, with the PreviewPrompt and mental-model spec names fixed (decision 1454);
  - empty repeated override fields stay nil.
- [x] Tokenizer: tiktoken cl100k_base (`github.com/tiktoken-go/tokenizer` v0.7.0, embedded tables) for recall,
  retain and consolidate.
- [x] `ServerDefaults`: `retain.entity_match_threshold` = 0.6.
- [x] The golden eval fixtures run against `memd all` with the emulator. Every check passes with no fixture
  or emulator tuning.

## Accept
| Item | Test | Result |
|---|---|---|
| `make e2e` green: every scenario, against real Postgres + MinIO and the emulator | `e2e.TestScenarios/{RetainSync, RetainAsync, ConsolidationRecallReflect, MentalModelBothStrategies, InvalidateRestore, RetagReprocess, CancelRetry, WorkerCrashReclaim, BankDelete}`, `e2e.TestMemdSmoke`, `e2e.TestStackUpDown`, `e2e.TestGoldenAgainstMemdAll` | pass (`make e2e`, 61 s) |
| Worker crash and reclaim | `e2e.TestScenarios/WorkerCrashReclaim`: SIGKILL mid-extraction, restart, reclaim after the 6 s stale threshold; completes with attempts = 1 | pass |
| `docs/rules.md` maps every rule to a passing test | `docs/rules.md` | pass (all names exist; `make test` and `make e2e` green) |
| README quick start works from a clean checkout | run by hand on a fresh compose project: `make up`, `make migrate`, gateway emulator, `make run`, then grpcurl Retain (2 units, EXTRACT usage), Recall, Reflect and GetBankStats | pass |
| `make gen && make lint && make test && make e2e` on the rebased branch | `make gen` (no diff), `make lint BREAKING_AGAINST=contracts-v1` (0 issues), `make test` (all ok, Postgres suites included), `make e2e` | pass |

| Carried item | Test | Result |
|---|---|---|
| Wiring end to end (all engines, fakes, emulator) | `memd.TestWiredStackNoModelCallInsideTransaction` | pass |
| ValidateOverrides names | `server.TestRealValidatorAcceptsServerRPCNames` | pass |
| Empty repeated override is not a clear | `server.TestEmptyRepeatedOverrideIsNotAClear` | pass |
| CreateNamed | `server.TestEveryRPCForwards` (CreateBankFromTemplate) | pass |
| Retain usage copied | `e2e.TestScenarios/RetainSync` | pass |
| consolidate doesn't re-enqueue | `worker.TestConsolidateHandlerNeverReenqueuesOnMore` | pass |
| delete_bank continuation and uncapped retry | `worker.TestDeleteBankReenqueuesUntilDone`, `worker.TestDeleteBankRetriesWithoutAttemptCap` | pass |
| graph_maintenance re-enqueue and re-check | `worker.TestGraphHandlerReenqueuesAndRechecks` | pass |
| Stranded-fact rederive tick | `worker.TestMaintenanceRederivesStrandedFacts`, `postgres.TestStrandedFacts` | pass |
| Result and usage on the operation | `worker.TestRetainHandlerReportsUsage` | pass |
| Config, 768 dims, tokenizer | `memd.TestLoadConfigEnvAndFlags`, `memd.TestValidateRejects`, `memd.TestUsage`, `memd.TestNormalizeDSN`, `memd.TestCheckEmbedding`, `memd.TestTokenCounter` | pass |
| entity_match_threshold 0.6 | `config.TestServerDefaults` | pass |
| Golden fixtures against memd all | `e2e.TestGoldenAgainstMemdAll` | pass |

## Decisions and CCRs
- 1450: process configuration, modes and startup checks.
- 1451: job handlers (continuations, graph re-check, delete_bank without an attempt cap).
- 1452: the stranded-fact rederive reconcile, an adapter query outside the ports.
- 1453: retain_batch settlement. Found by e2e: parents stayed PENDING forever and the barrier never emptied.
- 1454: server wiring (override names, aliases, operation reads, tokenizer).
- No CCRs. Nothing under api/, internal/port/, internal/domain/, migrations/ or schema/ changed.

## Outside the card's Owns (additive, flagged for review)
- `internal/adapter/postgres/{stranded,settle}.go`, `queries/{stranded,settle}.sql` and the generated
  `db/` files.
  - Two worker-only maintenance queries, consumed through worker interfaces (decisions 1452, 1453).
  - Not port methods, so no contract change.
  - Each has a Postgres test.
- `internal/worker/maintenance.go`: two step registrations and two `MaintenanceDeps` fields (the card's "add
  a tick step").
- `internal/mocks/worker` regenerated for the two new worker interfaces.
- `go.mod` gains `github.com/tiktoken-go/tokenizer` (the allowed tiktoken-compatible tokenizer).

## Known gaps
- **The graph re-check runs as the handler's last step, not after Complete.** A removal that commits in the
  milliseconds between the handler returning and Complete joins the finishing row. Its rows wait for the
  next removal (decision 1451).
- **A crash-reclaimed delete_bank still counts toward the attempt cap.** Past 3 reclaims the row fails;
  re-issuing DeleteBank resumes the delete (decision 1306).
- **A retain_batch parent settles on the next maintenance tick** (60 s in production), so an async-retain
  barrier can wait up to one tick after the last child. A child retried after its parent settled does not
  reopen it (decision 1453).
- **Audit and archive rows of replaced units are not tagged with the reprocess operation id.** Carried from
  WP-08 and WP-16; no port method. Listed in `docs/rules.md`.
- **The ChangeDetector misses fuzzy matches below 0.45.** It still resolves fuzzy scopes at recall's default
  trigram floor, because `NewChangeDetector` takes no ConfigResolver. That is an engine change (WP-16 known
  gap).
- **The e2e suite runs on the emulator.** It proves the wiring, the job flow and the failure handling, not
  answer quality. The golden fixtures only check presence (eval README).
- **The gateway readiness probe only checks reachability.** It makes no model call; the startup embedding
  check covers the model.
