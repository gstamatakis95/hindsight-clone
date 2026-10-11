package main

import "memd/internal/engine/maintenance"

// wireMaintenance builds the maintenance tick, which runWorker starts (the cross-bank scans come from
// store.Maintenance; wireMentalModels sets the mental-model steps), and the graph_maintenance job.
func wireMaintenance(cfg Config, c core, in infra, d *deps) {
	m := &d.maintenance
	m.Claimer, m.Store, m.Clock, m.Config, m.Logger = in.claimer, c.db, c.clock, c.resolver, d.log
	m.Consolidation, m.Deletes, m.Graph, m.Stranded, m.Batches = in.maint, in.maint, in.maint, in.maint, in.maint
	m.ConsolidationGrace, m.Interval, m.StaleAfter = cfg.ConsolidationGrace, cfg.MaintenanceInterval, cfg.StaleAfter
	d.handlers.Graph = maintenance.NewGraphMaintainer(c.db, c.linker, c.clock)
}
