// Package ai is a stub of the model interfaces.
package ai

import "context"

// LLM is a stub.
type LLM interface{ Complete(ctx context.Context) error }

// Embedder is a stub.
type Embedder interface{ Embed(ctx context.Context) error }

// Reranker is a stub.
type Reranker interface{ Rerank(ctx context.Context) error }

// Router is a stub.
type Router struct{}

// Resolve is a stub.
func (*Router) Resolve() {}
