package observation

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"strings"
)

// run is the state of one Consolidator.Run.
type run struct {
	c       *consolidator
	job     store.Job
	cfg     domain.BankConfig // effective run config (server < tenant < bank < request)
	payload jobs.ConsolidatePayload
	filter  []domain.ScopeKey    // a manual run's scopes; empty = every unit
	check   *jobs.Run            // progress and the cancel check
	models  []domain.MentalModel // refresh_after_consolidation models, listed once per run

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
	limit := orDefault(r.cfg.GetObservations().MaxUnitsPerRun, defaultMaxUnitsPerRun)
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
	modes := map[domain.DocumentID]*domain.ObservationScopes{}
	pending, cited = map[domain.UnitID][]domain.ScopeKey{}, map[domain.UnitID][]domain.ScopeKey{}
	for _, u := range units {
		mode, err := r.unitMode(ctx, tx, u, modes)
		if err != nil {
			return nil, nil, err
		}
		keys, err := domain.ExpandScopes(r.job.Bank, u.Tags, mode)
		if err != nil {
			// The mode changed since retain (which rejects this synchronously): fall back to the combined
			// scope rather than strand the unit (decision 1003).
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

// unitMode returns the unit's scope fan-out (decision 1003): its document's item-level
// observation_scopes, else the document's retain overrides' default_observation_scopes, else the run
// config's default, else combined. modes caches documents.
func (r *run) unitMode(
	ctx context.Context, tx store.Tx, u domain.Unit, modes map[domain.DocumentID]*domain.ObservationScopes,
) (domain.ObservationScopes, error) {
	if u.DocumentID != "" {
		m, seen := modes[u.DocumentID]
		if !seen {
			var err error
			if m, err = r.docMode(ctx, tx, u.DocumentID); err != nil {
				return domain.ObservationScopes{}, err
			}
			modes[u.DocumentID] = m
		}
		if m != nil {
			return *m, nil
		}
	}
	if r.cfg.GetRetain().DefaultObservationScopes != nil {
		return *r.cfg.GetRetain().DefaultObservationScopes, nil
	}
	return domain.ObservationScopes{Mode: domain.ScopeModeCombined}, nil
}

// docMode reads a document's own scope fan-out: the item's observation_scopes (Document.ObservationScopes),
// else its overrides' default; nil when neither is set or the document is gone.
func (r *run) docMode(ctx context.Context, tx store.Tx, doc domain.DocumentID) (*domain.ObservationScopes, error) {
	d, err := tx.Documents().Get(ctx, r.job.Bank, doc)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("document %s: %w", doc, err)
	case d.ObservationScopes != nil:
		return d.ObservationScopes, nil
	case d.Overrides != nil:
		return d.Overrides.GetRetain().DefaultObservationScopes, nil
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
	size := orDefault(cfg.GetObservations().LLMBatchSize, defaultBatchSize)
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
		Stage: stage, At: r.c.d.Clock.Now(), Processed: int64(r.processed), Total: int64(len(r.units)),
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
