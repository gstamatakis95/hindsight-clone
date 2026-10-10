// Package harness is the dataset-neutral core of memd's eval: the conversation and question types the
// loaders produce, the Target (memd over gRPC, or stock Hindsight over HTTP) and Judge interfaces, the
// runner that retains each conversation into its own bank, waits for the pending-operations barrier and
// asks every question, and the per-category report (JSON plus Markdown).
package harness
