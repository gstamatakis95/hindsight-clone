// Command gatewayemu serves the deterministic gateway emulator (memd/test/e2e/gatewayemu) on a TCP address, so memd can
// run locally without a real AI gateway. It answers every prompt memd sends with schema-valid output and
// 768-dimensional embeddings; it is for development and tests, not for real answers.
//
// # Usage
//
//	go run ./test/e2e/gatewayemu/cmd/gatewayemu [-addr 127.0.0.1:4000]
//
// The server has no TLS and no authentication (any API key is accepted) and runs until it is killed. At start it logs
// the environment memd needs, which the README quick start uses:
//
//	export MEMD_GATEWAY_URL=http://127.0.0.1:4000/v1 MEMD_MODEL_EXTRACT=emu-extract ...
//
// That is, MEMD_GATEWAY_URL points at the emulator's /v1 base path and every model slot (extract, time_parse,
// consolidate, dedup_check, reflect, structured_output, mental_model_delta, synthesize, rerank) is bound to its
// emu-<slot> model, which is how the emulator recognises tool-less chats. The embedding model needs no binding: the
// emulator answers /v1/embeddings for any model. It runs with the default options (shared-token embeddings) and no
// scripted faults.
package main
