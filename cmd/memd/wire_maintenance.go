package main

import "memd/internal/maintenance"

// wireMaintenance sets the maintenance service's dependencies (the cross-bank scans come from
// store.Maintenance; wireMentalModels sets the mental-model steps). runWorker builds the service, which runs
// the tick and the graph_maintenance job. The retention settings keep their defaults.
func wireMaintenance(cfg Config, c core, in infra, d *deps) {
	m := &d.maintenance
	m.Claimer, m.Store, m.Clock, m.Resolver, m.Logger = in.claimer, c.db, c.clock, c.resolver, d.log
	m.Consolidation, m.Deletes, m.Graph, m.Stranded, m.Batches = in.maint, in.maint, in.maint, in.maint, in.maint
	m.Linker = c.linker
	m.Config = maintenance.Config{
		ConsolidationGrace: cfg.ConsolidationGrace, Interval: cfg.MaintenanceInterval, StaleAfter: cfg.StaleAfter,
	}
}
