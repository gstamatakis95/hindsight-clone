package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
)

// errNoNewFacts is returned by a synthesizer that retrieved nothing to write from. The runner turns it
// into REFRESH_OUTCOME_NO_NEW_FACTS: no version, the watermark and last_refreshed_at advance.
var errNoNewFacts = errors.New("mentalmodel: nothing retrieved in scope")

// Mode fallback reasons recorded in RefreshTrace.ModeFallbackReason when delta was requested but the
// refresh ran in full.
const (
	// fallbackForceFull: the request (or a joined request) asked for force_full.
	fallbackForceFull = "force_full"
	// fallbackNoBaseline: the model has no content to edit yet.
	fallbackNoBaseline = "no_baseline_content"
	// fallbackSourceQueryChanged: the source query or scope changed since the content was written.
	fallbackSourceQueryChanged = "source_query_changed"
	// fallbackSourcesRetracted: a source the document cites was invalidated or replaced.
	fallbackSourcesRetracted = "sources_retracted"
	// fallbackMaxDeltaRatio: the delta changed more of the document than max_delta_ratio allows.
	fallbackMaxDeltaRatio = "max_delta_ratio_exceeded"
)

// refreshOptions are the knobs of one refresh run.
type refreshOptions struct {
	// Manual marks a user-requested refresh (RefreshMentalModel, first build); a manual success clears
	// auto_refresh_paused.
	Manual bool
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool
	// Preview runs the pipeline and returns the outcome and diff without writing anything.
	Preview bool
	// Strategy overrides the spec's refresh strategy; unspecified = the spec's or the bank default.
	Strategy domain.RefreshStrategy
	// Overrides are per-call config overrides, above the model's stored overrides; nil = none.
	Overrides *domain.BankConfig
	// OperationID is the refresh_mental_model operation, for events and checkpoints; empty for a preview.
	OperationID domain.OperationID
}

// RefreshOutcome is the result of one refresh run (the struct; domain.RefreshOutcome is the enum held in
// Trace.Outcome). It carries everything a refresh preview reports.
type RefreshOutcome struct {
	// Trace explains the refresh, including its domain.RefreshOutcome.
	Trace domain.RefreshTrace
	// RequestedMode is the mode the trigger asked for, before any fallback to full.
	RequestedMode domain.RefreshMode
	// ResolvedScope is the scope the refresh searched.
	ResolvedScope ResolvedScope
	// VersionWritten is the new version number; 0 when nothing was written (no new facts, failure or
	// preview).
	VersionWritten int64
	// ContentMD is the drafted document (written, or what a preview would write).
	ContentMD string
	// Diff is a unified diff of ContentMD against the stored content.
	Diff string
	// WindowAfterSeq is the lower seq bound of the read window (exclusive; last_seq_seen for delta).
	WindowAfterSeq int64
	// WindowBeforeSeq is the upper seq bound of the read window (inclusive; the snapshot upTo).
	WindowBeforeSeq int64
	// FactsRetrieved counts the units the synthesizer retrieved.
	FactsRetrieved int
	// FactsUsed counts the units the document cites.
	FactsUsed int
	// WouldPersist reports whether a non-preview run would write a version.
	WouldPersist bool
}

// ResolvedScope mirrors memory.v1.ResolvedScope: the effective scope of a model after defaults.
type ResolvedScope struct {
	// Tags is the effective tag filter (domain.MentalModelSpec.ScopeFilter): the scope's groups, or the
	// model's tags under the effective match mode.
	Tags domain.TagFilter
	// FactTypes limits the scope; empty = all.
	FactTypes []domain.FactType
}

// synthesizer drafts a model's content. Two implementations, picked by RefreshStrategy:
//
//   - agentic: the reflect loop, answering as a document (Hindsight's behaviour);
//   - single-shot: one recall over the scope, then one synthesize (full) or delta call.
//
// Both return errNoNewFacts when retrieval finds nothing, before any LLM call they would otherwise make.
type synthesizer interface {
	// Strategy is the refresh strategy the synthesizer implements.
	Strategy() domain.RefreshStrategy
	// Full writes the document from everything in scope with seq <= upTo.
	Full(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (draft, error)
	// Delta proposes edit ops for what changed in scope with LastSeqSeen < seq <= upTo and applies them
	// to m.ContentMD (with the one reject-and-retry). When every op is rejected it returns the draft (for
	// the trace) and an error wrapping errAllOpsRejected; the caller keeps the document.
	Delta(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (deltaDraft, error)
}

// draft is a synthesizer's full-refresh output.
type draft struct {
	// ContentMD is the drafted markdown document.
	ContentMD string
	// SourceIDs are the units the document is based on.
	SourceIDs []domain.UnitID
	// StructuredOutputJSON is the projection onto the spec's response schema; nil = none.
	StructuredOutputJSON json.RawMessage
	// Usage is per-slot token usage.
	Usage []domain.Usage
	// ToolCalls are the agentic loop's tool calls (agentic only).
	ToolCalls []domain.ToolCallTrace
	// FactsRetrieved counts the units retrieved while drafting.
	FactsRetrieved int
}

// deltaDraft is a synthesizer's delta-refresh output.
type deltaDraft struct {
	// Ops are the final attempt's ops with Applied and SkipReason filled in.
	Ops []domain.DeltaOp
	// ContentMD is m.ContentMD with the applied ops (unchanged when none applied).
	ContentMD string
	// SourceIDs are the new units the edits are based on; the runner adds them to the model's.
	SourceIDs []domain.UnitID
	// Usage is per-slot token usage.
	Usage []domain.Usage
	// ToolCalls are the agentic loop's tool calls (agentic only).
	ToolCalls []domain.ToolCallTrace
	// FactsRetrieved counts the units retrieved in the window.
	FactsRetrieved int
}
