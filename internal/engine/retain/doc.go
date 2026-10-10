// Package retain turns documents into memory units. It has two halves:
//
//   - Ingestor is the api-side half of the Retain RPC: it validates items (observation-scope fan-out,
//     overrides), stores original text in the blob store, upserts document rows and then either runs
//     the Retainer inline (sync Retain) or enqueues retain_document jobs (run_async), with a
//     retain_batch parent when there is more than one document.
//   - Retainer is the retain_document job handler: chunk -> extract -> normalize -> embed -> resolve ->
//     link -> commit, one short transaction per chunk batch, resuming by chunk hash after a crash.
//
// Extraction is one LLM call per chunk (EXTRACT slot) shaped by an ExtractionStrategy per mode
// (concise, verbose, verbatim, chunks, custom); chunks mode stores chunks as units with no call.
// Relative dates are resolved by timex.TimeNormalizer. Entities are resolved by EntityResolver
// (free-form mentions) and LabelResolver (exact-match label entities), and Linker adds temporal,
// semantic and causal edges for freshly inserted units. No model call ever runs inside Store.InTx.
package retain
