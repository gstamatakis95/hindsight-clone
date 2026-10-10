# 007 — Mockery v3 with the matryer template, built from a pinned tools module

## Context
- The spec requires mockery mocks for every interface and forbids hand-written mocks.
- Mockery v3's default `testify` template imports `github.com/stretchr/testify`, which is not an approved
  dependency.
- The installed mockery 3.8.0 binary was built against an older `golang.org/x/tools`. It cannot read Go
  1.27 export data ("export data version 5 is greater than maximum supported version 4"), so it fails on
  every package.

## Decision
- `.mockery.yaml` uses `template: matryer`, the moq-style template. Its mocks use only `sync` plus the
  mocked package's own imports, and expose `XxxFunc` fields and call recorders.
- **Layout.** Output goes to `internal/mocks/<path under internal/>/mocks.go`, for example
  `internal/mocks/engine/recall`.
  - Each source package gets its own package, `mock<name>` (for example `mockport`, `mockrecall`).
  - The mock types keep the interface names: `mockport.Store`, `mockrecall.Recaller`.
  - Separate packages avoid name clashes between the consumer-declared interfaces that share a name
    (`Recaller`, `Reflector`, `Invalidator`, ...).
- **Sources.** Mocks come from `internal/port`, `internal/engine/...` and `internal/worker`. gen/ is
  excluded: the gRPC server interfaces carry unexported `mustEmbed...` methods, and WP-06 tests servers
  over bufconn.
- **Building mockery.** `make gen` builds mockery from `tools/mockery`, a nested Go module with its own
  `go.mod`, into `.bin/mockery` (git-ignored).
  - The module pins mockery v3.8.0 with `golang.org/x/tools` v0.51.0, which reads current export data.
  - Being a separate module keeps mockery's dependency tree out of memd's `go.mod`.

## Alternatives
- testify template plus a CCR for testify. Rejected: matryer needs no dependency.
- `go tool` mockery inside memd's `go.mod`. Rejected: it pulls dozens of indirect modules into the
  product module.
- Use the system mockery binary. Rejected: it doesn't work with Go 1.27.

## Spec lines involved
Code standards ("Mocks for every interface are generated with mockery; hand-written mocks are not
allowed"); File structure (`.mockery.yaml`, `internal/mocks/`); worker template dependency list.
