package ai

import (
	"fmt"
	"memd/internal/domain"
)

// Router picks a slot's model binding from an already-merged config. It holds no state, so the zero value and
// one Router shared by every caller are both fine.
type Router struct{}

// NewRouter returns the model router.
func NewRouter() *Router { return &Router{} }

// Resolve returns the binding cfg yields for slot: the slot's own binding, else models.default for a chat-model
// slot (domain.ModelBindings.For). cfg is normally the effective config, whose levels package config has already
// merged so that the most specific level yielding a binding won. EMBED (the server-wide embedder takes no binding)
// and the unspecified slot are domain.ErrInvalidArgument. A slot with no model is domain.ErrModelUnbound
// (FAILED_PRECONDITION: the request is fine, the server, tenant or bank config lacks a binding).
func (*Router) Resolve(cfg domain.BankConfig, slot domain.ModelSlot) (domain.ModelBinding, error) {
	if slot == domain.ModelSlotUnspecified || slot == domain.ModelSlotEmbed {
		return domain.ModelBinding{}, fmt.Errorf("%w: slot %q has no per-call binding", domain.ErrInvalidArgument, slot)
	}
	b, ok := cfg.Binding(slot)
	if !ok {
		hint := "models.default or models." + string(slot)
		if !domain.IsLLMSlot(slot) {
			hint = "models." + string(slot)
		}
		return domain.ModelBinding{}, fmt.Errorf("%w: no model bound to slot %q (set %s in the server config file, "+
			"or in the tenant or bank config)", domain.ErrModelUnbound, slot, hint)
	}
	return b, nil
}
