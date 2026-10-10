package postgres

import "errors"

// errNotImplemented marks repository methods still being written. Delete with the last stub.
var errNotImplemented = errors.New("not implemented")
