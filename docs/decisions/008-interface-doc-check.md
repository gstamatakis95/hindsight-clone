# 008 — Interface-method doc check is a stdlib tool in tools/ifacedoc

## Context
Code standards say revive skips interface methods, "so a small go/analysis check in `make lint` covers
those". `golang.org/x/tools/go/analysis` is not an approved dependency.

## Decision
`tools/ifacedoc` (package main, in the memd module) does the check with only `go/parser` and `go/ast`.
`make lint` runs it as `go run ./tools/ifacedoc`.

What it checks:
- Every named method of every interface type in non-test, non-generated files must have a leading doc
  comment. Embedded interfaces are exempt.
- Every directory holding Go files must have a `doc.go` with a package comment. This is the spec's
  "every package has a doc.go".

What it skips:
- Files marked `// Code generated ... DO NOT EDIT.`
- The `gen/`, `internal/mocks/`, `tools/mockery/` and `third_party/` directories.
- `testdata` and hidden directories.

It prints `file:line: method I.M has no doc comment` per finding and exits 1 if there are any.

## Alternatives
- A golangci-lint custom plugin. Rejected: it needs a custom golangci build.
- An x/tools analyzer. Rejected: x/tools is not an approved dependency.

## Spec lines involved
Code standards, "Docstrings everywhere".
