// Package blobtest is the contract suite every blob.Store adapter runs from its own tests: [Run] takes a constructor
// that returns a fresh, empty store and checks the behaviour the blob.Store method comments specify (round trips,
// overwrite, missing keys, the size check, DeletePrefix and List paging, boundaries and refusals). It is test support,
// not a fake: production code never imports it.
package blobtest
