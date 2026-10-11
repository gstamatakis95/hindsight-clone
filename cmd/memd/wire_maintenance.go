package main

import (
	"memd/internal/domain"
	"memd/internal/maintenance"
	"time"
)

// wireMaintenance builds maintenance.Service, which runs the tick and the graph_maintenance job. It runs last:
// wireBanks, wireMemory, wireObservations and wireMentalModels set their services' tick steps in d.maintenance
// first. The cross-bank scans come from store.Maintenance, and the operator settings from the server config file's
// worker and retention sections.
func wireMaintenance(cfg Config, c core, in infra, d *deps) {
	m := &d.maintenance
	m.Claimer, m.Store, m.Clock, m.Logger = in.claimer, c.db, c.clock, d.log
	m.Graph = in.maint
	m.Linker = c.linker
	m.TraceTick = d.tel.TickHook()
	f := cfg.File
	m.Config = maintenance.Config{
		Interval: time.Duration(f.Worker.MaintenanceInterval), StaleAfter: time.Duration(f.Worker.StaleAfter),
		OperationRetention: time.Duration(f.Retention.Operations), EventRetention: time.Duration(f.Retention.Events),
	}
	d.maint = maintenance.New(*m)
	d.handlers.add(domain.OperationKindGraphMaintenance, d.maint.RunGraphJob)
}
