package main

import (
	"memd/internal/engine/docs"
	"memd/internal/engine/retain"
)

// wireDocuments builds the document area: the document reads, reprocess and retag (the RPC and the retag_document
// job).
func wireDocuments(c core, in infra, d *deps) {
	retagger := docs.NewRetagger(c.db)
	d.server.Retagger = retagger
	d.server.Documents = docs.NewDocumentAdmin(docs.DocumentAdminDeps{
		Store: c.db, Blob: in.blob, Source: c.read, Reprocessor: retain.NewReprocessor(c.db),
	})
	d.handlers.retagger = retagger
}
