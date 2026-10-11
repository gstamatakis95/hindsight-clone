package main

import "memd/internal/engine/bank"

// wireBanks builds the bank area: bank admin, stats and tag listings, templates and the two-step bank delete (the
// DeleteBank RPC and the delete_bank job).
func wireBanks(c core, in infra, d *deps) {
	deleter := bank.NewBankDeleter(c.db, in.blob, in.claimer, c.clock, bank.DeleterConfig{})
	d.server.Banks = bank.NewBankAdmin(c.db)
	d.server.Stats = bank.NewStatsReader(c.read)
	d.server.Templates = bank.NewTemplateCodec(c.db, c.queuer)
	d.server.Deleter = deleter
	d.handlers.Deleter = deleter
}
