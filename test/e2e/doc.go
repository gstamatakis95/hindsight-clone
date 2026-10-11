// Package e2e holds memd's opt-in end-to-end tests. They run the real cmd/memd binary as api, worker or all
// subprocesses against the docker-compose stack (Postgres with pgvector and pg_search, MinIO) and the emulated AI
// gateway in test/e2e/gatewayemu, and drive it through its public gRPC API with the helpers in test/e2e/harness.
// Nothing is faked inside memd: the only stand-in is the gateway.
//
// # Running
//
// The test files carry the e2e build tag, so `go test ./...` and `make test` compile only this doc.go. Run them with:
//
//	make e2e   # go test -race -count=1 -tags e2e -timeout ${E2E_TIMEOUT:-30m} ./test/e2e/...
//
// `make e2e` builds the Alembic venv first, names the run through MEMD_E2E_RUN, and afterwards removes any compose
// project of that run that a crashed test left behind. Docker is required; without it the tests skip, unless
// MEMD_REQUIRE_DOCKER is set, in which case they fail.
//
// # Tests
//
//   - TestStackUpDown brings a compose project up (migrated Postgres, MinIO with its bucket), tears it down and checks
//     that no container, volume or network of the project is left.
//   - TestMemdSmoke runs memd as separate api and worker processes, checks the api serves, and kills and restarts the
//     worker.
//   - TestScenarios runs one stack, the emulator and separate api and worker processes with fast worker timings
//     (the config file's worker section: poll 100ms, heartbeat 1s, stale_after 6s, maintenance 2s, so a
//     crashed worker's job is reclaimed in seconds) through these scenarios, each on its own bank and one after another
//     because emulator faults are global: RetainSync, RetainAsync, ConsolidationRecallReflect,
//     MentalModelBothStrategies, ConsolidationTriggersRefresh, InvalidateRestore, RetagReprocess, CancelRetry,
//     WorkerCrashReclaim and BankDelete.
//   - TestGoldenAgainstMemdAll runs every eval/golden fixture through the eval harness against `memd all`; every check
//     must pass.
//
// Scenarios wait for background work with the harness's two-quiet-polls barrier on
// BankStats.pending_operations_by_kind, and inspect MinIO directly where blob state matters (bank delete). Governing
// SPEC sections: "Process model", "Jobs" and "Behavioural rules".
package e2e
