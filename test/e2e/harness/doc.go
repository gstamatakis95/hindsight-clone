// Package harness runs memd end to end for the e2e tests in test/e2e: it brings up the docker-compose stack (Postgres +
// MinIO) under a unique project name and migrates it with Alembic, starts the emulated gateway (test/e2e/gatewayemu),
// builds cmd/memd and runs it as api, worker or all subprocesses pointed at both, kills and restarts processes for
// crash scenarios, dials gRPC clients that send the tenant header, and waits for a bank's background work to drain.
//
// It is a regular package (no build tag) so it compiles and lints with the rest of the repository; only test/e2e,
// behind the e2e tag, uses it. It reaches memd only through the process boundary: environment variables, the gRPC API
// (memd/gen/memory/v1) and the MinIO bucket.
//
// # Usage
//
//	bin := harness.BuildMemd(t)                          // go build ./cmd/memd, once per test binary
//	s := harness.StartStack(t)                           // compose up, bucket, alembic upgrade head
//	_, gw := harness.StartEmulator(t, gatewayemu.Options{})
//	p := s.StartMemd(t, bin, harness.MemdOptions{Mode: harness.ModeAll, GatewayURL: gw.URL + gatewayemu.BasePath})
//	c := harness.Dial(t, p.Addr, "tenant-1")
//	err := c.WaitIdle(ctx, "bank-1", 200*time.Millisecond)
//
// # Stack
//
// [StartStack] checks Docker ([RequireDocker]: skip without a daemon, fail when MEMD_REQUIRE_DOCKER is set), picks free
// loopback ports, and runs `docker compose up -d --wait postgres minio` with the root docker-compose.yml plus the
// overlay compose.e2e.yml (data on tmpfs, so a run leaves no volumes) under the project name memd-e2e-<MEMD_E2E_RUN or
// "local">-<random>, passing the ports as MEMD_PG_PORT, MEMD_MINIO_PORT and MEMD_MINIO_CONSOLE_PORT. It then creates
// the bucket with the minio-init service and runs `alembic upgrade head` (MEMD_ALEMBIC overrides the binary, default
// .venv/bin/alembic). Teardown (`compose down -v --remove-orphans`) is registered before the project starts, so a
// failed start still removes what it created; [Stack.Leftovers] lists anything still labelled with the project.
// [Deadline] bounds each step so it ends 30s before the test binary's -timeout, leaving time for the cleanups.
//
// # memd processes
//
// [BuildMemd] builds ./cmd/memd once per test binary into a temporary directory and probes it with a scrubbed
// environment. It skips the test (rather than failing it) when the build fails or when the binary prints "not
// implemented". [Stack.MemdEnv] is the environment memd needs for the stack: MEMD_DSN, the MEMD_S3_* settings
// (path-style, the compose credentials), MEMD_GATEWAY_URL and MEMD_GATEWAY_API_KEY, and MEMD_MODEL_<SLOT> =
// gatewayemu.ModelFor(slot) for every slot in [Slots]. [Stack.StartMemd] adds MEMD_GRPC_ADDR on a free port for api and
// all, applies MemdOptions.Env on top, drops every inherited MEMD_* variable, writes the process's output to a log
// file, and waits until it is ready: the gRPC health check reports SERVING for api and all (up to 90s), or a worker is
// still running after a 2s grace (it serves no health endpoint). An early exit fails the test with the log.
//
// A [Proc] supports [Proc.Kill] (SIGKILL: a crash, claimed jobs stay claimed), [Proc.Stop] (SIGTERM, SIGKILL after
// 15s), [Proc.Restart] (same mode, address and environment), [Proc.Exited] and [Proc.Logs] (every run's output). Every
// process is killed when its test ends.
//
// # Clients
//
// [Dial] connects to an api address in plaintext and returns a [Client] with a client for every memd service, each call
// stamped with the tenant in the x-tenant-id header; [Client.As] switches tenant over the same connection.
// [Client.Pending] returns BankStats.pending_operations_by_kind without zero counts, and [Client.WaitIdle] polls it
// until two consecutive polls see nothing pending, since one idle poll can fall between a job finishing and its
// follow-up being enqueued.
//
// Cleanups run in reverse registration order: memd processes, then the emulator, then compose with its volumes.
package harness
