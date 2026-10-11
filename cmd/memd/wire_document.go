package main

import (
	"memd/internal/document"
	"memd/internal/domain"
	"memd/internal/grpcapi"
)

// wireDocuments builds document.Service: it serves DocumentService (the document reads, retag and reprocess)
// and runs the retag_document job.
func wireDocuments(c core, in infra, d *deps) {
	svc := document.New(document.Deps{DB: c.db, Blob: in.blob, Clock: c.clock})
	d.api.Document = &grpcapi.DocumentHandler{Documents: svc}
	d.handlers.add(domain.OperationKindRetagDocument, svc.RunRetagJob)
}
