// Package harness runs memd end to end for the e2e tests (decisions 1411, 1412): it brings up the
// docker-compose stack (Postgres + MinIO) under a unique project name and migrates it with Alembic, starts the
// emulated gateway (test/e2e/gatewayemu), builds cmd/memd and runs it as api, worker or all subprocesses
// pointed at both, kills and restarts processes for crash scenarios, dials gRPC clients that send the tenant
// header, and waits for a bank's background work to drain (BankStats.pending_operations_by_kind empty).
//
// Everything registers its own teardown with t.Cleanup (memd processes, then the emulator, then compose with
// its volumes). Without Docker the stack skips its test unless MEMD_REQUIRE_DOCKER is set; while cmd/memd is
// still the placeholder, BuildMemd and StartMemd skip with that reason.
package harness
