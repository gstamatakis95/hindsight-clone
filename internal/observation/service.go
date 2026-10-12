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

// ScopePage is one page of observation scopes.
type ScopePage struct {
	// Items are the page's scopes with their observation counts.
	Items []domain.ScopeInfo
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// ListObservationScopes returns one page of the bank's observation scopes (every scope that has held an
// observation) with their counts, ordered by the scope's canonical key. The page size follows domain.ListPage;
// the page token is an opaque keyset cursor and a malformed one is domain.ErrInvalidArgument. An unknown or
// tombstoned bank is domain.ErrNotFound.
func (s *Service) ListObservationScopes(
	ctx context.Context, b domain.BankRef, size int, token string,
) (ScopePage, error) {
	page, err := s.scopePage(ctx, b, size, token)
	if err != nil {
		return ScopePage{}, fmt.Errorf("list scopes: %w", err)
	}
	return page, nil
}

// scopePage reads one page of the bank's observation scopes: the paging of ListObservationScopes, which
// PreviewConsolidationStrategies shares.
func (s *Service) scopePage(ctx context.Context, b domain.BankRef, size int, token string) (ScopePage, error) {
	size, err := domain.ListPage.Size(size)
	if err != nil {
		return ScopePage{}, fmt.Errorf("scope page: %w", err)
	}
	var after string
	if token != "" {
		if err := domain.DecodePageToken(token, &after); err != nil {
			return ScopePage{}, fmt.Errorf("scope page: %w", err)
		}
	}
	items, err := s.d.DB.Read().ListObservationScopes(ctx, b, after, size+1)
	if err != nil {
		return ScopePage{}, fmt.Errorf("scope page: %w", err)
	}
	page := ScopePage{Items: items}
	if len(items) > size {
		page.Items = items[:size]
		if page.NextPageToken, err = domain.EncodePageToken(scopeCursor(b, page.Items[size-1])); err != nil {
			return ScopePage{}, fmt.Errorf("scope page: %w", err)
		}
	}
	return page, nil
}

// scopeCursor is the keyset cursor after scope s: its canonical key, except that the shared scope's key ""
// would restart the listing, so it maps to "0", which sorts before every tagged key.
func scopeCursor(b domain.BankRef, s domain.ScopeInfo) string {
	if key := domain.NewScopeKey(b, s.Scope.Tags).String(); key != "" {
		return key
	}
	return "0"
}

// StrategyPreview mirrors memory.v1.PreviewConsolidationStrategiesResponse.
type StrategyPreview struct {
	// Matches has one entry per draft strategy, in order, over this page's scopes.
	Matches []StrategyMatch
	// NextPageToken continues with the next page of scopes; empty = every scope has been evaluated.
	NextPageToken string
}

// StrategyMatch mirrors memory.v1.StrategyMatch: which existing scopes one strategy would govern.
type StrategyMatch struct {
	// Strategy is the strategy name.
	Strategy string
	// MatchedScopes are the scopes this strategy wins (first match).
	MatchedScopes []domain.ScopeKey
	// ShadowedScopes are scopes its patterns match but an earlier strategy already wins.
	ShadowedScopes []domain.ScopeKey
}

// PreviewConsolidationStrategies evaluates draft strategies against one page of the bank's existing observation
// scopes without saving: it pages the scopes as ListObservationScopes does (size and token follow its rules) and
// reports, per draft strategy, the page's scopes it would win (config.MatchStrategy picks the first match) and
// those an earlier strategy shadows. Each scope's verdict depends only on the drafts, so the pages add up.
func (s *Service) PreviewConsolidationStrategies(
	ctx context.Context, b domain.BankRef, draft []domain.ConsolidationStrategy, size int, token string,
) (StrategyPreview, error) {
	page, err := s.scopePage(ctx, b, size, token)
	if err != nil {
		return StrategyPreview{}, fmt.Errorf("preview strategies: %w", err)
	}
	scopes := make([]domain.ScopeKey, len(page.Items))
	for i, in := range page.Items {
		scopes[i] = domain.NewScopeKey(b, in.Scope.Tags)
	}
	out := StrategyPreview{Matches: make([]StrategyMatch, len(draft)), NextPageToken: page.NextPageToken}
	for i := range draft {
		out.Matches[i].Strategy = draft[i].Name
	}
	for _, sc := range scopes {
		won := false
		for i := range draft {
			if _, ok := config.MatchStrategy(draft[i:i+1], sc); !ok {
				continue
			}
			if won {
				out.Matches[i].ShadowedScopes = append(out.Matches[i].ShadowedScopes, sc)
				continue
			}
			out.Matches[i].MatchedScopes = append(out.Matches[i].MatchedScopes, sc)
			won = true
		}
	}
	return out, nil
}
