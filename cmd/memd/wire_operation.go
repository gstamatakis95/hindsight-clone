package main

import (
	"memd/internal/grpcapi"
	"memd/internal/jobs"
)

// wireOperations builds the operation area: OperationService (operation reads and events through operationReader in
// glue.go, and operation control: cancel, retry).
func wireOperations(c core, d *deps) {
	d.api.Operation = &grpcapi.OperationHandler{
		Operations: &operationReader{s: c.db}, Control: jobs.NewOperationControl(c.db),
	}
}
