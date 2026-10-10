# 300 — Gateway wire contract: base URL, config naming, strict mode, request fields

## Context
The spec fixes the routes (`/v1/chat/completions`, `/v1/embeddings`, a Cohere/Jina-style rerank route) and
the signature `NewClient(cfg GatewayConfig, hc *http.Client) *Client`. It does not say how the base URL is
written, whether `json_schema` is sent in strict mode, which output-cap field is used, or how `ToolChoice`
and `extra` land in the body.

## Decision
- **Naming.** The config types are `gateway.Config` and `s3.Config`, not `GatewayConfig` / `S3Config`.
  revive's `exported` rule (a lint gate) rejects stuttering names. Otherwise the signatures match the spec.
- **Base URL.** `Config.BaseURL` is the OpenAI-style base *including* the version (`http://gw:4000/v1`), as
  OpenAI SDKs and LiteLLM expect. The routes are `/chat/completions`, `/embeddings` and `RerankPath`. The
  default `RerankPath` is `/rerank`, which gives `/v1/rerank`; Cohere, Jina and LiteLLM all serve it.
- **Strict mode off.** `response_format` is `{"type":"json_schema","json_schema":{"name","schema"}}` with no
  `strict` field. STRUCTURED_OUTPUT schemas come from users' ReflectRequest, and OpenAI rejects strict mode
  unless every property is required and `additionalProperties` is false. The adapter validates the output
  locally anyway.
- **Schema name.** `json_schema.name` must match `[a-zA-Z0-9_-]{1,64}`, so the request's template `Name` is
  sanitized: any other character becomes `_`, the result is truncated to 64 characters, and an empty name
  becomes `response`.
- **Output cap.** `max_tokens` is `req.MaxOut`, or the binding's `max_output_tokens` if that is 0. It is
  omitted when both are unset. Temperature, top_p and reasoning_effort are sent only when set.
- **Extra.** The binding's `extra` map is copied onto the body last, so it can add or override any field. For
  example, it can add `max_completion_tokens` for providers that want it. `response_format` is set after
  `extra`.
- **Tool choice.** An empty value is omitted (the provider default, auto). `auto`, `none` and `required` are
  sent as strings. Any other value is a tool name and becomes
  `{"type":"function","function":{"name":...}}`.
- **Tool-call arguments** are a JSON string on the wire.
  - Inbound: the string's raw text becomes `ToolCall.Arguments` unchanged, even when it is malformed. If a
    gateway sends an object instead, it is kept as is.
  - Outbound: `Arguments` is always sent as `string(Arguments)`. A malformed call can then be fed back to
    the model on the next turn (Behavioural rules: "a malformed tool call goes back to the model").
- **Embeddings** send `model` (default `nomic-embed-text-v1.5`), `input` and `encoding_format: "float"`.
  Each result is placed by its `index`.
- **Rerank** sends `model`, `query`, `documents`, `top_n = len(docs)`, `return_documents: false` and `extra`.
  Each result is placed by its `index`. A document the route leaves unscored, or scores NaN, gets 0, as in
  Hindsight.

## Alternatives
- Base URL without `/v1`: rejected, because it breaks the common `OPENAI_BASE_URL` convention.
- `strict: true` (Hindsight builds strict schemas from Pydantic): rejected, because user-supplied schemas
  usually don't meet strict mode's rules (see above).
- `max_completion_tokens`: rejected as the default. It is a newer, OpenAI-only name, while every
  OpenAI-compatible gateway accepts `max_tokens`, and `extra` can add the other.

## Spec lines involved
Model routing ("Binding fields ... extra passed through"); Ports (LLM, Embedder, Reranker); Function
signatures (`NewClient(cfg GatewayConfig, ...)`, `NewBlob(ctx, cfg S3Config)`); Behavioural rules (Reflect,
malformed tool calls).
