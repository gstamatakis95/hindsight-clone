// Package ai declares the boundary to the AI gateway, [LLM], [Embedder] and [Reranker], with the request and response
// types they take, plus two concrete helpers every model caller uses: [Router], which picks a slot's model binding from
// an already-merged config, and [Tokenizer], which counts tokens for budgets.
//
// # The boundary
//
// internal/ai/gateway implements all three interfaces over one OpenAI-compatible HTTP gateway. Errors crossing the
// boundary are already domain error classes (a timeout or transport failure is domain.ErrUnavailable, a prompt that
// does not fit is domain.ErrContextOverflow), so callers test them with errors.Is. Tests mock the interfaces with the
// mockery output in internal/mocks/ai or use the scripted fakes in internal/fake.
//
// Never call an LLM, the embedder or the reranker inside store.DB.InTx: read, call the model, then write in a short
// transaction.
//
// # Model routing
//
// Code never chooses a model itself. It resolves the effective config (server < tenant < org < bank < strategy <
// request, merged by package config: per slot, the most specific level yielding a binding, its own or its
// models.default, wins), asks [Router.Resolve] for its slot's binding and passes that binding to the LLM or the
// reranker:
//
//	b, err := router.Resolve(cfg, domain.ModelSlotExtract)
//	if err != nil {
//		return err // domain.ErrModelUnbound: no model bound to the slot
//	}
//	usage, err := llm.Structured(ctx, ai.StructuredRequest{Slot: domain.ModelSlotExtract, Binding: b, ...}, &out)
//
// Resolve returns the whole domain.ModelBinding (model, temperature, max output tokens, reasoning effort and the
// free-form extra object). A slot with no model bound is domain.ErrModelUnbound, which wraps
// domain.ErrFailedPrecondition: the API answers FAILED_PRECONDITION and the job runner fails the job at once instead of
// retrying. The message names the config keys that bind it (models.default, models.<slot>). EMBED (the
// server-wide embedder takes no binding) and the unspecified slot are domain.ErrInvalidArgument. The router is pure
// and safe for concurrent use.
//
// # Tokens
//
// [Tokenizer] uses tiktoken's cl100k_base encoding, embedded in the binary; a text it can't encode counts as about four
// characters per token.
package ai
