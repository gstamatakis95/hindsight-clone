package main

import (
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"
	"memd/internal/grpcapi"
)

// wireObservations builds the observation area: ObservationService (the observation admin RPCs and the strategy
// preview) and the consolidate job.
func wireObservations(c core, in infra, d *deps) {
	d.api.Observation = &grpcapi.ObservationHandler{
		Observations: curate.NewObservationAdmin(curate.ObservationAdminDeps{
			Store: c.db, Scopes: c.read, Clearer: consolidate.NewInvalidator(),
		}),
		Strategies: c.matcher,
	}
	d.handlers.consolidator = consolidate.NewConsolidator(consolidate.ConsolidatorDeps{
		Store: c.db, Config: c.resolver, Router: c.router, LLM: in.gw, Embedder: in.gw, Clock: c.clock,
		Scopes: c.scopes, Tokens: c.tok,
	})
}
