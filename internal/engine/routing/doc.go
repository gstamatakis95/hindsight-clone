// Package routing implements port.ModelRouter: it picks the model binding of an LLM slot from an
// already-merged BankConfig (server < tenant < bank < strategy < request). Engines never choose a
// model themselves; they ask the router for their slot's binding and pass it to port.LLM, so per-slot
// overrides at every config level reach the gateway call.
//
// The router is pure (no I/O, no clock): a slot with no model bound is domain.ErrInvalidArgument.
package routing
