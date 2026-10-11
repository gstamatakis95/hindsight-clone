package mentalmodel

import (
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/recall"
	"memd/internal/reflect"
	"memd/internal/store"
)

// Deps are the Service's dependencies.
type Deps struct {
	// DB reads and writes models, reads scopes, sources and directives, and queues refreshes.
	DB store.DB
	// Maintenance lists the cron models and the failed refreshes for the maintenance sweeps.
	Maintenance store.Maintenance
	// Config merges server < tenant < bank < model overrides < call overrides.
	Config *config.Resolver
	// Recall runs the single-shot recall, resolves fuzzy scope tags and serves SearchMentalModels.
	Recall *recall.Engine
	// Reflect runs the agentic refresh loop and projects pages onto a spec's response schema; nil = the
	// agentic strategy and structured output fail.
	Reflect *reflect.Agent
	// LLM makes the synthesize and delta calls.
	LLM ai.LLM
	// Router resolves each slot's binding from the merged config.
	Router *ai.Router
	// Embedder embeds pages for SearchMentalModels; nil = pages are stored without an embedding.
	Embedder ai.Embedder
	// Clock stamps last_refreshed_at, history, traces, enqueues and the cron sweep.
	Clock clock.Clock
}

// Service serves memory.v1.MentalModelService, runs the refresh_mental_model job and the mental-model
// maintenance sweeps (cron refreshes, failed-refresh reconcile).
type Service struct {
	d      Deps
	synths map[domain.RefreshStrategy]synthesizer
}

// New returns the Service over d with both refresh strategies.
func New(d Deps) *Service {
	md := modelDeps{LLM: d.LLM, Router: d.Router, Clock: d.Clock, DB: d.DB}
	s := &Service{d: d, synths: map[domain.RefreshStrategy]synthesizer{}}
	for _, syn := range []synthesizer{newAgentic(d.Reflect, md), newSingleShot(d.Recall, md)} {
		s.synths[syn.Strategy()] = syn
	}
	return s
}
