package mentalmodel

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
)

// RefreshRunner is the one pipeline behind refresh and dry run; DryRun writes nothing at all.
// It picks the Synthesizer from the effective RefreshStrategy.
type RefreshRunner interface {
	// Run refreshes model id of bank b (or dry-runs it) and reports what happened. A failed refresh
	// records a refresh_failed history entry and pauses automatic refreshes; the document stays as it was.
	Run(ctx context.Context, b port.BankRef, id port.MentalModelID, opts RefreshOptions) (RefreshOutcome, error)
}

// RefreshOptions are the knobs of one refresh run.
type RefreshOptions struct {
	// Manual marks a user-requested refresh (RefreshMentalModel, first build); a manual success clears
	// auto_refresh_paused.
	Manual bool
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool
	// DryRun runs the pipeline and returns the outcome and diff without writing anything.
	DryRun bool
	// Strategy overrides the trigger's refresh strategy; unspecified = the trigger's or the bank default.
	Strategy domain.RefreshStrategy
	// Overrides are per-call config overrides, above the model's stored overrides; nil = none.
	Overrides *domain.BankConfig
	// OperationID is the refresh_mental_model operation, for events and checkpoints; empty for a dry run.
	OperationID port.OperationID
}

// RefreshOutcome is the result of one refresh run (the struct; domain.RefreshOutcome is the enum held in
// Trace.Outcome). It carries everything DryRunRefreshResponse reports.
type RefreshOutcome struct {
	// Trace explains the refresh, including its domain.RefreshOutcome.
	Trace domain.RefreshTrace
	// RequestedMode is the mode the trigger asked for, before any fallback to full.
	RequestedMode domain.RefreshMode
	// ResolvedScope is the scope the refresh searched.
	ResolvedScope ResolvedScope
	// VersionWritten is the new version number; 0 when nothing was written (no new facts, failure or
	// dry run).
	VersionWritten int64
	// ContentMD is the drafted document (written, or what a dry run would write).
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
	// WouldPersist reports whether a non-dry run would write a version.
	WouldPersist bool
}

// ResolvedScope mirrors memory.v1.ResolvedScope: the effective scope of a model after defaults.
type ResolvedScope struct {
	// Tags are the model's tags.
	Tags []string
	// TagsMatch is the effective match mode for Tags.
	TagsMatch domain.TagsMatch
	// TagGroups override Tags and TagsMatch when set.
	TagGroups []domain.TagGroup
	// FactTypes limits the scope; empty = all.
	FactTypes []domain.FactType
}

// Synthesizer drafts a model's content. Two implementations, picked by RefreshStrategy:
//
//   - agentic: the reflect loop, answering as a document (Hindsight's behaviour);
//   - single-shot: one recall over the scope, then one SYNTHESIZE (full) or MENTAL_MODEL_DELTA call.
type Synthesizer interface {
	// Strategy is the refresh strategy the synthesizer implements.
	Strategy() domain.RefreshStrategy
	// Full writes the document from everything in scope with seq <= upTo.
	Full(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (Draft, error)
	// Delta returns edit ops for what changed in scope with LastSeqSeen < seq <= upTo.
	Delta(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) ([]EditOp, error)
}

// Draft is a synthesizer's full-refresh output.
type Draft struct {
	// ContentMD is the drafted markdown document.
	ContentMD string
	// SourceIDs are the units the document is based on.
	SourceIDs []port.UnitID
	// StructuredOutputJSON is the projection onto the trigger's response schema; nil = none.
	StructuredOutputJSON json.RawMessage
	// Usage is per-slot token usage.
	Usage []domain.Usage
	// ToolCalls are the agentic loop's tool calls (agentic only).
	ToolCalls []domain.ToolCallTrace
	// FactsRetrieved counts the units retrieved while drafting.
	FactsRetrieved int
}
