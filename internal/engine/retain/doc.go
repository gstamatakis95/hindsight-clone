// Package retain turns documents into memory units. It has two halves:
//
//   - Ingestor is the api-side half of the Retain RPC: it validates items (observation-scope fan-out,
//     custom-mode prompt, duplicate document ids), skips items whose content and overrides hash match their
//     document's last completed retain (no write, no model call), stores the original text in the blob store,
//     upserts document rows and then either runs the Retainer inline (sync Retain) or enqueues
//     retain_document jobs (run_async), with a retain_batch parent when there is more than one.
//   - Retainer is the retain_document job handler: chunk -> extract -> normalize -> embed -> resolve ->
//     link -> commit, one short transaction per chunk batch. The first batch replaces the document's old
//     units (deleting their observations and resetting co-sources); the last transaction stamps the
//     document complete, appends RETAIN_COMPLETED and queues the bank's consolidation. Chunk hashes are
//     salted with the run (job) id, so a retried job resumes exactly after the chunks it committed itself
//     (decision 801).
//
// Extraction is one LLM call per chunk (EXTRACT slot) shaped by an ExtractionStrategy per mode
// (concise, verbose, verbatim, chunks, custom); chunks mode stores chunks as units with no call.
// Relative dates are resolved by a DateNormalizer (timex). Entities are resolved by EntityResolver
// (free-form mentions) and LabelResolver (exact-match label entities), and Linker adds temporal,
// semantic and causal edges for freshly inserted units. No model call ever runs inside Store.InTx.
package retain
