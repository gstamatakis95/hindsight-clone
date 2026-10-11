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

// Service serves ObservationService (manual consolidation, scope listing, an observation's history, clears,
// the strategy preview), runs the consolidate job and the consolidation reconcile sweep.
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

// HistoryPage is one page of an observation's versions, newest first.
type HistoryPage struct {
	// Items are the page's versions.
	Items []domain.ObservationVersion
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListObservationHistory returns one page of observation id's versions, newest first. The page size follows
// domain.ListPage; the page token is an opaque cursor (the last version returned) and a malformed one is
// domain.ErrInvalidArgument. An unknown id, bank or a tombstoned bank is domain.ErrNotFound.
func (s *Service) ListObservationHistory(
	ctx context.Context, b domain.BankRef, id domain.UnitID, size int, token string,
) (HistoryPage, error) {
	size, err := domain.ListPage.Size(size)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("observation history %s: %w", id, err)
	}
	var before int64
	if token != "" {
		if err := domain.DecodePageToken(token, &before); err != nil {
			return HistoryPage{}, fmt.Errorf("observation history %s: %w", id, err)
		}
		if before <= 0 {
			return HistoryPage{}, domain.InvalidField("page_token", "malformed page token")
		}
	}
	vs, err := s.d.DB.Read().ObservationHistory(ctx, b, id, before, size+1)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("observation history %s: %w", id, err)
	}
	page := HistoryPage{Items: vs}
	if len(vs) > size {
		page.Items = vs[:size]
		if page.NextPageToken, err = domain.EncodePageToken(page.Items[size-1].Version); err != nil {
			return HistoryPage{}, fmt.Errorf("observation history %s: %w", id, err)
		}
	}
	return page, nil
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
