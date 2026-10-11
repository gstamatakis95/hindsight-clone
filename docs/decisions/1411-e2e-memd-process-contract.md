# 1411 — How the e2e harness runs memd (environment, readiness, skip)

## Context
The harness must start `memd` as separate api and worker processes, or as `all`, pointed at the emulator and
the compose stack. It must also kill and restart a worker. `cmd/memd` is still a placeholder until WP-14b: it
builds, prints "memd: not implemented yet (WP-14)" and exits 1. The SPEC says only that memd is configured
by "env + flags only (DSN, blob endpoint, gateway URL, model names, budgets)". Of these, only `MEMD_DSN` (from
Alembic) and `MEMD_MODEL` / `MEMD_MODEL_<SLOT>` (from `config.ServerDefaults`) have names so far.

## Decision
- **Invocation:** `memd <mode> [args...]`, where mode is `api`, `worker` or `all`.
- **Environment.** The harness sets the variables below. WP-14b's `cmd/memd` reads these names. If it picks
  others, it changes the constants in `test/e2e/harness/memd.go` in the same PR.

| Variable | Value |
|---|---|
| `MEMD_DSN` | `postgres://memd:memd@127.0.0.1:<port>/memd?sslmode=disable` |
| `MEMD_GRPC_ADDR` | `127.0.0.1:<free port>` (api and all only) |
| `MEMD_GATEWAY_URL` | the emulator URL + `/v1` |
| `MEMD_GATEWAY_API_KEY` | `e2e` |
| `MEMD_S3_ENDPOINT`, `MEMD_S3_BUCKET`, `MEMD_S3_ACCESS_KEY`, `MEMD_S3_SECRET_KEY`, `MEMD_S3_REGION`, `MEMD_S3_PATH_STYLE` | the compose MinIO: bucket `memd`, keys `memd` / `memd-secret`, `us-east-1`, `true` |
| `MEMD_MODEL_<SLOT>` | `emu-<slot>` for every LLM slot and RERANK (binding RERANK turns rerank on) |

- `MemdOptions.Env` adds to or overrides any of these, for example worker poll intervals.
- **Readiness.**
  - api and all are ready when `grpc.health.v1` answers SERVING.
  - A worker serves no health endpoint, so it counts as started once it has stayed up for 2 s.
  - Both waits stop at the test's deadline minus a margin.
- **Early exit and skip.**
  - `BuildMemd` builds `./cmd/memd` once per test binary.
  - If the build fails, it skips with the build output.
  - Otherwise it runs `memd` with no mode once as a probe. If the output says "not implemented", it skips
    with that reason. That is the placeholder, and the probe runs before any compose project starts.
  - The probe's environment is scrubbed to `PATH`, `HOME` and an unreachable `MEMD_DSN`, so a real memd can
    never reach a developer's database.
  - `StartMemd` also drops every inherited `MEMD_*` variable before applying the harness's own.
  - `StartMemd` repeats the placeholder check on an early exit. Any other early exit fails the test with the
    process log, so a real startup crash in WP-14b is never silently skipped.
- **Crash and restart.**
  - `Proc.Kill` sends SIGKILL and waits. This is a crash: claimed jobs stay claimed until reclaim.
  - `Proc.Stop` sends SIGTERM and waits, then kills after 15 s.
  - `Proc.Restart` starts the process again with the same mode, address and environment.
  - Logs from every run are appended to one file under `t.TempDir()`.
- **Clients and barrier.**
  - `harness.Dial` returns a client for every service. Each call carries `x-tenant-id` (`server.TenantHeader`).
  - `Client.As` switches tenant over the same connection.
  - `Client.WaitIdle` polls `GetBankStats` until `pending_operations_by_kind` is empty on two consecutive
    polls (decision 702).
- **Teardown order.** Processes registered after the stack are torn down first, through `t.Cleanup` LIFO:
  memd, then the emulator, then compose.

## Alternatives
- **Flags instead of environment.** Rejected: env is what `ServerDefaults` and Alembic already read, and
  flags would need parsing decisions that belong to WP-14b.
- **Skip whenever the build fails or the binary exits non-zero.** Rejected: this would hide real memd crashes
  once WP-14b lands. Only the placeholder's marker skips.
- **Running memd in-process.** Rejected: the card asks for subprocesses, so that kill -9 and reclaim are real.

## Spec lines involved
- SPEC "Process model" (modes, env + flags config, `grpc.health.v1`, tenant header).
- SPEC "Model routing" (per-slot bindings).
- Decisions 600 (tenant header) and 702 (barrier).
