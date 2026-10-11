package main

import (
	"memd/internal/configsvc"
	"memd/internal/grpcapi"
)

// wireConfig builds configsvc.Service: it serves TenantService and BankService's config, directive and prompt
// preview RPCs. It runs after wireBanks, which builds the BankHandler.
func wireConfig(c core, d *deps) {
	svc := configsvc.New(configsvc.Deps{DB: c.db, Config: c.resolver})
	d.api.Tenant = &grpcapi.TenantHandler{Config: svc}
	d.api.Bank.Config = svc
}
