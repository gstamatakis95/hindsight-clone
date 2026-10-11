package main

import (
	"memd/internal/engine/mentalmodel"
	"memd/internal/grpcapi"
)

// wireMentalModels builds the mental-model area: MentalModelService (the admin RPCs, page search, dry runs), the
// refresh runner (both strategies) for the refresh_mental_model job, and the cron and failed-refresh maintenance
// steps.
func wireMentalModels(c core, in infra, d *deps) {
	detector := mentalmodel.NewChangeDetector(c.read, c.read, c.recall)
	md := mentalmodel.ModelDeps{LLM: in.gw, Router: c.router, Clock: c.clock, Directives: c.db}
	runner := mentalmodel.NewRefreshRunner(mentalmodel.RefreshRunnerDeps{
		Store: c.db, Config: c.resolver, Scope: c.read, Tags: c.recall,
		Synthesizers: []mentalmodel.Synthesizer{
			mentalmodel.NewAgenticSynthesizer(c.agent, md), mentalmodel.NewSingleShotSynthesizer(c.recall, md),
		},
		Projector: c.agent, Embedder: in.gw, Clock: c.clock,
	})
	d.api.MentalModel = &grpcapi.MentalModelHandler{
		MentalModels: mentalmodel.NewAdmin(mentalmodel.AdminDeps{
			Store: c.db, Models: c.read, Detector: detector, Clock: c.clock, Config: c.resolver,
		}),
		Refresher: runner, Pages: c.recall,
	}
	d.handlers.refresher = runner
	d.maintenance.CronRefresh = mentalmodel.NewTriggers(mentalmodel.TriggerDeps{
		Store: c.db, Scheduler: mentalmodel.NewRefreshScheduler(detector, c.resolver), Cron: in.maint,
		Clock: c.clock,
	})
	d.maintenance.RefreshFailures = mentalmodel.NewRefreshFailureReconciler(in.maint, c.db, c.resolver, c.clock)
}
