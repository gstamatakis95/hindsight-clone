package porttest

import (
	"memd/internal/domain"
	"memd/internal/port"
)

// embeddedFact is fact with an embedding. UnitRepo.Unconsolidated and Reader.BanksToConsolidate skip units
// without one (decision 1500), so a test expecting a fact among the consolidation candidates builds it here.
func embeddedFact(b port.BankRef, text string) domain.Unit {
	u := fact(b, text)
	u.Embedding = angleVec(0)
	return u
}
