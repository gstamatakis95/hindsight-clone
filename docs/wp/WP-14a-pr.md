# WP-14a — E2E harness and emulated gateway

Branch `wp/WP-14a-e2e-harness`, based on main `5dbeb22`. The plan is in docs/wp/WP-14a.md.

## What was built
- [x] **Emulated OpenAI-compatible gateway** (`test/e2e/gatewayemu`). It is an `http.Handler`, which
  `gatewayemu.Start` serves on `httptest`. It serves `/v1/chat/completions` (plain, tools and
  `response_format: json_schema`), `/v1/embeddings` and `/v1/rerank`, the routes the adapter uses.
  - It is deterministic, and every structured answer is validated against the request schema with
    `internal/jsonschema` before it is sent.
  - **Extraction:** one fact per sentence. Verbatim mode returns one entry.
  - **TIME_PARSE:** null window.
  - **Consolidation:** one observation per fact, citing its id, with the exact fact text.
  - **Dedup:** keep.
  - **Reflect:** a `recall` call, then `done`, quoting the retrieved texts and citing their ids. This works
    for the plain, directive, document and document+directive done tools.
  - **Mental-model synthesize:** a markdown page.
  - **Markdown delta:** the current document plus the facts.
  - **Structured delta:** one `add_section`.
  - **Structured output:** an instance of the caller's schema.
  - Structured calls are recognised by schema (template) name. Chats are recognised by tools and by the slot
    model `emu-<slot>`; there is no text matching.
  - Recovering data from the rendered prompts is tested by rendering the real templates and parsing them back.
- [x] **Embeddings:** 768 dimensions, deterministic, L2-normalised and never zero. The default mode gives
  shared-token similarity; `EmbedTextHash` is the alternative.
- [x] **Rerank:** scores by query-token overlap in [0,1].
- [x] **Scripted overrides** (`Inject(Fault{Op, Times, Status, Body, Delay, InvalidJSON})`):
  - fail N times with any status (500/429 are retried, 400 is permanent, and a 400 saying
    `context_length_exceeded` is an overflow);
  - a delay that ends when the request is cancelled;
  - invalid JSON.
  - `ClearFaults` removes them, and a call log (`Calls`, `Count`) records each request.
- [x] **Harness: compose** (`test/e2e/harness`). `StartStack` brings up a unique compose project
  `memd-e2e-<run>-<rand>` with free host ports and a tmpfs overlay (no named volumes). It runs `minio-init`,
  then `alembic upgrade head`. `Down` runs `down -v --remove-orphans`, is registered before `up` and is
  idempotent. `Leftovers` lists anything still labelled with the project.
- [x] **Harness: memd processes.**
  - `BuildMemd` runs `go build ./cmd/memd` once. It skips with a clear reason when the build fails or the
    binary is the placeholder.
  - `StartMemd` runs api, worker or all subprocesses with the env contract of decision 1411, pointed at the
    emulator and the stack. It waits for `grpc.health.v1` SERVING; a worker needs only to stay up for 2 s.
    A placeholder exit skips; any other early exit fails with the log.
  - `Proc.Kill` sends SIGKILL (a crash), `Proc.Stop` sends SIGTERM, and `Proc.Restart` starts the process
    again with the same config. `Proc.Logs` returns the output.
- [x] **gRPC helpers.** `harness.Dial` returns clients for all eight services, each call carrying
  `x-tenant-id`. `Client.As` switches tenant.
- [x] **Barrier.** `Client.WaitIdle` waits for an empty `pending_operations_by_kind` on two consecutive polls,
  per decision 702.
- [x] **Teardown always removes containers.**
  - Cleanups run memd, then the emulator, then compose.
  - Steps are bounded by the test deadline minus 30 s (`harness.Deadline`), so a slow step fails before the
    `-timeout` panic.
  - `make e2e` then sweeps this run's projects, which catches a panic or a kill.
- [x] **Makefile `e2e`.** It runs `go test -race -count=1 -tags e2e -timeout ${E2E_TIMEOUT:-30m}
  ./test/e2e/...`, depends on `venv` (Alembic), and sweeps afterwards.
- [x] **Emulator smoke test** (no memd). It runs untagged, so it is part of `make test`.

## Acceptance

| Accept item | Test | Result |
|---|---|---|
| Emulator smoke test passes for every prompt schema in `internal/prompts` | `gatewayemu.TestSmokeEveryPromptSchema` (iterates `prompts.Names()`; a new schema without a case fails), plus `TestSmokeExtractOptionalDimensions`, `TestSmokeStructuredOutputCallerSchemas`, `TestSmokeReflectLoop`, `TestSmokePlainChat`, `TestConsolidateOnePerFact`, `TestEmbeddingsSimilarity`, `TestRerankOverlap`, `TestFaults`, `TestPromptTextRoundTrip`, `TestSentences` | pass (`-race`, `-count=3`) |
| Harness brings compose up and down cleanly (e2e tag) | `e2e.TestStackUpDown` (migrated DB, MinIO healthy, then no container, volume or network left) | pass |
| Harness skips with a clear reason until cmd/memd exists | `e2e.TestMemdSmoke`, which skips with "harness: cmd/memd is still the placeholder (WP-14b wires it): memd: not implemented yet (WP-14)" | skip, as intended |
| `make gen && make lint && make test` pass | `make lint BREAKING_AGAINST=contracts-v1`, with no diff in gen/, schema/ or internal/mocks/ | pass |
| `make e2e` runs (skipping the memd parts) and leaves no containers behind | `make e2e`, then `docker ps -a`, `docker volume ls` and `docker network ls` filtered by memd-e2e. Also checked with `E2E_TIMEOUT=6s`: the panicked run's project is removed by the sweep. | pass |

## Decisions and CCRs
- 1410: how the gateway emulator recognises prompts and what it answers.
- 1411: how the harness runs memd. This covers the env names WP-14b's `cmd/memd` should read
  (`MEMD_DSN`, `MEMD_GRPC_ADDR`, `MEMD_GATEWAY_URL`, `MEMD_GATEWAY_API_KEY`, `MEMD_S3_*`,
  `MEMD_MODEL_<SLOT>`), readiness, the placeholder skip, and kill/restart.
- 1412: compose project isolation, the tmpfs overlay, deadlines and the leftover sweep.
- No CCRs.

## Known gaps
- **memd's env and flag names are a proposal.** They are listed in decision 1411; `cmd/memd` doesn't exist
  yet. If WP-14b chooses different names, it updates the constants in `test/e2e/harness/memd.go`.
- **Worker readiness is only "alive after 2 s".** A worker has no health endpoint.
- **The memd paths of the harness are unexercised.** `StartMemd` readiness, `Dial`, `WaitIdle`, `Kill` and
  `Restart` against a real memd can't run until WP-14b. Only their placeholder/skip path and the compose
  path run today.
- **TIME_PARSE always returns "no window".** Scenarios that need a parsed window rely on the rule-based parser
  in timex.
- **A narrow cleanup race on kill.** If the test binary is killed while `docker compose up` is still creating
  containers, the orphaned compose CLI could create them after the sweep. Deadlines make this unlikely; a
  later `make e2e` run does not sweep other runs' projects.
- **Orphaned memd processes on kill.** Likewise, a test binary that is killed or panics orphans its memd
  subprocesses (Darwin has no `Pdeathsig`). The sweep removes the containers, but memd keeps running and
  retrying the database it lost. Normal failures and the deadline path kill them through `t.Cleanup`.
