package observation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"

	"github.com/google/uuid"
)

// Run defaults for knobs left unset in the effective config.
const (
	defaultBatchSize        = 8
	defaultMaxUnitsPerRun   = 100
	defaultSourceTokensPerO = 256
)

// consolidationResult summarizes one consolidation run.
type consolidationResult struct {
	// Processed counts the units decided (stamped or parked) in this run.
	Processed int
	// Changed are the scopes whose observations changed.
	Changed []domain.ScopeKey
	// More reports that unconsolidated units remain; Run has already enqueued the continuation.
	More bool
	// Usage is the run's LLM usage per slot and model, recorded on the operation.
	Usage []domain.Usage
}

// consolidator runs the consolidate job over the Service's dependencies.
type consolidator struct {
	d       Deps
	deduper *deduper
	tokens  tokenCounter
	newID   func() domain.UnitID // observation ids; tests replace it for readable ids
}

// tokenCounter counts tokens: the wired ai.Tokenizer, else approxTokens.
type tokenCounter interface {
	// Count returns text's token count.
	Count(text string) int
}

// newConsolidator returns the consolidator over d: dedup probes the store, tokens default to approxTokens
// and observation ids are random UUIDs.
func newConsolidator(d Deps) *consolidator {
	var tokens tokenCounter = approxTokens{}
	if d.Tokens != nil {
		tokens = d.Tokens
	}
	return &consolidator{
		d: d, deduper: newDeduper(d.LLM, d.Router, d.DB.Read()), tokens: tokens,
		newID: func() domain.UnitID { return domain.UnitID(uuid.NewString()) },
	}
}

// approxTokens counts about four characters per token.
type approxTokens struct{}

// Count returns ceil(runes / 4).
func (approxTokens) Count(text string) int { return (len([]rune(text)) + 3) / 4 }

// Run takes up to observations.max_units_per_run of the bank's oldest unconsolidated units (only the job's
// scopes, for a manual run). It plans the run in a read transaction, decides and applies scope by scope, then
// finishes in one transaction (leftover stamps, continuation, CONSOLIDATION_COMPLETED event). It reports
// progress through job between scopes and batches and stops (domain.ErrCancelled) once the job is cancelled
// or its bank deleted.
func (c *consolidator) Run(ctx context.Context, job *jobs.Run) (consolidationResult, error) {
	j := job.Job
	p, err := decodePayload(j.Payload)
	if err != nil {
		return consolidationResult{}, err
	}
	var overrides []domain.BankConfig
	if j.Overrides != nil {
		overrides = append(overrides, *j.Overrides)
	}
	cfg, err := c.d.Config.Effective(ctx, j.Bank, overrides...)
	if err != nil {
		return consolidationResult{}, fmt.Errorf("consolidate: config: %w", err)
	}
	if cfg.GetObservations().Enabled != nil && !*cfg.GetObservations().Enabled {
		return consolidationResult{}, nil
	}
	r := &run{c: c, job: j, check: job, cfg: cfg, payload: p, filter: payloadScopes(j.Bank, p)}
	if r.models, err = r.refreshCandidates(ctx); err != nil {
		return consolidationResult{}, err
	}
	if err := r.plan(ctx); err != nil {
		return consolidationResult{}, err
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

// decodePayload reads a consolidate payload; an empty or null payload is the automatic job.
func decodePayload(raw json.RawMessage) (jobs.ConsolidatePayload, error) {
	var p jobs.ConsolidatePayload
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return p, nil
	}
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return jobs.ConsolidatePayload{}, fmt.Errorf("%w: consolidate payload: %w", domain.ErrInvalidArgument, err)
	}
	return p, nil
}

// payloadScopes turns the payload's tag sets into scope keys of b.
func payloadScopes(b domain.BankRef, p jobs.ConsolidatePayload) []domain.ScopeKey {
	out := make([]domain.ScopeKey, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		out = append(out, domain.NewScopeKey(b, s.Tags))
	}
	return out
}
