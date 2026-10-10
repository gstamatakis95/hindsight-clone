// Package gateway is the AI gateway client. One Client implements port.LLM (OpenAI-compatible
// /v1/chat/completions: schema-constrained Structured calls and tool-calling Chat turns), port.Embedder
// (nomic-embed-text-v1.5, 768 dims, with nomic's task prefixes) and port.Reranker (the gateway's
// Cohere/Jina-style rerank route). It enforces a process-wide concurrency limit plus one per model
// slot, validates structured output against its JSON Schema with bounded retries, reports per-slot
// usage and maps transport failures to domain.ErrUnavailable. It holds no business logic.
package gateway
