// Command gatewayemu serves the deterministic gateway emulator (memd/test/e2e/gatewayemu) on a TCP address,
// so memd can run locally without a real AI gateway: the README quick start points MEMD_GATEWAY_URL at it
// and binds every slot to its emu-<slot> model. It answers every prompt memd sends with schema-valid
// output and 768-dimensional embeddings; it is for development and tests, not for real answers.
package main
