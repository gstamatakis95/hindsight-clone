// Command gatewayemu serves the deterministic gateway emulator (memd/test/e2e/gatewayemu) on a TCP address, so memd can
// run locally without a real AI gateway. It answers every prompt memd sends with schema-valid output and
// 768-dimensional embeddings; it is for development and tests, not for real answers.
//
// # Usage
//
//	go run ./test/e2e/gatewayemu/cmd/gatewayemu [-addr 127.0.0.1:4000] [-write-config memd.yaml]
//
// The server has no TLS and no authentication (any API key is accepted) and runs until it is killed. At start it logs
// the gateway URL memd needs (export MEMD_GATEWAY_URL=http://127.0.0.1:4000/v1), and with -write-config it writes a
// memd server config file (gatewayemu.ServerConfig) binding every model slot (extract, time_parse, consolidate,
// dedup_check, reflect, structured_output, mental_model, rerank) to its emu-<slot> model, which is how the emulator
// recognises tool-less chats. The README quick start uses both. The embedding model needs no binding: the
// emulator answers /v1/embeddings for any model. It runs with the default options (shared-token embeddings) and no
// scripted faults.
package main
