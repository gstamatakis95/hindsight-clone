// Package e2e holds the opt-in end-to-end tests (build tag e2e, `make e2e`): memd runs as api, worker or all
// subprocesses against the docker-compose stack (Postgres with pgvector and pg_search, MinIO) and the emulated
// AI gateway in test/e2e/gatewayemu, driven through its gRPC API with the helpers in test/e2e/harness.
package e2e
