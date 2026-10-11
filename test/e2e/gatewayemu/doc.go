// Package gatewayemu is a deterministic, in-process emulation of the OpenAI-compatible AI gateway memd talks
// to: /v1/chat/completions (plain, tools and response_format json_schema), /v1/embeddings and /v1/rerank, the
// routes internal/adapter/gateway uses. It answers every structured prompt memd sends with schema-valid JSON
// (decision 1410):
//
//   - extraction: one fact per sentence of the chunk (one metadata entry in verbatim mode);
//   - TIME_PARSE: no window (start and end null);
//   - consolidation: one new observation per fact, citing the fact's id, its text an exact quote;
//   - dedup: keep;
//   - reflect: one recall call, then done with an answer quoting the retrieved texts and citing their ids;
//   - mental-model synthesize, markdown delta and structured delta (one add_section with the new facts);
//   - structured output: an instance of the caller's schema built from the answer text.
//
// Structured calls are recognised by their json_schema name (the prompt template name), chats by the slot
// model name ("emu-<slot>", see ModelFor) and the presence of tools. Embeddings are 768-dimensional hashed
// bags of words, so texts that share tokens are similar; rerank scores by query-token overlap. Faults (fail N
// times, delay, invalid JSON) can be scripted per operation for crash, retry and cancel scenarios.
package gatewayemu
