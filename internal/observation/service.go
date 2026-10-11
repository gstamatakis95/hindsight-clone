package observation

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
)

// Deps are the Service's dependencies; every field is required.
type Deps struct {
	// DB reads units and scopes and applies every write in short transactions.
	DB store.DB
	// Maintenance lists the banks with facts waiting for consolidation (ReconcileConsolidation).
	Maintenance store.Maintenance
	// Config resolves the effective config per run (Effective) and per scope (ForScope: strategy < request).
	Config *config.Resolver
	// Router resolves the CONSOLIDATE and DEDUP_CHECK bindings.
	Router *ai.Router
	// LLM makes the CONSOLIDATE and DEDUP_CHECK calls.
	LLM ai.LLM
	// Embedder embeds new and rewritten observation text.
	Embedder ai.Embedder
	// Tokens counts tokens for the source-fact budgets; nil = about four characters per token.
	Tokens *ai.Tokenizer
	// Clock stamps consolidated_at, versions, events and the reconcile cut-off.
	Clock clock.Clock
}

// Service serves ObservationService (manual consolidation, scope listing, clears, the strategy preview) and
// an observation's history, runs the consolidate job and the consolidation reconcile sweep.
type Service struct {
	d    Deps
	cons *consolidator
}

// New returns the Service over d.
func New(d Deps) *Service {
	return &Service{d: d, cons: newConsolidator(d)}
}

// RunConsolidateJob runs one consolidate job and records its result (processed, changed_scopes, more) and
// model usage on the run. It never re-enqueues on more: the run already enqueued its continuation in its final
// transaction.
func (s *Service) RunConsolidateJob(ctx context.Context, run *jobs.Run) error {
	res, err := s.cons.Run(ctx, run)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}
	run.SetResult(map[string]any{
		"processed": res.Processed, "changed_scopes": len(res.Changed), "more": res.More,
	}, res.Usage...)
	return nil
}

// Consolidate queues a manual consolidation over scopes (every scope when empty) with request overrides and
// returns its operation. It is a write: an unknown bank is created, a tombstoned one is domain.ErrBankDeleting.
func (s *Service) Consolidate(
	ctx context.Context, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (domain.Operation, error) {
	scopes = normalizeScopes(scopes)
	var op domain.Operation
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		op = domain.Operation{}
		if _, err := tx.Banks().Ensure(ctx, b); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		var err error
		op, err = enqueueConsolidation(ctx, tx, b, scopes, overrides)
		return err
	})
	if err != nil {
		return domain.Operation{}, fmt.Errorf("consolidate: %w", err)
	}
	return op, nil
}

// ListObservationHistory returns the versions of observation id in the store's order; an unknown id, bank
// or a tombstoned bank is domain.ErrNotFound.
func (s *Service) ListObservationHistory(
	ctx context.Context, b domain.BankRef, id domain.UnitID,
) ([]domain.ObservationVersion, error) {
	vs, err := s.d.DB.Read().ObservationHistory(ctx, b, id)
	if err != nil {
		return nil, fmt.Errorf("observation history %s: %w", id, err)
	}
	return vs, nil
}

// normalizeScopes returns scopes with normalized tags.
func normalizeScopes(scopes []domain.TagSet) []domain.TagSet {
	out := make([]domain.TagSet, len(scopes))
	for i, s := range scopes {
		out[i] = domain.TagSet{Tags: domain.NormalizeTags(s.Tags)}
	}
	return out
}

// enqueueConsolidation enqueues a consolidate in tx (jobs.EnqueueScopedConsolidate: {} with DedupePending when
// automatic, the scopes payload with DedupeNone when it carries scopes or overrides) and returns its
// operation row.
func enqueueConsolidation(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (domain.Operation, error) {
	id, _, err := jobs.EnqueueScopedConsolidate(ctx, tx, b, scopes, overrides)
	if err != nil {
		return domain.Operation{}, err
	}
	return tx.Queue().Get(ctx, b, id)
}
