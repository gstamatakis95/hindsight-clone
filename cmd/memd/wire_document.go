package main

import (
	"memd/internal/engine/docs"
	"memd/internal/engine/retain"
	"memd/internal/grpcapi"
)

// wireDocuments builds the document area: DocumentService (the document reads, reprocess and retag) and the
// retag_document job.
func wireDocuments(c core, in infra, d *deps) {
	retagger := docs.NewRetagger(c.db)
	d.api.Document = &grpcapi.DocumentHandler{
		Documents: docs.NewDocumentAdmin(docs.DocumentAdminDeps{
			Store: c.db, Blob: in.blob, Source: c.read, Reprocessor: retain.NewReprocessor(c.db),
		}),
		Retagger: retagger, Operations: &operationReader{s: c.db},
	}
	d.handlers.retagger = retagger
}
