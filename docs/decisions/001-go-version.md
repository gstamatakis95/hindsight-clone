# 001 — go.mod pins `go 1.25.0`, not 1.24

## Context
The playbook asks for `go 1.24` (its minimum) "unless a dependency forces higher". The generated gRPC code
needs `google.golang.org/grpc` and the `protoc-gen-go-grpc` plugin, both approved dependencies.

## Decision
`go.mod` says `go 1.25.0`. The current releases, `google.golang.org/grpc` v1.84.0 and
`google.golang.org/grpc/cmd/protoc-gen-go-grpc` v1.6.2, both declare `go 1.25.0`, so `go get` raised the
directive. The protobuf code generators are pinned as `tool` directives in `go.mod` (`go tool
protoc-gen-go`, `go tool protoc-gen-go-grpc`), and `buf.gen.yaml` runs them through `go tool`. Nobody has
to install them by hand, and every agent gets the same versions.

## Alternatives
- Pin older grpc (<= v1.7x) to keep `go 1.24`. Rejected: it gives up security fixes for a minimum the
  toolchain doesn't need (Go 1.27.2 is installed everywhere).
- Install the plugins globally with `go install`. Rejected: the versions would drift between agents.

## Spec lines involved
SWARM_PROMPT setup ("Go (1.24 or later)"); orchestrator note "Pin go.mod to go 1.24 unless a dependency
forces higher".
