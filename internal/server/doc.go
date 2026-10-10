// Package server is the gRPC surface of memd: one handler type per memory.v1 service, plus the tenant
// (x-tenant-id) and deadline interceptors, health, and the mapping from domain errors to gRPC status
// codes. Handlers are thin: they convert proto messages to domain request types, call engines and
// convert the results back. server never imports adapters; cmd/memd/wire.go injects engines built on
// ports.
package server
