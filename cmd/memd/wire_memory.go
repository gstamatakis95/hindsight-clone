package main

import (
	"memd/internal/config"
	"memd/internal/engine/curate"
	"memd/internal/engine/retain"
	"memd/internal/grpcapi"
)

// wireMemory builds the memory area: MemoryService (retain, recall, reflect), MemoryUnitService (the memory listing
// and curation), and the retain_document and rederive_memory jobs.
func wireMemory(c core, in infra, d *deps) {
	d.api.Memory = &grpcapi.MemoryHandler{
		Ingestor: retain.NewIngestor(retain.IngestorDeps{
			Store: c.db, Blob: in.blob, Config: c.resolver, Retainer: c.retainer, Merge: config.Merge,
			Clock: c.clock,
		}),
		Engine: c.recall, Agent: c.agent,
	}
	d.api.MemoryUnit = &grpcapi.MemoryUnitHandler{
		Lister: curate.NewMemoryLister(c.read),
		Curator: curate.NewCurator(curate.CuratorDeps{
			Store: c.db, Clock: c.clock, Citing: c.read, Resolver: c.entities,
			Config: c.resolver, Archived: c.read,
		}),
	}
	d.handlers.retainer = c.retainer
	d.handlers.rederiver = curate.NewRederiver(curate.RederiverDeps{Store: c.db, Embedder: in.gw, Linker: c.linker})
}
