// Command txcheck is the transaction-body check of `make lint` (target lint-txcheck). It runs the
// [memd/tools/txcheck/analyzer] analyzer over the repository's packages.
//
// # Usage
//
//	txcheck ./...   # run from the repository root; make builds it into .bin/txcheck
//
// It is a standard go/analysis single checker, so it accepts the usual flags and exits non-zero on any finding.
// It lives in its own module so that golang.org/x/tools stays out of the memd module.
package main
