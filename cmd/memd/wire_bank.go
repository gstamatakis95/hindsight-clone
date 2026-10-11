package main

import (
	"memd/internal/bank"
	"memd/internal/domain"
	"memd/internal/grpcapi"
)

// wireBanks builds bank.Service: it serves BankService (create, get, list, delete, stats, tags, templates), runs the
// delete_bank job and the tick's resume_bank_deletes sweep.
func wireBanks(c core, in infra, d *deps) {
	svc := bank.New(bank.Deps{DB: c.db, Maintenance: in.maint, Blob: in.blob, Clock: c.clock})
	d.api.Bank = &grpcapi.BankHandler{Banks: svc}
	d.handlers.add(domain.OperationKindDeleteBank, svc.RunDeleteJob)
	d.maintenance.Banks = svc
}
