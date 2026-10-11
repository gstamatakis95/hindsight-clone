package mentalmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/engine/reflect"
	"memd/internal/port"
	"slices"
	"strings"
	"time"
)

// runner is the RefreshRunner.
type runner struct {
	d      RefreshRunnerDeps
	synths map[domain.RefreshStrategy]Synthesizer
}

var _ RefreshRunner = (*runner)(nil)

// NewRefreshRunner returns the RefreshRunner over d.
func NewRefreshRunner(d RefreshRunnerDeps) RefreshRunner {
	r := &runner{d: d, synths: map[domain.RefreshStrategy]Synthesizer{}}
	for _, s := range d.Synthesizers {
		r.synths[s.Strategy()] = s
	}
	return r
}

// refreshPlan is what one run decided before drafting.
type refreshPlan struct {
	m         domain.MentalModel
	cfg       domain.BankConfig
	synth     Synthesizer
	requested domain.RefreshMode
	mode      domain.RefreshMode
	reason    string
	upTo      int64
	window    port.SeqRange
	// automatic: neither manual, force_full nor a dry run; such a run re-checks staleness and the
	// interval floor before drafting.
	automatic bool
}

// drafted is a run's drafting result.
type drafted struct {
	content    string
	sourceIDs  []port.UnitID
	structured json.RawMessage
	usage      []domain.Usage
	toolCalls  []domain.ToolCallTrace
	ops        []EditOp
	retrieved  int
	used       int
	noNewFacts bool
	// nothingInScope: the no-LLM pre-check found nothing in scope or in the window (SPEC 2002).
	nothingInScope bool
	// floored: an automatic run started inside the interval floor; it writes nothing at all, so the
	// floor does not slide and the watermark holds.
	floored bool
	// fresh: an automatic run found its model fresh (a duplicate or racing trigger); it writes nothing.
	fresh     bool
	warnings  []string
	embedding []float32
}

// Run reads the model and BankSeq (the snapshot bound), plans the mode and strategy, drafts, and writes
// the result in one short transaction (nothing for a dry run).
func (r *runner) Run(
	ctx context.Context, b port.BankRef, id port.MentalModelID, opts RefreshOptions,
) (RefreshOutcome, error) {
	start := r.d.Clock.Now()
	p, err := r.plan(ctx, b, id, opts)
	if err != nil {
		return RefreshOutcome{Trace: domain.RefreshTrace{Outcome: domain.RefreshOutcomeFailed}}, err
	}
	dr, err := r.draft(ctx, p)
	out := RefreshOutcome{
		RequestedMode: p.requested, ResolvedScope: resolveScope(p.m),
		WindowAfterSeq: p.window.After, WindowBeforeSeq: p.upTo, Trace: r.trace(p, dr, start),
	}
	out.FactsRetrieved, out.FactsUsed = dr.retrieved, dr.used
	if err != nil {
		out.Trace.Outcome = domain.RefreshOutcomeFailed
		return out, err
	}
	out.ContentMD = dr.content
	out.Diff = diffMarkdown(p.m.ContentMD, dr.content)
	out.WouldPersist = !dr.noNewFacts && dr.content != p.m.ContentMD
	if !out.WouldPersist {
		out.Trace.Outcome = domain.RefreshOutcomeNoNewFacts
	}
	if opts.DryRun {
		return out, nil
	}
	if dr.floored {
		return out, r.repark(ctx, p)
	}
	if dr.fresh {
		return out, nil
	}
	if out.WouldPersist {
		r.embed(ctx, p, &dr, &out.Trace)
	}
	out.VersionWritten, err = r.write(ctx, p, dr, opts, out.Trace)
	if err != nil {
		out.Trace.Outcome = domain.RefreshOutcomeFailed
		out.VersionWritten = 0
		return out, err
	}
	return out, nil
}

// plan loads the model, reads BankSeq, merges the config and picks the strategy, mode and window.
func (r *runner) plan(
	ctx context.Context, b port.BankRef, id port.MentalModelID, opts RefreshOptions,
) (*refreshPlan, error) {
	m, err := loadModel(ctx, r.d.Store, b, id)
	if err != nil {
		return nil, err
	}
	upTo, err := r.d.Store.Read().BankSeq(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: bank seq: %w", err)
	}
	cfg, err := r.d.Config.Effective(ctx, b, configLevels(m, opts.Overrides)...)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: config: %w", err)
	}
	p := &refreshPlan{m: m, cfg: cfg, upTo: upTo, automatic: !opts.Manual && !opts.ForceFull && !opts.DryRun}
	strategy := effectiveStrategy(opts.Strategy, m, cfg)
	if p.synth = r.synths[strategy]; p.synth == nil {
		return nil, fmt.Errorf("%w: no synthesizer for refresh strategy %q", domain.ErrInvalidArgument, strategy)
	}
	p.requested = requestedMode(m, cfg)
	p.mode, p.reason = p.requested, ""
	if p.requested == domain.RefreshModeDelta {
		if p.reason, err = r.fallbackReason(ctx, m, opts); err != nil {
			return nil, err
		}
		if p.reason != "" {
			p.mode = domain.RefreshModeFull
		}
	}
	p.window = port.SeqUpTo(0, upTo)
	if p.mode == domain.RefreshModeDelta {
		p.window = port.SeqUpTo(m.LastSeqSeen, upTo)
	}
	return p, nil
}

// fallbackReason says why a requested delta must run in full; "" = it can run as a delta.
func (r *runner) fallbackReason(ctx context.Context, m domain.MentalModel, opts RefreshOptions) (string, error) {
	switch {
	case opts.ForceFull:
		return FallbackForceFull, nil
	case strings.TrimSpace(m.ContentMD) == "":
		return FallbackNoBaseline, nil
	case m.LastSeqSeen == 0:
		return FallbackSourceQueryChanged, nil
	}
	retracted, err := retractedSources(ctx, r.d.Store.Read(), m)
	if err != nil {
		return "", err
	}
	if retracted {
		return FallbackSourcesRetracted, nil
	}
	return "", nil
}

// draft checks the window without a model call, then runs the synthesizer (delta, falling back to full
// past max_delta_ratio), then projects structured output. The window check reads the model's full scope
// (untagged units included where the scope reads them); only staleness excludes untagged units.
func (r *runner) draft(ctx context.Context, p *refreshPlan) (drafted, error) {
	if r.floored(p) {
		return drafted{content: p.m.ContentMD, noNewFacts: true, floored: true}, nil
	}
	fresh, err := r.freshAutomatic(ctx, p)
	if err != nil {
		return drafted{content: p.m.ContentMD}, err
	}
	if fresh {
		return drafted{content: p.m.ContentMD, noNewFacts: true, fresh: true}, nil
	}
	q, err := scopeQuery(ctx, r.d.Scope, p.m, p.window, false, recall.FuzzyTagMinSimilarity(p.cfg))
	if err != nil {
		return drafted{content: p.m.ContentMD}, err
	}
	newest, err := r.d.Scope.NewestSeqInScope(ctx, q)
	if err != nil {
		return drafted{content: p.m.ContentMD}, fmt.Errorf("mentalmodel: scope seq: %w", err)
	}
	if newest == 0 {
		return drafted{content: p.m.ContentMD, noNewFacts: true, nothingInScope: true}, nil
	}
	var dr drafted
	if p.mode == domain.RefreshModeDelta {
		dr, err = r.delta(ctx, p)
		if err != nil || p.mode == domain.RefreshModeDelta {
			return r.finish(ctx, p, dr, err)
		}
	}
	full, err := r.full(ctx, p)
	full.usage = append(dr.usage, full.usage...)
	full.toolCalls = append(dr.toolCalls, full.toolCalls...)
	return r.finish(ctx, p, full, err)
}

// floorSkew is the clock-skew tolerance of the interval floor: a run with at most this much of the floor
// left is not floored, and a re-park is never due sooner than this, so DB / worker skew can't loop.
const floorSkew = 5 * time.Second

// floored reports whether an automatic run started inside the model's interval floor with more than
// floorSkew of it left.
func (r *runner) floored(p *refreshPlan) bool {
	floor, last := intervalFloor(p.m, p.cfg), p.m.LastRefreshedAt
	return p.automatic && floor > 0 && last != nil && r.d.Clock.Now().Add(floorSkew).Before(last.Add(floor))
}

// repark re-queues an automatic run that started inside the interval floor with run_at = the floor's end
// (SPEC: the floor parks a triggered refresh), joining a pending refresh if one exists. The running job is
// still PROCESSING, so the pending-only dedupe inserts a new row behind it. Nothing else is written.
func (r *runner) repark(ctx context.Context, p *refreshPlan) error {
	now := r.d.Clock.Now()
	end := p.m.LastRefreshedAt.Add(intervalFloor(p.m, p.cfg))
	if soonest := now.Add(floorSkew); end.Before(soonest) {
		end = soonest
	}
	err := r.d.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Seq(ctx, p.m.Bank); err != nil {
			return err
		}
		_, _, err := EnqueueRefresh(ctx, tx, RefreshRequest{Bank: p.m.Bank, ID: p.m.Spec.ID, RunAt: end}, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("mentalmodel: re-park %s: %w", p.m.Spec.ID, err)
	}
	return nil
}

// freshAutomatic reports whether an automatic run finds its model fresh (ChangeDetector: nothing in the
// staleness scope past the watermark, no retracted source), e.g. a trigger that raced with a refresh. Such
// a run finishes as NO_NEW_FACTS without a model call and writes nothing, not even last_refreshed_at.
func (r *runner) freshAutomatic(ctx context.Context, p *refreshPlan) (bool, error) {
	if !p.automatic {
		return false, nil
	}
	stale, _, err := NewChangeDetector(r.d.Store.Read(), r.d.Scope).NeedsRefresh(ctx, p.m)
	if err != nil {
		return false, fmt.Errorf("mentalmodel: recheck staleness: %w", err)
	}
	return !stale, nil
}

// intervalFloor is min_refresh_interval_seconds: the trigger's, else the merged config's; 0 = none.
func intervalFloor(m domain.MentalModel, cfg domain.BankConfig) time.Duration {
	secs := 0
	if cfg.MentalModels != nil && cfg.MentalModels.MinRefreshIntervalSeconds != nil {
		secs = *cfg.MentalModels.MinRefreshIntervalSeconds
	}
	if m.Spec.Trigger.MinRefreshIntervalSeconds != nil {
		secs = *m.Spec.Trigger.MinRefreshIntervalSeconds
	}
	return time.Duration(max(secs, 0)) * time.Second
}

// delta runs the synthesizer's delta; past max_delta_ratio it switches p to full.
func (r *runner) delta(ctx context.Context, p *refreshPlan) (drafted, error) {
	d, err := p.synth.Delta(ctx, p.m, p.cfg, p.upTo)
	dr := drafted{
		content: d.ContentMD, ops: d.Ops, usage: d.Usage, toolCalls: d.ToolCalls, retrieved: d.FactsRetrieved,
		sourceIDs: mergeIDs(p.m.SourceIDs, d.SourceIDs), used: len(d.SourceIDs),
	}
	if errors.Is(err, ErrNoNewFacts) {
		dr.content, dr.noNewFacts = p.m.ContentMD, true
		return dr, nil
	}
	if err != nil {
		dr.content = p.m.ContentMD
		return dr, fmt.Errorf("mentalmodel: delta: %w", err)
	}
	if limit := maxDeltaRatio(p.cfg); limit > 0 && changedFraction(p.m.ContentMD, dr.content) > limit {
		p.mode, p.reason = domain.RefreshModeFull, FallbackMaxDeltaRatio
		p.window = port.SeqUpTo(0, p.upTo)
	}
	return dr, nil
}

// full runs the synthesizer's full draft.
func (r *runner) full(ctx context.Context, p *refreshPlan) (drafted, error) {
	d, err := p.synth.Full(ctx, p.m, p.cfg, p.upTo)
	dr := drafted{
		content: d.ContentMD, sourceIDs: d.SourceIDs, structured: d.StructuredOutputJSON, usage: d.Usage,
		toolCalls: d.ToolCalls, retrieved: d.FactsRetrieved, used: len(d.SourceIDs),
	}
	if errors.Is(err, ErrNoNewFacts) {
		dr.content, dr.noNewFacts = p.m.ContentMD, true
		return dr, nil
	}
	if err != nil {
		dr.content = p.m.ContentMD
		return dr, fmt.Errorf("mentalmodel: full: %w", err)
	}
	if strings.TrimSpace(dr.content) == "" {
		dr.content = p.m.ContentMD
		return dr, fmt.Errorf("%w: mentalmodel: empty_candidate: the synthesizer returned an empty document",
			domain.ErrUnavailable)
	}
	return dr, nil
}

// finish projects structured output onto the trigger's schema when the content changed.
func (r *runner) finish(ctx context.Context, p *refreshPlan, dr drafted, err error) (drafted, error) {
	schema := strings.TrimSpace(p.m.Spec.Trigger.ResponseSchemaJSON)
	if err != nil || dr.noNewFacts || schema == "" || dr.content == p.m.ContentMD {
		return dr, err
	}
	if dr.structured != nil {
		return dr, nil
	}
	if r.d.Projector == nil {
		return dr, fmt.Errorf("%w: mentalmodel: structured_output_failed: no projector wired",
			domain.ErrFailedPrecondition)
	}
	out, err := r.d.Projector.Project(ctx, reflect.ProjectRequest{
		Bank: p.m.Bank, Schema: json.RawMessage(schema), Document: dr.content, Overrides: &p.cfg,
	})
	if err != nil {
		return dr, fmt.Errorf("mentalmodel: structured_output_failed: %w", err)
	}
	dr.structured = out
	return dr, nil
}

// embed computes the page embedding for SearchPages; a failure is a warning, not a failed refresh.
func (r *runner) embed(ctx context.Context, p *refreshPlan, dr *drafted, trace *domain.RefreshTrace) {
	if r.d.Embedder == nil {
		return
	}
	vecs, err := r.d.Embedder.EmbedDocuments(ctx, []string{pageText(p.m.Spec.Name, dr.content)})
	if err != nil || len(vecs) != 1 {
		trace.Warnings = append(trace.Warnings, fmt.Sprintf("page embedding failed: %v", err))
		return
	}
	dr.embedding = vecs[0]
}

// trace builds the run's trace.
func (r *runner) trace(p *refreshPlan, dr drafted, start time.Time) domain.RefreshTrace {
	now := r.d.Clock.Now()
	t := domain.RefreshTrace{
		EffectiveMode: p.mode, ModeFallbackReason: p.reason, Outcome: domain.RefreshOutcomeContentWritten,
		ToolCalls: dr.toolCalls, DeltaOperations: dr.ops, Usage: dr.usage, RecordedAt: now,
		DurationMS: now.Sub(start).Milliseconds(), Warnings: dr.warnings,
		EffectiveStrategy: p.synth.Strategy(),
	}
	if dr.noNewFacts {
		t.Outcome = domain.RefreshOutcomeNoNewFacts
	}
	return t
}

// pageText is what SearchPages embeds: the page name and its content.
func pageText(name, content string) string {
	return strings.TrimSpace(name + "\n\n" + content)
}

// mergeIDs returns a followed by the ids of b it lacks.
func mergeIDs(a, b []port.UnitID) []port.UnitID {
	out := slices.Clone(a)
	for _, id := range b {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// loadModel reads one model in a short read-only transaction.
func loadModel(ctx context.Context, s port.Store, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error) {
	var m domain.MentalModel
	err := s.InTx(ctx, func(tx port.Tx) error {
		var err error
		m, err = tx.MentalModels().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return domain.MentalModel{}, fmt.Errorf("mentalmodel: load %s: %w", id, err)
	}
	return m, nil
}
