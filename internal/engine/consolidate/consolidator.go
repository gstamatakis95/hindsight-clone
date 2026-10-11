package consolidate

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	"memd/internal/store"

	"github.com/google/uuid"
)

// Run defaults for knobs left unset in the effective config.
const (
	defaultBatchSize        = 8
	defaultMaxUnitsPerRun   = 100
	defaultSourceTokensPerO = 256
)

// Consolidator is the consolidate job handler.
type Consolidator interface {
	// Run takes up to consolidation_max_units_per_run of the bank's oldest unconsolidated units (only
	// the job's scopes, for a manual run). Each unit is decided in every scope it belongs to, then stamped
	// in the same tx as its last scope's writes; a scope that already cites the unit is skipped, so a
	// replayed run is safe. A failing LLM batch is halved and retried; a unit that fails alone is parked
	// with consolidation_failed_at. When units remain, Run enqueues the continuation itself (in its final
	// transaction) and reports More=true.
	Run(ctx context.Context, j store.Job) (ConsolidationResult, error)
}

// ConsolidationResult summarizes one consolidation run.
type ConsolidationResult struct {
	// Processed counts the units decided (stamped or parked) in this run.
	Processed int
	// Changed are the scopes whose observations changed; drives mental-model refresh triggers.
	Changed []domain.ScopeKey
	// More reports that unconsolidated units remain; Run has already enqueued the continuation.
	More bool
	// Usage is the run's LLM usage per slot and model, for Claimer.Complete.
	Usage []domain.Usage
}

// ConsolidatorDeps are the Consolidator's dependencies. Store, Config, Router, LLM, Embedder and Clock
// are required; the rest are optional as documented.
type ConsolidatorDeps struct {
	// Store reads units and applies decisions in short transactions.
	Store store.DB
	// Config resolves the effective config per run (Effective) and per scope (ForScope, strategy < request).
	Config config.Resolver
	// Router resolves the CONSOLIDATE and DEDUP_CHECK bindings.
	Router *ai.Router
	// LLM makes the CONSOLIDATE calls (and DEDUP_CHECK through the default Deduper).
	LLM ai.LLM
	// Embedder embeds new and rewritten observation text.
	Embedder ai.Embedder
	// Clock stamps consolidated_at, versions and events.
	Clock clock.Clock
	// Scopes expands units into scopes; nil = NewScopeExpander().
	Scopes ScopeExpander
	// Deduper checks new observations for near-duplicates; nil = NewDeduper(LLM, Router, Store.Read()).
	Deduper Deduper
	// Tokens counts tokens for the source-fact budgets; nil = about four characters per token.
	Tokens recall.TokenCounter
	// Checkpoint returns the run's checkpoint (progress + cancel check) between scopes and batches;
	// nil = none.
	Checkpoint func(j store.Job) Checkpointer
	// NewID makes observation ids; nil = random UUIDs.
	NewID func() domain.UnitID
}

// consolidator implements Consolidator.
type consolidator struct {
	d ConsolidatorDeps
}

var _ Consolidator = (*consolidator)(nil)

// NewConsolidator returns the Consolidator, filling optional dependencies with their defaults.
func NewConsolidator(d ConsolidatorDeps) Consolidator {
	if d.Scopes == nil {
		d.Scopes = NewScopeExpander()
	}
	if d.Deduper == nil {
		d.Deduper = NewDeduper(d.LLM, d.Router, d.Store.Read())
	}
	if d.Tokens == nil {
		d.Tokens = approxTokens{}
	}
	if d.NewID == nil {
		d.NewID = func() domain.UnitID { return domain.UnitID(uuid.NewString()) }
	}
	return &consolidator{d: d}
}

// approxTokens counts about four characters per token.
type approxTokens struct{}

// Count returns ceil(runes / 4).
func (approxTokens) Count(text string) int { return (len([]rune(text)) + 3) / 4 }

// Run plans the run in a read transaction, decides and applies scope by scope, then finishes in one
// transaction (leftover stamps, continuation, refresh triggers, CONSOLIDATION_COMPLETED event).
func (c *consolidator) Run(ctx context.Context, j store.Job) (ConsolidationResult, error) {
	p, err := decodePayload(j.Payload)
	if err != nil {
		return ConsolidationResult{}, err
	}
	var overrides []domain.BankConfig
	if j.Overrides != nil {
		overrides = append(overrides, *j.Overrides)
	}
	cfg, err := c.d.Config.Effective(ctx, j.Bank, overrides...)
	if err != nil {
		return ConsolidationResult{}, fmt.Errorf("consolidate: config: %w", err)
	}
	if cfg.EnableObservations != nil && !*cfg.EnableObservations {
		return ConsolidationResult{}, nil
	}
	r := &run{c: c, job: j, cfg: cfg, payload: p, filter: payloadScopes(j.Bank, p)}
	if c.d.Checkpoint != nil {
		r.check = c.d.Checkpoint(j)
	}
	if r.models, err = r.refreshCandidates(ctx); err != nil {
		return ConsolidationResult{}, err
	}
	if err := r.plan(ctx); err != nil {
		return ConsolidationResult{}, err
	}
	for i := range r.scopes {
		if err := r.scope(ctx, i); err != nil {
			return r.result(), err
		}
	}
	if err := r.finish(ctx); err != nil {
		return r.result(), err
	}
	return r.result(), nil
}

// errStops reports whether err means the run must stop now (cancelled, lost job, ctx done) rather than
// being a batch failure.
func errStops(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, domain.ErrCancelled) || errors.Is(err, context.Canceled) ||
		store.LostJobs(err) != nil
}

// orDefault returns *p, or def when p is nil or not positive.
func orDefault(p *int, def int) int {
	if p == nil || *p <= 0 {
		return def
	}
	return *p
}
