package main

import (
	"memd/internal/grpcapi"
	"memd/internal/operation"
)

// wireOperations builds operation.Service: it serves OperationService (operation and event reads, cancel, retry).
func wireOperations(c core, d *deps) {
	d.api.Operation = &grpcapi.OperationHandler{Operations: operation.New(operation.Deps{DB: c.db})}
}
