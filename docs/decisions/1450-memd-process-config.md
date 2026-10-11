# 1450 — memd process: configuration, modes, startup checks

## Context
SPEC "Process model" says memd is configured by "env + flags only (DSN, blob endpoint, gateway URL, model
names, budgets)", has three modes (api, worker, all) and serves `grpc.health.v1` with a readiness check on
Postgres and the gateway. SPEC "Function signatures" puts the 768-dimension check in `Config.Validate`. The
e2e harness already fixed the variable names (decision 1411). The Makefile exports `MEMD_DSN` in SQLAlchemy
form (`postgresql+psycopg://...`) for Alembic.

## Decision
- **Settings.** Every knob is a `MEMD_*` variable with a flag of the same name: lower case, no prefix, dashes
  for underscores (`MEMD_GRPC_ADDR` = `-grpc-addr`). A flag wins. The decision 1411 names are kept as they
  are. Additions:
  - `MEMD_EMBED_MODEL` (default nomic-embed-text-v1.5);
  - gateway concurrency and timeout;
  - worker id, slots, poll, heartbeat;
  - `MEMD_STALE_AFTER` and the maintenance interval;
  - RPC deadline, pool size and log level.

  Model bindings stay with `config.ServerDefaults` (`MEMD_MODEL`, `MEMD_MODEL_<SLOT>`).
- **One StaleAfter.** `MEMD_STALE_AFTER` feeds both `RunnerConfig.StaleAfter` and `MaintenanceDeps.StaleAfter`.
  `Validate` rejects a heartbeat interval at or above 3/4 of it, because the runner fences itself at 3/4.
- **Validate is static. The 768-dimension check runs in `buildDeps`** (`checkEmbedding`), right after the
  gateway client exists, and fails startup. `Validate` can't make network calls before the client is built,
  so this splits the spec's one function in two.
- **DSN.** A SQLAlchemy driver suffix in the scheme is dropped (`postgresql+psycopg://` becomes
  `postgresql://`), so `make run` works with the Makefile's single `MEMD_DSN`.
- **Readiness.**
  - Postgres: a ping.
  - Gateway: one plain GET to the base URL. Any HTTP status counts as up. The result is cached for 10 s.
  - Health polls never spend tokens and never show up as model traffic. The startup embedding check covers
    the model.
- **gRPC reflection** is registered in `runAPI`, so `grpcurl` works without proto files (README).
- **Shutdown.**
  - SIGINT or SIGTERM stops the api gracefully, with a 20 s grace.
  - The worker drains through `Runner.Run`.
  - `all` runs both under one errgroup, so if either fails, both stop.
- **Pool size.** Default `max(2 × slots + 16, 32)`, overridable with `MEMD_DB_MAX_CONNS`.

## Alternatives
- **A config file.** Rejected: the spec says env + flags only.
- **Embedding on every readiness poll.** Rejected: it costs tokens and a scripted emulator fault would flip
  health.

## Spec lines involved
SPEC "Process model", "Function signatures" (cmd/memd), "Jobs" (heartbeats and reclaim); decisions 501 and 1411.
