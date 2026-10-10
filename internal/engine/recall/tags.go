package recall

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// TagFilterCompiler turns flat tags or tag groups into one SQL predicate shared by all four arms.
type TagFilterCompiler interface {
	// Compile resolves fuzzy leaves against the bank's tags by trigram similarity before compiling. An
	// empty filter compiles to an empty PredicateAnd, which matches every unit.
	Compile(ctx context.Context, b port.BankRef, f domain.TagFilter) (port.SQLPredicate, error)
}
