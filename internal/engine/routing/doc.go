// Package routing implements port.ModelRouter: it picks the model binding of an LLM slot from an
// already-merged domain.BankConfig.
//
// # Where it sits
//
// routing is an engine package (domain -> port -> engine -> server/worker -> cmd/memd/wire.go). It imports
// only domain and port; like every engine it must never import adapters, server, worker, gen, pgx, net/http,
// the S3 SDK or Datadog (depguard enforces this).
//
// Engines never choose a model themselves. They resolve the effective config through port.ConfigResolver
// (server < tenant < bank < strategy < request, merged per slot by package config), ask the router for
// their slot's binding and pass that binding to port.LLM or port.Reranker. Because the merge happens before
// the router sees the config, a per-slot override at any level reaches the gateway call unchanged, and
// setting one slot never clears another (SPEC "Model routing").
//
// # Usage
//
// [NewRouter] returns the router; it holds no state:
//
//	r := routing.NewRouter()
//	b, err := r.Resolve(cfg, domain.ModelSlotExtract) // cfg is the merged BankConfig
//	if err != nil {
//		return err // domain.ErrInvalidArgument: slot unbound, unspecified or EMBED
//	}
//	usage, err := llm.Structured(ctx, port.StructuredRequest{Slot: domain.ModelSlotExtract, Binding: b, ...}, &out)
//
// Resolve returns the whole domain.ModelBinding (model, temperature, max output tokens, reasoning effort and
// the free-form extra object), not just the model name.
//
// # Rules and errors
//
// The router is pure: no I/O, no clock, no locking, so it is safe for concurrent use. Resolve returns
// domain.ErrInvalidArgument for:
//
//   - domain.ModelSlotUnspecified;
//   - domain.ModelSlotEmbed, which is server-wide (one embedding model per deployment) and never part of
//     BankConfig;
//   - a slot with no binding in cfg.Models, or a binding whose Model is empty.
//
// cmd/memd does not use the router bare. Its slotRouter wraps it so that an unbound slot (other than EMBED
// and the unspecified slot) becomes domain.ErrFailedPrecondition joined with worker.ErrPermanent, naming the
// MEMD_MODEL / MEMD_MODEL_<SLOT> variables to set: the request is fine, the deployment lacks a binding, and a
// job on that slot fails at once instead of retrying.
//
// # Tests
//
// router_test.go covers bound, unbound, empty-model, EMBED and unspecified slots and checks that binding
// parameters are returned; cmd/memd's TestUnboundSlotRouter covers the wrapper.
package routing
