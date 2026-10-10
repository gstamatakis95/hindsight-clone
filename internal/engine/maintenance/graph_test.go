package maintenance

import (
	"memd/internal/engine/retain"
	"testing"
)

// retain.Linker must satisfy the consumer-declared Linker, so wire.go can pass one to the maintainer.
var _ Linker = retain.Linker(nil)

func TestLinkerContract(t *testing.T) {
	var l Linker = retain.Linker(nil)
	if l != nil {
		t.Fatal("a nil retain.Linker converts to a nil Linker")
	}
}
