// Command model-per-step shows how to spend model budget where it matters: a cheap model for the high-volume
// steps (extraction, consolidation), a strong model for the steps that talk to the user (reflect, mental models)
// and a cross-encoder for reranking. The bindings are set on the bank, then overridden for single requests, and
// the usage reported by each call proves which model served which step.
package main
