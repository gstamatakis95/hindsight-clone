package main

import (
	"memd/internal/engine/bank"
	"memd/internal/grpcapi"
)

// wireConfig builds the config area: TenantService (the tenant-level config).
func wireConfig(c core, d *deps) {
	d.api.Tenant = &grpcapi.TenantHandler{Tenants: bank.NewTenantAdmin(c.db)}
}
