# WP-14b plan — Integration

Branch `wp/WP-14b-integration`. Decisions 1450-1454.

## Files
- `cmd/memd/main.go`, `config.go`, `wire.go`, `glue.go`, `tokens.go`. `wire.go` is the only composition
  root: it opens the Postgres pool, S3 and the gateway, narrows them to a `ports` struct, and builds every
  engine once. It serves gRPC (api), runs the runner and maintenance (worker), or both (all).
- `internal/worker/handlers.go`: handler bodies for every job kind.
- New worker files:
  - `stranded.go`: the NULL-embedding rederive step;
  - `batches.go`: retain_batch settlement.
  - Small `maintenance.go` edits register both steps.
- `internal/server`: wiring-only edits.
  - Type aliases.
  - `CreateNamed`.
  - Retain usage.
  - The override RPC names.
- `internal/config/defaults.go`: `retain.entity_match_threshold` = 0.6.
- Adapter queries, new files only, consumed through worker interfaces (decisions 1452, 1453):
  - `internal/adapter/postgres/stranded.go` and `queries/stranded.sql`;
  - `internal/adapter/postgres/settle.go` and `queries/settle.sql`.
- `test/e2e`:
  - `env_test.go` and `scenarios_test.go`, the scenarios;
  - `golden_test.go`, the eval fixtures against `memd all`;
  - `gatewayemu/cmd/gatewayemu`, the emulator as a command for the README quick start.
- `docs/rules.md`, `README.md`.

## Interfaces implemented
- `server.OperationReader` and `reflect.PageReader` (glue in cmd/memd).
- `worker.StrandedFactSource` and `worker.BatchSettler` (Postgres Store).
- `recall.TokenCounter` and `retain.TokenCounter` (tiktoken counter).
- `server.ReadinessProbe`: Postgres and gateway.

## Acceptance tests
| Card item | Test |
|---|---|
| e2e: retain sync | `e2e.TestScenarios/RetainSync` |
| e2e: retain async | `e2e.TestScenarios/RetainAsync` |
| e2e: consolidation, recall, reflect | `e2e.TestScenarios/ConsolidationRecallReflect` |
| e2e: mental model create and refresh, both strategies | `e2e.TestScenarios/MentalModelBothStrategies` |
| e2e: invalidate and restore (plus edit) | `e2e.TestScenarios/InvalidateRestore` |
| e2e: retag, reprocess | `e2e.TestScenarios/RetagReprocess` |
| e2e: cancel and retry | `e2e.TestScenarios/CancelRetry` |
| e2e: worker crash and reclaim | `e2e.TestScenarios/WorkerCrashReclaim` |
| e2e: bank delete | `e2e.TestScenarios/BankDelete` |
| e2e: api + worker processes start, kill, restart | `e2e.TestMemdSmoke` |
| Golden fixtures against memd all | `e2e.TestGoldenAgainstMemdAll` |
| Config validation | `memd.TestLoadConfigEnvAndFlags`, `memd.TestValidateRejects`, `memd.TestUsage`, `memd.TestNormalizeDSN` |
| 768-dimension check against the gateway | `memd.TestCheckEmbedding` |
| Tokenizer | `memd.TestTokenCounter` |
| Whole wiring, no model call inside a transaction | `memd.TestWiredStackNoModelCallInsideTransaction` |
| Handlers for every kind; nil engine fails permanently | `worker.TestRegisterEveryKindButRetainBatch` |
| consolidate doesn't re-enqueue on More | `worker.TestConsolidateHandlerNeverReenqueuesOnMore` |
| delete_bank re-enqueues; uncapped retry | `worker.TestDeleteBankReenqueuesUntilDone`, `worker.TestDeleteBankRetriesWithoutAttemptCap` |
| graph_maintenance re-enqueues and re-checks | `worker.TestGraphHandlerReenqueuesAndRechecks` |
| Stranded-fact rederive tick | `worker.TestMaintenanceRederivesStrandedFacts`, `postgres.TestStrandedFacts` |
| retain_batch parents settle | `postgres.TestSettleRetainBatches`, `postgres.TestSettleRetainBatchFailedChild` |
| Usage reported on the operation | `worker.TestRetainHandlerReportsUsage` |
| ValidateOverrides accepts the server's RPC names | `server.TestRealValidatorAcceptsServerRPCNames` |
| Empty repeated override is not a clear | `server.TestEmptyRepeatedOverrideIsNotAClear` |
| CreateNamed | `server.TestEveryRPCForwards` (CreateBankFromTemplate case) |
| Retain usage copied into RetainResponse | `e2e.TestScenarios/RetainSync` |
| ServerDefaults entity_match_threshold 0.6 | `config.TestServerDefaults` |
| rules.md maps every rule to a passing test | `docs/rules.md` (names checked by script) |
