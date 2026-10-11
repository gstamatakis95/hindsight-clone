package main

import (
	"memd/internal/domain"
	"memd/internal/grpcapi"
	"memd/internal/mentalmodel"
)

// wireMentalModels builds mentalmodel.Service: it serves MentalModelService, runs the refresh_mental_model job and
// the tick's failed-refresh and cron sweeps.
func wireMentalModels(c core, in infra, d *deps) {
	svc := mentalmodel.New(mentalmodel.Deps{
		DB: c.db, Maintenance: in.maint, Config: c.resolver, Recall: c.recall, Reflect: c.agent, LLM: in.gw,
		Router: c.router, Embedder: in.gw, Clock: c.clock,
	})
	d.api.MentalModel = &grpcapi.MentalModelHandler{MentalModels: svc}
	d.handlers.add(domain.OperationKindRefreshMentalModel, svc.RunRefreshJob)
	d.maintenance.MentalModels = svc
}
