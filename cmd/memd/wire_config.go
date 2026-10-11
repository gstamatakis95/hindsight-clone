package main

import (
	"memd/internal/config"
	"memd/internal/engine/bank"
)

// wireConfig builds the config area: tenant config, directives, the effective-config view, override validation and
// the prompt preview.
func wireConfig(c core, d *deps) {
	d.server.Config = c.resolver
	d.server.ValidateOverrides = config.ValidateOverrides
	d.server.Tenants = bank.NewTenantAdmin(c.db)
	d.server.Directives = bank.NewDirectiveAdmin(c.db)
	d.server.Previewer = bank.NewPromptPreviewer(c.resolver, c.dirs)
}
