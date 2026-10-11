package main

import "memd/internal/worker"

// wireOperations builds the operation area: operation reads and events (operationReader in glue.go) and operation
// control (cancel, retry).
func wireOperations(c core, d *deps) {
	d.server.Operations = &operationReader{s: c.db}
	d.server.Control = worker.NewOperationControl(c.db)
}
