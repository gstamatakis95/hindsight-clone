package main

import (
	"memd/internal/configsvc"
	"memd/internal/grpcapi"
)

// wireConfig builds configsvc.Service: it serves ConfigService (the tenant and bank config levels, the prompt
// preview and the directives).
func wireConfig(c core, d *deps) {
	svc := configsvc.New(configsvc.Deps{DB: c.db, Config: c.resolver})
	d.api.Config = &grpcapi.ConfigHandler{Config: svc}
}
