// Package reflect answers the Reflect RPC with a bounded tool-calling agent loop (REFLECT slot) over the
// bank's memory: search_mental_models, read_mental_models, search_observations, recall, expand and done.
//
// The system prompt (PromptBuilder) renders the reflect mission, disposition and the directives that
// match the request tags. The loop runs up to max_iterations, must retrieve evidence before done, drops
// citations of ids it never retrieved, fails with RESOURCE_EXHAUSTED past max_total_tokens and with
// UNAVAILABLE when a retrieval tool errors. An optional StructuredProjector pass (STRUCTURED_OUTPUT
// slot) extracts JSON matching a response schema from the final answer.
//
// The agentic mental-model refresh reuses the same loop, answering as a document. reflect imports
// recall and declares the small Recaller and PageReader it consumes; it never imports mentalmodel,
// which imports reflect.
package reflect
