// Package mentalmodel keeps mental models (knowledge pages) up to date: synthesized markdown documents
// that each answer one source query over a tag and fact-type scope of the bank.
//
// ChangeDetector decides staleness from the bank's change sequence (an in-scope unit or observation
// with seq > last_seq_seen, or a retracted source). RefreshRunner is the one pipeline behind the
// refresh_mental_model job and DryRunRefresh: it reads a stable snapshot (seq <= upTo), picks a
// Synthesizer by RefreshStrategy (agentic: the reflect loop answering as a document; single-shot: one
// recall plus one SYNTHESIZE or MENTAL_MODEL_DELTA call), applies delta edit ops or rewrites in full,
// optionally projects structured output, and writes a version in one short transaction. A dry run
// writes nothing. RefreshScheduler decides whether an automatic trigger runs now, parks (debounce,
// interval floor) or is skipped, and PageSearcher ranks whole pages for SearchPages.
//
// mentalmodel imports recall and reflect and declares the small Reflector and Recaller it consumes;
// neither imports mentalmodel.
package mentalmodel
