package main

import (
	"memd/internal/domain"
	"memd/internal/engine/curate"
	"memd/internal/grpcapi"
	"memd/internal/memory"
)

// wireMemory builds the memory area: memory.Service serves MemoryService (retain, recall, reflect) and
// MemoryUnitService's listing and curation, runs the retain_document and rederive_memory jobs, and provides the
// maintenance tick's rederive_stranded and settle_retain_batches steps. GetObservationHistory still reads
// through the curate lister.
func wireMemory(c core, in infra, d *deps) {
	svc := memory.New(memory.Deps{
		DB: c.db, Maintenance: in.maint, Blob: in.blob, LLM: in.gw, Embedder: in.gw, Router: c.router,
		Tokens: c.tok, Config: c.resolver, Times: c.times, Linker: c.linker, Entities: c.entities,
		Recall: c.recall, Agent: c.agent, Clock: c.clock,
	})
	d.api.Memory = &grpcapi.MemoryHandler{Memory: svc}
	d.api.MemoryUnit = &grpcapi.MemoryUnitHandler{Memory: svc, Lister: curate.NewMemoryLister(c.read)}
	d.handlers.add(domain.OperationKindRetainDocument, svc.RunRetainJob)
	d.handlers.add(domain.OperationKindRederiveMemory, svc.RunRederiveJob)
	d.maintenance.Memory = svc
}
