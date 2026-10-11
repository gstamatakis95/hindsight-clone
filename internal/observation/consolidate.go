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
	"slices"
	"strings"

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

// run is the state of one Consolidator.Run.
type run struct {
	c       *consolidator
	job     store.Job
	cfg     domain.BankConfig // effective run config (server < tenant < bank < request)
	payload jobs.ConsolidatePayload
	filter  []domain.ScopeKey    // a manual run's scopes; empty = every unit
	check   *jobs.Run            // progress and the cancel check
	models  []domain.MentalModel // schedule.after_consolidation models, listed once per run

	units  map[domain.UnitID]domain.Unit       // planned units (their seq and tags are re-checked before stamping)
	cited  map[domain.UnitID][]domain.ScopeKey // scopes that cite the unit: skipped at plan, or written this run
	scopes []scopeWork
	last   map[domain.UnitID]int // index into scopes of the unit's last pending scope
	done   []domain.UnitID       // planned units with no pending scope: stamped in finish

	parked    map[domain.UnitID]bool
	unsettled map[domain.UnitID]bool // decision did not fully land (changed, skipped action): never stamped this run
	deferred  []domain.UnitID        // failed alone in the current top-level batch; settled after it
	treeOK    bool                   // a call of the current top-level batch succeeded
	deadTrees int                    // consecutive top-level batches with every call failed, since a success
	lastErr   error                  // the latest batch failure
	processed int
	changed   map[string]domain.ScopeKey
	usage     []domain.Usage
	more      bool
}

// scopeWork is one scope of the run and the planned units still undecided in it, oldest first.
type scopeWork struct {
	key   domain.ScopeKey
	units []domain.UnitID
}

// plan reads the run's units in one read-only transaction: up to consolidation_max_units_per_run oldest
// unconsolidated units (filtered by the job's scopes), each unit's scopes, and which of those already
// cite it (a replayed run skips them).
func (r *run) plan(ctx context.Context) error {
	limit := domain.PositiveOr(r.cfg.GetObservations().MaxUnitsPerRun, defaultMaxUnitsPerRun)
	var units []domain.Unit
	var pending, cited map[domain.UnitID][]domain.ScopeKey
	err := r.c.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		if units, err = tx.Units().Unconsolidated(ctx, r.job.Bank, r.filter, limit); err != nil {
			return fmt.Errorf("unconsolidated units: %w", err)
		}
		pending, cited, err = r.pendingScopes(ctx, tx, units)
		return err
	})
	if err != nil {
		return fmt.Errorf("consolidate: plan: %w", err)
	}
	r.units, r.cited, r.unsettled = map[domain.UnitID]domain.Unit{}, cited, map[domain.UnitID]bool{}
	r.last, r.parked, r.changed = map[domain.UnitID]int{}, map[domain.UnitID]bool{}, map[string]domain.ScopeKey{}
	byKey := map[string]*scopeWork{}
	for _, u := range units {
		r.units[u.ID] = u
		if len(pending[u.ID]) == 0 {
			r.done = append(r.done, u.ID)
		}
		for _, k := range pending[u.ID] {
			w, ok := byKey[k.String()]
			if !ok {
				w = &scopeWork{key: k}
				byKey[k.String()] = w
			}
			w.units = append(w.units, u.ID)
		}
	}
	for _, w := range byKey {
		r.scopes = append(r.scopes, *w)
	}
	slices.SortFunc(r.scopes, func(x, y scopeWork) int { return strings.Compare(x.key.String(), y.key.String()) })
	for i, w := range r.scopes {
		for _, id := range w.units {
			r.last[id] = i
		}
	}
	return nil
}

// pendingScopes expands each unit into its scopes and splits them into those still to decide and those
// that already cite it.
func (r *run) pendingScopes(
	ctx context.Context, tx store.Tx, units []domain.Unit,
) (pending, cited map[domain.UnitID][]domain.ScopeKey, err error) {
	modes := map[domain.DocumentID]*domain.ObservationScopeModes{}
	pending, cited = map[domain.UnitID][]domain.ScopeKey{}, map[domain.UnitID][]domain.ScopeKey{}
	for _, u := range units {
		mode, err := r.unitMode(ctx, tx, u, modes)
		if err != nil {
			return nil, nil, err
		}
		keys, err := domain.ExpandScopes(r.job.Bank, u.Tags, mode)
		if err != nil {
			// The mode changed since retain (which rejects this synchronously): fall back to the combined
			// scope rather than strand the unit.
			keys = []domain.ScopeKey{domain.NewScopeKey(r.job.Bank, u.Tags)}
		}
		for _, k := range keys {
			ok, err := tx.Evidence().Cites(ctx, k, u.ID)
			if err != nil {
				return nil, nil, fmt.Errorf("cites %s in scope %q: %w", u.ID, k.String(), err)
			}
			if ok {
				cited[u.ID] = append(cited[u.ID], k)
			} else {
				pending[u.ID] = append(pending[u.ID], k)
			}
		}
	}
	return pending, cited, nil
}

// unitMode returns the unit's scope fan-out (docs/design/consolidation.md#scopes): its document's item-level
// observation_scopes, else the document's retain overrides' default_observation_scope_modes, else the run
// config's default, else combined. modes caches documents.
func (r *run) unitMode(
	ctx context.Context, tx store.Tx, u domain.Unit, modes map[domain.DocumentID]*domain.ObservationScopeModes,
) (domain.ObservationScopeModes, error) {
	if u.DocumentID != "" {
		m, seen := modes[u.DocumentID]
		if !seen {
			var err error
			if m, err = r.docMode(ctx, tx, u.DocumentID); err != nil {
				return domain.ObservationScopeModes{}, err
			}
			modes[u.DocumentID] = m
		}
		if m != nil {
			return *m, nil
		}
	}
	if r.cfg.GetRetain().DefaultObservationScopeModes != nil {
		return *r.cfg.GetRetain().DefaultObservationScopeModes, nil
	}
	return domain.ObservationScopeModes{Mode: domain.ScopeModeCombined}, nil
}

// docMode reads a document's own scope fan-out: the item's observation_scopes (Document.ObservationScopeModes),
// else its overrides' default; nil when neither is set or the document is gone.
func (r *run) docMode(ctx context.Context, tx store.Tx, doc domain.DocumentID) (*domain.ObservationScopeModes, error) {
	d, err := tx.Documents().Get(ctx, r.job.Bank, doc)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("document %s: %w", doc, err)
	case d.ObservationScopeModes != nil:
		return d.ObservationScopeModes, nil
	case d.Overrides != nil:
		return d.Overrides.GetRetain().DefaultObservationScopeModes, nil
	}
	return nil, nil
}

// scope decides every planned unit of scope i in LLM batches of consolidation_llm_batch_size, with the
// scope's config (strategy < request). After each top-level batch that left units deferred, settle tells
// an outage (fail the run) from poison (park it) before going on.
func (r *run) scope(ctx context.Context, i int) error {
	w := r.scopes[i]
	cfg, err := r.c.d.Config.ForScope(ctx, w.key, r.job.Overrides)
	if err != nil {
		return fmt.Errorf("consolidate: config for scope %q: %w", w.key.String(), err)
	}
	size := domain.PositiveOr(cfg.GetObservations().LLMBatchSize, defaultBatchSize)
	for start := 0; start < len(w.units); start += size {
		if err := r.checkpoint(ctx, "scope "+w.key.String()); err != nil {
			return err
		}
		ids := r.open(w.units[start:min(start+size, len(w.units))])
		if len(ids) == 0 {
			continue
		}
		r.treeOK = false
		if err := r.batch(ctx, i, cfg, ids); err != nil {
			return err
		}
		if !r.treeOK {
			r.deadTrees++
		}
		if err := r.settle(ctx, i, cfg); err != nil {
			return err
		}
	}
	return nil
}

// open returns ids without the parked, deferred and unsettled ones.
func (r *run) open(ids []domain.UnitID) []domain.UnitID {
	out := make([]domain.UnitID, 0, len(ids))
	for _, id := range ids {
		if !r.parked[id] && !r.unsettled[id] && !slices.Contains(r.deferred, id) {
			out = append(out, id)
		}
	}
	return out
}

// checkpoint reports progress and stops the run when the job was cancelled or its bank deleted.
func (r *run) checkpoint(ctx context.Context, stage string) error {
	err := r.check.Report(ctx, domain.Progress{
		Stage: stage, RecordedAt: r.c.d.Clock.Now(), Processed: int64(r.processed), Total: int64(len(r.units)),
	})
	if err != nil {
		return fmt.Errorf("consolidate: checkpoint: %w", err)
	}
	return nil
}

// result summarizes the run so far.
func (r *run) result() consolidationResult {
	out := consolidationResult{Processed: r.processed, More: r.more, Usage: r.usage}
	for _, k := range r.changed {
		out.Changed = append(out.Changed, k)
	}
	slices.SortFunc(out.Changed, func(x, y domain.ScopeKey) int { return strings.Compare(x.String(), y.String()) })
	return out
}

// addUsage folds usage into the run's per-(slot, model) totals.
func (r *run) addUsage(us ...domain.Usage) {
	for _, u := range us {
		if u == (domain.Usage{}) {
			continue
		}
		i := slices.IndexFunc(r.usage, func(x domain.Usage) bool { return x.Slot == u.Slot && x.Model == u.Model })
		if i < 0 {
			r.usage = append(r.usage, u)
			continue
		}
		r.usage[i].InputTokens += u.InputTokens
		r.usage[i].OutputTokens += u.OutputTokens
	}
}
