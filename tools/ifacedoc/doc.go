// Command ifacedoc is the documentation check of `make lint` (target lint-ifacedoc) that revive can't do. It enforces
// two repository rules:
//
//   - every method of every interface type carries a leading doc comment (revive's exported rule checks exported
//     identifiers, not the methods inside an interface);
//   - every Go package directory has a doc.go holding the package comment.
//
// # Usage
//
//	go run ./tools/ifacedoc [dir ...]   # default "."
//
// It walks each directory, prints one finding per line (sorted), and exits 1 if there is any finding, 2 if a file can't
// be parsed or a directory can't be walked, and 0 otherwise. Findings look like:
//
//	internal/foo/repo.go:12: method Repo.Get has no doc comment
//	internal/foo: package has no parseable doc.go
//	internal/foo/doc.go: doc.go has no package comment
//
// # What it checks and skips
//
// It parses every non-test .go file with go/parser and go/ast only (golang.org/x/tools is not an approved dependency).
// Generated files (ast.IsGenerated: a "Code generated ... DO NOT EDIT." header) are skipped, and a directory whose only
// non-test files are generated needs no doc.go. Embedded interfaces and type constraints inside an interface are not
// methods and are skipped. The walk never enters hidden directories, testdata, or directories whose path ends in gen,
// internal/mocks, tools/mockery, third_party or node_modules; the starting directory itself is always entered.
//
// The package comment must be in doc.go itself: a package comment in another file does not count. The tool has no tests
// of its own; `make lint` runs it over the whole repository.
package main
