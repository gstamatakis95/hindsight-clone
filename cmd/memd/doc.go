// Command memd is the agent-memory gRPC service. One binary runs in three modes: api (the gRPC server:
// Recall, Reflect, sync Retain and every admin RPC), worker (the job runner and maintenance tick) or
// all (both in one process). Configuration comes from flags and the environment; wire.go is the only
// composition root, building adapters and narrowing them to the ports engines consume.
package main
