package main

import (
	"memd/internal/domain"
	"memd/internal/grpcapi"
	"memd/internal/observation"
)

// wireObservations builds observation.Service: it serves ObservationService and MemoryUnitService
// (GetObservationHistory), runs the consolidate job and the tick's consolidation reconcile sweep.
func wireObservations(c core, in infra, d *deps) {
	svc := observation.New(observation.Deps{
		DB: c.db, Maintenance: in.maint, Config: c.resolver, Router: c.router, LLM: in.gw, Embedder: in.gw,
		Tokens: c.tok, Clock: c.clock,
	})
	d.api.Observation = &grpcapi.ObservationHandler{Observations: svc}
	d.api.MemoryUnit = &grpcapi.MemoryUnitHandler{Observations: svc}
	d.handlers.add(domain.OperationKindConsolidate, svc.RunConsolidateJob)
	d.maintenance.Observations = svc
}
