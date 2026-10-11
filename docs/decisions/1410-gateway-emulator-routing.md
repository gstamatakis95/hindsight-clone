# 1410 — How the gateway emulator recognises prompts and what it answers

## Context
The WP-14a card asks for a deterministic OpenAI-compatible gateway emulator (`test/e2e/gatewayemu`). It must
answer every structured prompt memd sends with schema-valid output. It must recognise the prompt "by template
name or schema name, not by brittle text matching".

The gateway adapter (`internal/adapter/gateway`) puts the template name on the wire only for `Structured`
calls, as `response_format.json_schema.name`. `ChatRequest.Name` is never sent. The plain and tool chats
(reflect loop, reflect final answer, SYNTHESIZE, markdown delta) carry only `model`, `messages` and `tools`.

## Decision
- **Structured calls are routed on the schema name.**
  - `extract_*`: one fact per sentence of the chunk. `extract_verbatim` returns a single entry instead.
  - `time_parse_system`: `{start: null, end: null}`.
  - `consolidate_system`: one create per new fact, citing that fact's id, with the fact's exact text.
  - `dedup_system`: keep.
  - `mm_structured_delta_system`: a single `add_section` op ("New facts") quoting the new facts. This op needs
    no ids from the document, so it always applies. When there are no facts, it returns no ops.
  - `structured_output_system`: an instance of the caller's schema.
  - Any other name: a generic instance of its schema.
- **Every output is validated against the request schema** with `internal/jsonschema`. A mismatch is an
  emulator bug, and the emulator answers it with a 400 so the client fails at once.
- **Chats are routed by tools, then by the slot's model name.** The harness binds every slot to
  `emu-<slot>` (`MEMD_MODEL_<SLOT>`, read by `config.ServerDefaults`).
  - A chat that offers tools is a reflect turn:
    - It calls `recall` (or the first offered search tool) while the conversation has no tool message.
    - Once there is a tool message, or when `tool_choice` names `done`, it calls `done`. The answer quotes
      the retrieved texts and the call cites their ids. The arguments follow the offered done tool's declared
      parameters: `answer` or `document`, plus `directive_compliance` when declared.
  - A tool-less chat is routed on its model:
    - `emu-reflect` gets the final answer quoting the retrieved data.
    - `emu-synthesize` gets a page with the topic as heading and the facts quoted.
    - `emu-mental_model_delta` gets the current document with the facts appended.
  - A tool-less chat on any other model (for example a per-request override) gets the quoting answer.
- **Data is recovered from the rendered prompts by their structural markers.** These are the chunk after the
  first `Content:` line following the extract header, `[id] text` fact lines, `- [type:id] text` source
  lines, fenced JSON blocks and tool-output JSON. `TestPromptTextRoundTrip` renders the real templates and
  checks that the emulator recovers exactly what was put in, so a template change that breaks parsing fails
  `make test`.
- **Embeddings** are a hashed bag of content words: two dimensions per token plus a small constant bias, so a
  vector is never zero. They are L2-normalised and 768-dimensional. The nomic prefixes are stripped before
  hashing. `EmbedTextHash` is the alternative mode, in which only equal texts are similar.
- **Rerank** scores each document by the share of query tokens it contains, in [0,1], so the adapter passes
  the scores through unchanged.
- **Faults** are scripted per operation:
  - fail N times (any status, with an optional body such as `context_length_exceeded`);
  - a delay that ends when the request is cancelled;
  - invalid JSON.
- Every answered request is logged as a `Call`.

## Alternatives
- **Match prompts on their text** (system-prompt prefixes). Rejected: the card forbids it, and any prompt edit
  would break it.
- **Add the template name to the chat body** (for example through the binding's `extra`). Rejected: this
  needs an adapter change outside WP-14a, and per-slot models already identify the slot.
- **Return 400 for any unrecognised chat.** Rejected: a scenario that overrides a slot's model per request
  would then fail for a reason unrelated to what it tests. The call log shows how each request was routed.

## Spec lines involved
- SPEC "Model routing": slots, per-slot bindings, `MEMD_MODEL_*` through `config.ServerDefaults`, and the
  nomic prefixes.
- Decision 300 (gateway wire contract), 303 (JSON Schema subset), 404 (prompt layout).
