# 1412 — E2E compose projects: isolation and guaranteed cleanup

## Context
The harness brings up the root `docker-compose.yml` (Postgres and MinIO) for every test that needs a stack.
Runs must not collide with a developer's `make up`, with other worktrees, or with each other. `make e2e` must
leave no containers behind. Two things work against that:
- `go test -timeout` panics without running `t.Cleanup`;
- the compose file declares named volumes (`pgdata`, `miniodata`).

## Decision
- **Project name.** Each stack is its own compose project, `memd-e2e-<run>-<random>`.
  - `<run>` comes from `MEMD_E2E_RUN`. `make e2e` sets it to a timestamp plus the shell PID; it defaults to
    `local`.
  - Host ports are picked free per stack and passed as `MEMD_PG_PORT`, `MEMD_MINIO_PORT` and
    `MEMD_MINIO_CONSOLE_PORT`, the variables the compose file already reads.
- **Overlay.** An overlay, `test/e2e/harness/compose.e2e.yml`, mounts tmpfs over the Postgres and MinIO data
  directories. The named volumes are then unused and never created. Postgres also starts faster.
  - The root compose file is unchanged.
- **Start.** `StartStack` registers `down -v --remove-orphans` with `t.Cleanup` before `up`, so a failed start
  still cleans up. It then runs:
  1. `up -d --wait postgres minio`;
  2. `run --rm minio-init` (creates the bucket);
  3. `alembic upgrade head`. This uses `MEMD_ALEMBIC` or `.venv/bin/alembic`, as pgtest does; `make e2e`
     depends on `venv`.
- **Deadlines.** Harness steps are bounded by `harness.Deadline`: the test's own deadline minus 30 s. A slow
  step therefore fails the test with its cleanups intact, instead of hitting the timeout panic.
- **Sweep.** After `go test`, `make e2e` removes every remaining project whose name contains
  `memd-e2e-<run>-`. This catches a panic or a killed test binary, and it is checked by forcing
  `E2E_TIMEOUT=6s`. The sweep never touches other runs' projects.
- **Verification.** `TestStackUpDown` asserts that after `Down` no container, volume or network carries the
  project's `com.docker.compose.project` label.
- **No Docker.** Without Docker the stack skips its test, unless `MEMD_REQUIRE_DOCKER` is set (as in pgtest).
- `make e2e` accepts `E2E_TIMEOUT` (default 30m).

## Alternatives
- **testcontainers** (like pgtest). Rejected: the card asks for docker compose, and compose exercises the
  same file developers use.
- **Fixed ports.** Rejected: they collide with `make up` and with parallel runs.
- **Sweeping every `memd-e2e-*` project.** Rejected: it would tear down a concurrent run in another worktree.

## Spec lines involved
- SPEC "File structure" (`docker-compose.yml`), "Process model" (Alembic migration step).
- Decisions 003 (image, hidden bootstrap) and 101 (pgtest conventions).
