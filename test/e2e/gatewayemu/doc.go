// Package gatewayemu is a deterministic, in-process emulation of the OpenAI-compatible AI gateway memd talks to: POST
// /v1/chat/completions (plain, tools and response_format json_schema), /v1/embeddings and /v1/rerank, the routes and
// defaults internal/adapter/gateway uses. It answers every prompt memd sends with schema-valid output, so memd runs end
// to end with no model in the loop: the e2e tests (test/e2e), the golden fixtures and local development through
// cmd/gatewayemu use it. Its answers exercise memd's plumbing; they are not real answers.
//
// It is a regular (non-test) package. It reads memd's prompt template names from internal/prompts and validates its own
// output with internal/jsonschema, the gateway adapter's validator.
//
// # Usage
//
//	emu, ts := gatewayemu.Start(gatewayemu.Options{}) // or serve gatewayemu.New(opts) as an http.Handler
//	defer ts.Close()
//	// memd: MEMD_GATEWAY_URL = ts.URL + gatewayemu.BasePath, MEMD_MODEL_<SLOT> = gatewayemu.ModelFor(slot)
//	emu.Inject(gatewayemu.Fault{Op: gatewayemu.OpEmbeddings, Times: 2, Status: 503})
//	n := emu.Count("consolidate_system")
//
// # Recognising requests
//
// A request is recognised as an operation without matching prompt text:
//
//   - a structured call by its json_schema name, which is the prompt template name;
//   - a chat that offers tools as [OpReflectLoop] (a reflect or agentic-refresh turn);
//   - a tool-less chat by its model: memd binds every slot to "emu-<slot>" ([ModelPrefix], [ModelFor]), so emu-reflect
//     is [OpReflectFinal] and emu-synthesize [OpSynthesize]; any other model is [OpChat];
//   - /embeddings and /rerank as [OpEmbeddings] and [OpRerank].
//
// The data a reply needs is recovered from the rendered prompts by their structural markers (the chunk after the
// extract header's Content: line, "[id] text" fact lines, "- [type:id] text" source lines, fenced JSON and tool
// output). TestPromptTextRoundTrip renders the real templates and checks the emulator recovers exactly what was put in,
// so a template change that breaks parsing fails `make test`.
//
// # Answers
//
//   - extract_*: one fact per sentence of the chunk ([Sentences]), built from the schema's fact item so mode-specific
//     fields are present; extract_verbatim returns a single entry.
//   - time_parse_system: no window (start and end null).
//   - consolidate_system: one new observation per new fact, citing the fact's id, its text an exact quote.
//   - dedup_system: keep.
//   - mm_structured_delta_system: one add_section ("New facts") quoting the new facts, none without facts.
//   - structured_output_system: an instance of the caller's schema built from the answer text; any other schema name
//     gets a generic instance of its schema.
//   - reflect loop: a recall (or the first offered search tool) call while the conversation has no tool message, then
//     done, with an answer quoting the retrieved texts and citing their ids, shaped by the done tool's declared
//     parameters.
//   - reflect final, synthesize and markdown delta: the final answer quoting the retrieved data, a page with the topic
//     as heading and the facts quoted, and the current document with the facts appended; OpChat gets the quoting
//     answer.
//
// Every structured output is validated against the request's schema; a mismatch is an emulator bug and returns 400 so
// the client fails at once.
//
// Embeddings ([Embed], [EmbedMode]) are 768-dimensional and L2-normalised. In the default EmbedSharedTokens mode they
// are hashed bags of content words ([Tokens]) with a small constant bias component, so texts that share tokens are
// similar and no vector is zero; the nomic task prefixes are stripped first. EmbedTextHash makes only equal texts
// similar. Rerank scores each document by the share of query tokens it contains ([Overlap]), in [0,1], so the adapter
// passes the scores through. Usage is estimated at one token per four bytes.
//
// # Faults and the call log
//
// [Server.Inject] scripts a [Fault] for one operation (or all, with Op ""): fail Times requests (0 = until
// [Server.ClearFaults]) with a status and body (500 and 429 are retried by the gateway client, 400 is not, and a 400
// saying context_length_exceeded is a context overflow), a delay that ends when the request is cancelled, or invalid
// JSON. Faults apply in injection order. [Server.Calls] and [Server.Count] report every answered request with its
// operation, model, status and whether a fault shaped it. The Server is safe for concurrent use; faults are global to
// it, so tests that inject faults run one after another.
//
// # Testing
//
// prompttext_test.go round-trips the real templates; smoke_test.go answers every prompt schema memd has, including the
// reflect loop and plain chats, through the real gateway client; server_test.go covers embeddings, rerank and faults.
package gatewayemu
