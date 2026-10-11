// Package gateway is memd's AI gateway client. One [Client] implements ai.LLM, ai.Embedder and ai.Reranker over
// the gateway's OpenAI-compatible HTTP API:
//
//   - POST {BaseURL}/chat/completions: schema-constrained Structured calls (response_format json_schema) and
//     tool-calling Chat turns;
//   - POST {BaseURL}/embeddings: nomic-embed-text-v1.5 with nomic's task prefixes, 768 dimensions;
//   - POST {BaseURL}{RerankPath} (default /rerank): a Cohere/Jina-style cross-encoder rerank route.
//
// # Architecture
//
// The package is the adapter of internal/ai: *Client implements ai.LLM, ai.Embedder and ai.Reranker, and cmd/memd
// builds it. It imports internal/ai, internal/domain and internal/jsonschema and never internal/engine,
// internal/server or internal/jobs; engines must not import net/http, so every model call goes through this package.
// It holds no business logic: which model serves a slot, prompt text and token budgets are decided by the engines and
// internal/config, which pass a domain.ModelBinding with every call.
//
// # Usage
//
// [NewClient] takes a [Config] and an optional *http.Client (nil = a default client). cmd/memd/wire.go fills the config
// from MEMD_GATEWAY_URL (BaseURL, including the version, e.g. http://gateway:4000/v1), MEMD_GATEWAY_API_KEY (sent as
// "Authorization: Bearer <key>"), MEMD_EMBED_MODEL, MEMD_GATEWAY_MAX_CONCURRENT (cmd/memd default 32),
// MEMD_GATEWAY_MAX_CONCURRENT_<SLOT> (SlotConcurrency) and MEMD_GATEWAY_TIMEOUT (cmd/memd default 120s); the package
// itself reads no environment variables. Zero Config fields take the package defaults: [DefaultEmbedModel],
// [DefaultEmbedBatchSize] (64), [DefaultRerankPath], [DefaultMaxConcurrent] (32), [DefaultTimeout] (60s),
// [DefaultMaxRetries] (2) and [DefaultRetryBackoff] (500ms).
//
//	gw := gateway.NewClient(gateway.Config{BaseURL: "http://gateway:4000/v1", APIKey: key}, nil)
//	usage, err := gw.Structured(ctx, ai.StructuredRequest{
//		Slot: slot, Binding: binding, Name: "extract", System: sys, User: user, Schema: schema,
//	}, &out)
//
// # Calls
//
//   - [Client.Structured] compiles req.Schema with internal/jsonschema (a missing or malformed schema is
//     domain.ErrInvalidArgument and no call is made), sends it as response_format json_schema without strict mode,
//     named after the sanitized template name, then validates the output locally and decodes it into out. Invalid
//     output is retried with the invalid answer and the validator's error appended to the conversation.
//   - [Client.Chat] makes one turn of a tool-calling loop. Tool-call arguments travel as JSON strings and come back
//     unchanged even when malformed, so a bad call can be fed back to the model.
//   - [Client.EmbedDocuments] prefixes "search_document: " and sends EmbedBatchSize texts per request;
//     [Client.EmbedQuery] prefixes "search_query: ". Every vector must have domain.EmbeddingDim (768) floats and land
//     at its input index, else domain.ErrUnavailable.
//   - [Client.Rerank] returns one score per document in input order: raw scores pass through when all are in [0,1],
//     otherwise all go through a sigmoid; an unscored or NaN document scores 0.
//
// The binding's sampling fields (temperature, top_p, max_output_tokens, reasoning_effort) are sent only when set, and
// its extra map is copied onto the request body last. This lets a binding add or override any request field, e.g.
// max_completion_tokens for providers that want it.
//
// # Retries, timeouts and concurrency
//
// Each HTTP attempt is bounded by the binding's timeout_ms, else Config.Timeout. A call makes at most 1 + max_retries
// attempts (the binding's max_retries, else Config.MaxRetries; embeddings always use the config value), with a backoff
// of RetryBackoff << n capped at 8s and cut short by ctx. Invalid structured output, attempt timeouts, transport
// errors, 408, 429, 5xx, an undecodable response and a response with no choices all draw on that one budget; other 4xx
// responses are not retried. A call's worst case is therefore (1 + max_retries) x timeout plus backoff; callers bound
// the whole call with their ctx.
//
// Every HTTP attempt (chat, embeddings and rerank alike) takes a permit from the slot's semaphore when SlotConcurrency
// caps the slot (embeddings run under the embed slot, rerank under the rerank slot), then from the process-wide
// MaxConcurrent semaphore. Permits are held for one attempt only, never across a backoff, and waiting for them honours
// ctx; a saturated slot never holds global capacity while it waits. The Client is safe for concurrent use.
//
// # Errors
//
// Gateway failures wrap domain.ErrUnavailable with %w, with context.DeadlineExceeded for an attempt timeout or the HTTP
// status and the start of the body in the message. A 400 or 413 whose body says the prompt exceeded the context window
// is domain.ErrContextOverflow (which wraps domain.ErrUnavailable and is not retried), so reflect can synthesise from
// what it has instead of failing. The caller's own cancellation or deadline is returned as the context error, never
// retried and never wrapped in domain.ErrUnavailable. Response bodies are read up to 64 MiB.
//
// # Usage accounting and tracing
//
// Structured and Chat return their token usage summed over every attempt, failed ones included; its model is the one
// the gateway reported, else the binding's. Config.OnUsage, when set, is called once per successful HTTP call of any
// kind; cmd/memd does not set it. Config.TraceCall ([CallTraceFunc]) wraps every call, with all its retries, in a trace
// span that sees only the slot, the requested model and the route, never prompts or responses. cmd/memd/wire.go passes
// the Datadog hook and an instrumented *http.Client from internal/observability when MEMD_DATADOG_ENABLED is set; this
// package imports nothing from Datadog.
//
// # Testing
//
// The tests run the client against httptest servers that script responses, failures, delays and overflow bodies
// (chat_test.go, embed_rerank_test.go, limits_test.go, overflow_test.go). End-to-end, test/e2e drives it against the
// deterministic gateway emulator in test/e2e/gatewayemu. Governing documents: SPEC "Model routing" and "Ports" (LLM,
// Embedder, Reranker). Structured-output schemas use the JSON Schema subset internal/jsonschema supports.
package gateway
