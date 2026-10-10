// Package golden holds the golden retain -> recall fixtures (*.yaml in this directory) and their loader.
// Each fixture is one bank: its sessions are retained, then each check recalls a query and passes when
// a recalled memory contains one of the accepted substrings. The checks are graded by judge.Contains, so
// CI can run them against `memd all` with the fake gateway, with no model in the loop.
package golden
