package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/engine/reflect"
	"memd/internal/port"
)

// ErrNoNewFacts is returned by a Synthesizer that retrieved nothing to write from. The runner turns it
// into REFRESH_OUTCOME_NO_NEW_FACTS: no version, the watermark and last_refreshed_at advance.
var ErrNoNewFacts = errors.New("mentalmodel: nothing retrieved in scope")

// Mode fallback reasons recorded in RefreshTrace.ModeFallbackReason when delta was requested but the
// refresh ran in full.
const (
	// FallbackForceFull: the request (or a joined request) asked for force_full.
	FallbackForceFull = "force_full"
	// FallbackNoBaseline: the model has no content to edit yet.
	FallbackNoBaseline = "no_baseline_content"
	// FallbackSourceQueryChanged: the source query or scope changed since the content was written.
	FallbackSourceQueryChanged = "source_query_changed"
	// FallbackSourcesRetracted: a source the document cites was invalidated or replaced.
	FallbackSourcesRetracted = "sources_retracted"
	// FallbackMaxDeltaRatio: the delta changed more of the document than max_delta_ratio allows.
	FallbackMaxDeltaRatio = "max_delta_ratio_exceeded"
)

// RefreshRunner is the one pipeline behind refresh and dry run; DryRun writes nothing at all.
// It picks the Synthesizer from the effective RefreshStrategy.
type RefreshRunner interface {
	// Run refreshes model id of bank b (or dry-runs it) and reports what happened. A failed run writes
	// nothing and returns the error with Trace.Outcome = failed; the document stays as it was. Pausing and
	// the REFRESH_FAILED history entry happen once the operation fails for good, in the maintenance
	// reconcile (RefreshFailureReconciler), so job retries are not recorded as failures.
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
//
// Both return ErrNoNewFacts when retrieval finds nothing, before any LLM call they would otherwise make.
type Synthesizer interface {
	// Strategy is the refresh strategy the synthesizer implements.
	Strategy() domain.RefreshStrategy
	// Full writes the document from everything in scope with seq <= upTo.
	Full(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (draft, error)
	// Delta proposes edit ops for what changed in scope with LastSeqSeen < seq <= upTo and applies them
	// to m.ContentMD (with the one reject-and-retry). When every op is rejected it returns the draft (for
	// the trace) and an error wrapping ErrAllOpsRejected; the caller keeps the document.
	Delta(ctx context.Context, m domain.MentalModel, cfg domain.BankConfig, upTo int64) (deltaDraft, error)
}

// draft is a synthesizer's full-refresh output.
type draft struct {
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

// deltaDraft is a synthesizer's delta-refresh output.
type deltaDraft struct {
	// Ops are the final attempt's ops with Applied and SkipReason filled in.
	Ops []domain.DeltaOp
	// ContentMD is m.ContentMD with the applied ops (unchanged when none applied).
	ContentMD string
	// SourceIDs are the new units the edits are based on; the runner adds them to the model's.
	SourceIDs []port.UnitID
	// Usage is per-slot token usage.
	Usage []domain.Usage
	// ToolCalls are the agentic loop's tool calls (agentic only).
	ToolCalls []domain.ToolCallTrace
	// FactsRetrieved counts the units retrieved in the window.
	FactsRetrieved int
}

// Projector is the part of reflect.StructuredProjector the runner consumes to project a finished page
// onto the trigger's response schema; reflect.StructuredProjector satisfies it.
type Projector interface {
	// Project makes one STRUCTURED_OUTPUT call and returns JSON valid against req.Schema.
	Project(ctx context.Context, req reflect.ProjectRequest) (json.RawMessage, error)
}

// RefreshRunnerDeps are the RefreshRunner's dependencies.
type RefreshRunnerDeps struct {
	// Store reads the model and writes the version.
	Store port.Store
	// Config merges server < tenant < bank < model overrides < trigger < call overrides.
	Config port.ConfigResolver
	// Scope answers the no-LLM "anything in scope / in the window?" check.
	Scope ScopeReader
	// Synthesizers holds one Synthesizer per strategy.
	Synthesizers []Synthesizer
	// Projector projects the page onto trigger.response_schema_json; nil = a model with a schema fails.
	Projector Projector
	// Embedder embeds the page for SearchPages; nil = pages are stored without an embedding.
	Embedder port.Embedder
	// Clock stamps last_refreshed_at, history and traces.
	Clock port.Clock
}
