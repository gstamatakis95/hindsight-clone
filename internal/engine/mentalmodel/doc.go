// Package mentalmodel keeps mental models (knowledge pages) up to date: synthesized markdown documents
// that each answer one source query over a tag and fact-type scope of the bank.
//
// ChangeDetector decides staleness from the bank's change sequence (an in-scope unit or observation
// with seq > last_seq_seen, untagged units never staling a tagged model, or a retracted source).
// RefreshRunner is the one pipeline behind the refresh_mental_model job (RunJob) and DryRunRefresh: it
// reads BankSeq first (the snapshot bound S), picks the Synthesizer by RefreshStrategy (agentic: the
// reflect loop answering as a document; single-shot: one recall plus one SYNTHESIZE or MENTAL_MODEL_DELTA
// call) and the mode (delta falls back to full with a recorded reason), skips every model call when
// nothing is in scope or in the window, applies delta edit ops on section and block anchors (one retry
// when every op is rejected; never a partial document), optionally projects structured output, and
// writes a version in one short transaction with last_seq_seen = S. A dry run writes nothing.
//
// RefreshScheduler decides whether an automatic trigger runs now, parks (debounce, interval floor) or
// is skipped (paused, fresh); EnqueueRefresh queues a refresh or joins and upgrades the queued one.
// Triggers fires refreshes after consolidation, on retracted sources and on cron schedules
// (worker.CronRefresher); RefreshFailureReconciler pauses models whose refresh failed for good
// (worker.RefreshFailureReconciler). Admin serves the CRUD, refresh, clear, history and folder RPCs and
// PageSearcher ranks whole pages for SearchPages.
//
// Reads the ports lack (in-scope newest seq, model listing, cron models, failed refreshes) are declared
// in reads.go and requested in CCR WP-12-1. mentalmodel imports recall and reflect for their request
// types and declares the small Reflector, Recaller and Projector it consumes; neither imports mentalmodel.
package mentalmodel
