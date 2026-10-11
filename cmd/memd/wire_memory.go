package main

import (
	"memd/internal/config"
	"memd/internal/engine/curate"
	"memd/internal/engine/retain"
)

// wireMemory builds the memory area: retain (the Retain RPC and the retain_document job), recall and reflect for
// their RPCs, the memory listing and curation, and the rederive_memory job.
func wireMemory(c core, in infra, d *deps) {
	d.server.Ingestor = retain.NewIngestor(retain.IngestorDeps{
		Store: c.db, Blob: in.blob, Config: c.resolver, Retainer: c.retainer, Merge: config.Merge,
		Scopes: c.scopes, Clock: c.clock,
	})
	d.server.Recaller = c.recaller
	d.server.Reflector = c.reflector
	d.server.Lister = curate.NewMemoryLister(c.read)
	d.server.Curator = curate.NewCurator(curate.CuratorDeps{
		Store: c.db, Clock: c.clock, Citing: c.read, Refresh: c.queuer, Resolver: c.entities,
		Config: c.resolver, Archived: c.read,
	})
	d.handlers.Retainer = c.retainer
	d.handlers.Rederiver = curate.NewRederiver(curate.RederiverDeps{Store: c.db, Embedder: in.gw, Linker: c.linker})
}
