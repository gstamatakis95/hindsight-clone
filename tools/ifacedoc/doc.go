// Command ifacedoc is the `make lint` check that revive can't do: every method of every interface
// type carries a leading doc comment, and every Go package directory has a doc.go holding the package
// comment. It uses only go/parser and go/ast (golang.org/x/tools is not an approved dependency) and
// skips generated files, gen/, internal/mocks/, tools/mockery/, third_party/, testdata and hidden dirs.
//
// Usage: go run ./tools/ifacedoc [dir ...] (default "."). It prints one line per finding and exits 1
// if there is any.
package main
