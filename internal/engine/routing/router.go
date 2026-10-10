package routing

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// router is the pure port.ModelRouter.
type router struct{}

var _ port.ModelRouter = (*router)(nil)

// NewRouter returns the model router. It holds no state; the binding comes from the merged config.
func NewRouter() port.ModelRouter { return &router{} }

// Resolve returns cfg's binding for slot. EMBED (server-wide, never in BankConfig), an unspecified slot
// and a slot with no model bound are domain.ErrInvalidArgument.
func (*router) Resolve(cfg domain.BankConfig, slot port.ModelSlot) (port.ModelBinding, error) {
	if slot == domain.ModelSlotUnspecified || slot == domain.ModelSlotEmbed {
		return port.ModelBinding{}, fmt.Errorf("%w: slot %q has no per-bank binding", domain.ErrInvalidArgument, slot)
	}
	b, ok := cfg.Binding(slot)
	if !ok || b.Model == "" {
		return port.ModelBinding{}, fmt.Errorf("%w: no model bound to slot %q", domain.ErrInvalidArgument, slot)
	}
	return b, nil
}
