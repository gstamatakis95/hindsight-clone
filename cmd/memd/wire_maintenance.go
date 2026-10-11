package main

import (
	"memd/internal/maintenance"
	"time"
)

// wireMaintenance sets the maintenance service's dependencies (the cross-bank scans come from
// store.Maintenance; wireMemory, wireObservations and wireMentalModels set their services' steps). runWorker
// builds the service, which runs the tick and the graph_maintenance job. Its operator settings come from the
// server config file's worker and retention sections.
func wireMaintenance(cfg Config, c core, in infra, d *deps) {
	m := &d.maintenance
	m.Claimer, m.Store, m.Clock, m.Logger = in.claimer, c.db, c.clock, d.log
	m.Deletes, m.Graph = in.maint, in.maint
	m.Linker = c.linker
	f := cfg.File
	m.Config = maintenance.Config{
		Interval: time.Duration(f.Worker.MaintenanceInterval), StaleAfter: time.Duration(f.Worker.StaleAfter),
		OperationRetention: time.Duration(f.Retention.Operations), EventRetention: time.Duration(f.Retention.Events),
	}
}
