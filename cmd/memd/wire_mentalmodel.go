package main

import (
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/reflect"
)

// wireMentalModels builds the mental-model area: the admin RPCs, page search, the refresh runner (both strategies)
// for the RPC and the refresh_mental_model job, and the cron and failed-refresh maintenance steps.
func wireMentalModels(c core, in infra, d *deps) {
	detector := mentalmodel.NewChangeDetector(c.read, c.read)
	md := mentalmodel.ModelDeps{LLM: in.gw, Router: c.router, Clock: c.clock, Directives: c.dirs}
	runner := mentalmodel.NewRefreshRunner(mentalmodel.RefreshRunnerDeps{
		Store: c.db, Config: c.resolver, Scope: c.read,
		Synthesizers: []mentalmodel.Synthesizer{
			mentalmodel.NewAgenticSynthesizer(c.reflector, md), mentalmodel.NewSingleShotSynthesizer(c.recaller, md),
		},
		Projector: reflect.NewStructuredProjector(in.gw, c.router, c.resolver), Embedder: in.gw, Clock: c.clock,
	})
	d.server.Refresher = runner
	d.server.Pages = mentalmodel.NewPageSearcher(c.read, in.gw)
	d.server.MentalModels = mentalmodel.NewAdmin(mentalmodel.AdminDeps{
		Store: c.db, Models: c.read, Detector: detector, Clock: c.clock, Config: c.resolver,
	})
	d.handlers.refresher = runner
	d.maintenance.CronRefresh = mentalmodel.NewTriggers(mentalmodel.TriggerDeps{
		Store: c.db, Scheduler: mentalmodel.NewRefreshScheduler(detector, c.resolver), Cron: in.maint,
		Clock: c.clock,
	})
	d.maintenance.RefreshFailures = mentalmodel.NewRefreshFailureReconciler(in.maint, c.db, c.resolver, c.clock)
}
