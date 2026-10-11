package ai

import (
	"fmt"
	"memd/internal/domain"
	"strings"
)

// Environment variables that bind models to slots at the server level (read by config.ServerDefaults).
const (
	// EnvModel is the default gateway model for every LLM slot (all but RERANK and EMBED).
	EnvModel = "MEMD_MODEL"
	// EnvModelPrefix + an upper-case slot name (MEMD_MODEL_EXTRACT, MEMD_MODEL_RERANK, ...) binds one slot,
	// winning over EnvModel. RERANK is bound only this way.
	EnvModelPrefix = "MEMD_MODEL_"
)

// Router picks a slot's model binding from an already-merged config. It holds no state, so the zero value and
// one Router shared by every caller are both fine.
type Router struct{}

// NewRouter returns the model router.
func NewRouter() *Router { return &Router{} }

// Resolve returns cfg's binding for slot. EMBED (server-wide, never in BankConfig) and the unspecified slot are
// domain.ErrInvalidArgument. A slot with no model bound is domain.ErrModelUnbound (FAILED_PRECONDITION: the request
// is fine, the deployment or the tenant or bank config lacks a binding), naming the variables that would bind it.
func (*Router) Resolve(cfg domain.BankConfig, slot domain.ModelSlot) (domain.ModelBinding, error) {
	if slot == domain.ModelSlotUnspecified || slot == domain.ModelSlotEmbed {
		return domain.ModelBinding{}, fmt.Errorf("%w: slot %q has no per-bank binding", domain.ErrInvalidArgument, slot)
	}
	b, ok := cfg.Binding(slot)
	if !ok || b.Model == "" {
		return domain.ModelBinding{}, fmt.Errorf("%w: no model bound to slot %q (set %s or %s%s, or bind it in the "+
			"tenant or bank config)", domain.ErrModelUnbound, slot, EnvModel, EnvModelPrefix,
			strings.ToUpper(string(slot)))
	}
	return b, nil
}
