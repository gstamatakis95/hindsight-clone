package recall

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"memd/internal/engine/timex"
	"memd/internal/store"
	"strings"
	"time"
)

// parsedQuery is a recall request after parsing: what every Retriever and the Scorer read.
type parsedQuery struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Text is the query text, cut to MaxQueryTokens.
	Text string
	// Embedding is the query vector from EmbedQuery, 768 dims.
	Embedding []float32
	// Window is the explicit or timex-resolved time window; nil = none (the temporal arm is skipped).
	Window *domain.TimeWindow
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags is the request's tag filter.
	Tags domain.TagFilter
	// Predicate is Tags compiled once (fuzzy leaves resolved) and shared by all four arms.
	Predicate *store.SQLPredicate
	// Limit is the per-arm candidate limit from the budget: 100 / 300 / 1000.
	Limit int
	// At is the reference time for recency and temporal boosts.
	At time.Time
	// MinScores are the request's arm and result floors.
	MinScores domain.MinScores
	// Seq bounds the units' seq; zero = unbounded.
	Seq store.SeqRange
	// Config is the effective (merged) config of the call: recall knobs and the RERANK binding.
	Config domain.BankConfig
}

// parseDeps are what parse calls.
type parseDeps struct {
	embedder ai.Embedder
	times    timex.TimeNormalizer
	tags     TagFilterCompiler
	clock    clock.Clock
	count    TokenCounter
}

// parse validates req and builds the parsedQuery under the effective config cfg: query cut to
// MaxQueryTokens, reference time, fact types, time window (explicit, else timex, which calls TIME_PARSE only
// for a cue its rules can't resolve), compiled tag filter, per-arm limit and query embedding.
func parse(ctx context.Context, req RecallRequest, cfg domain.BankConfig, d parseDeps) (*parsedQuery, error) {
	text := strings.TrimSpace(req.Query)
	if text == "" {
		return nil, fmt.Errorf("%w: recall: query is empty", domain.ErrInvalidArgument)
	}
	text = truncateTokens(text, MaxQueryTokens, d.count)
	q := &parsedQuery{
		Bank: req.Bank, Text: text, Types: factTypes(req.Types), Tags: req.Tags,
		MinScores: req.MinScores, Seq: req.Seq, Config: cfg, At: d.clock.Now(),
	}
	if req.QueryTime != nil {
		q.At = *req.QueryTime
	}
	rc := domain.RecallConfig{}
	if cfg.Recall != nil {
		rc = *cfg.Recall
	}
	q.Limit = budgetLimit(rc, req.Budget, req.MaxTokens)
	pred, err := d.tags.Compile(ctx, req.Bank, req.Tags, settingsOf(cfg).fuzzyMinSimilarity)
	if err != nil {
		return nil, fmt.Errorf("recall: tags: %w", err)
	}
	q.Predicate = &pred
	if q.Window, err = window(ctx, req, cfg, text, q.At, d.times); err != nil {
		return nil, err
	}
	if q.Embedding, err = d.embedder.EmbedQuery(ctx, text); err != nil {
		return nil, fmt.Errorf("recall: embed query: %w", err)
	}
	return q, nil
}

// window returns the explicit window when the request has one (no time analysis at all), else the window
// timex derives from the query; nil = none. timex degrades every fallback failure to no window itself.
func window(ctx context.Context, req RecallRequest, cfg domain.BankConfig, text string, at time.Time,
	times timex.TimeNormalizer,
) (*domain.TimeWindow, error) {
	if req.Window != nil {
		if req.Window.Start.IsZero() && req.Window.End.IsZero() {
			return nil, nil
		}
		w := *req.Window
		return &w, nil
	}
	if times == nil {
		return nil, nil
	}
	w, err := times.QueryWindow(ctx, cfg, text, at)
	if err != nil {
		return nil, fmt.Errorf("recall: time window: %w", err)
	}
	return w, nil
}

// factTypes drops unspecified entries; an empty result means all types.
func factTypes(ts []domain.FactType) []domain.FactType {
	var out []domain.FactType
	for _, t := range ts {
		if t != domain.FactTypeUnspecified {
			out = append(out, t)
		}
	}
	return out
}
