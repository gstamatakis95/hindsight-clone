// Package fake provides in-memory implementations of every port (Store with its Tx, Reader and repos,
// Queue and Claimer, Blob, LLM, Embedder, Reranker, Clock) for engine unit tests. Each fake passes the
// same internal/porttest contract suite as its production adapter, so fakes can't drift from real
// behaviour. Model fakes are scripted per prompt name and slot and record their calls.
package fake
