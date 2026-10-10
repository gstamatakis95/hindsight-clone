package domain

import "time"

// Directive is a set of rules reflect must follow. Untagged directives are global; tagged ones apply
// when the request's tags match.
type Directive struct {
	// ID is the directive id; empty on create.
	ID DirectiveID
	// Bank owns the directive.
	Bank BankRef
	// Name is a display name.
	Name string
	// Rules are the instructions, rendered into the reflect prompt.
	Rules []string
	// Tags select which requests the directive applies to; none = global.
	Tags []string
	// Active directives are applied; inactive ones are kept but skipped.
	Active bool
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}
