package main

import (
	"memd/internal/engine/bank"
	"memd/internal/grpcapi"
)

// wireBanks builds the bank area: BankService (bank admin, the effective config, stats and tag listings, the prompt
// preview, directives, templates) and the two-step bank delete (the DeleteBank RPC and the delete_bank job).
func wireBanks(c core, in infra, d *deps) {
	deleter := bank.NewBankDeleter(c.db, in.blob, c.clock, bank.DeleterConfig{})
	d.api.Bank = &grpcapi.BankHandler{
		Banks: bank.NewBankAdmin(c.db), Config: c.resolver, Stats: bank.NewStatsReader(c.read),
		Previewer: bank.NewPromptPreviewer(c.resolver, c.db), Deleter: deleter,
		Templates: bank.NewTemplateCodec(c.db, c.clock), Directives: bank.NewDirectiveAdmin(c.db),
	}
	d.handlers.deleter = deleter
}
