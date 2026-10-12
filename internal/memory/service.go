package memory

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/ai"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/recall"
	"memd/internal/reflect"
	"memd/internal/store"
	"memd/internal/timex"
	"time"
)

// Deps are the Service's dependencies. Every field is required except Times, Tokens, Labels and Clock.
type Deps struct {
	// DB opens every transaction; its Read() serves the listings and the curation pre-reads.
	DB store.DB
	// Maintenance holds the cross-bank scans the sweeps use (StrandedFacts, SettleRetainBatches).
	Maintenance store.Maintenance
	// Blob stores (Retain) and reads (retain_document) each document's original text.
	Blob blob.Store
	// LLM makes the EXTRACT calls.
	LLM ai.LLM
	// Embedder embeds facts at retain and rederive.
	Embedder ai.Embedder
	// Router resolves the model binding of every call.
	Router *ai.Router
	// Tokens counts tokens for chunking; nil = an approximation of ceil(runes / 4).
	Tokens *ai.Tokenizer
	// Config resolves the effective config (server < tenant < org < bank < request overrides).
	Config *config.Resolver
	// Times resolves relative time expressions of extracted facts; nil = facts keep only absolute dates.
	Times *timex.Normalizer
	// Linker adds the temporal, semantic and causal links of new and re-derived facts.
	Linker *linking.Linker
	// Entities resolves the entity mentions of extracted facts and the names a curation sets.
	Entities *linking.Resolver
	// Labels resolves label entities; nil = linking.NewLabelResolver().
	Labels *linking.LabelResolver
	// Recall serves Recall.
	Recall *recall.Engine
	// Agent serves Reflect.
	Agent *reflect.Agent
	// Clock stamps events, audit rows and progress, and dates items without a timestamp; nil = the system
	// clock.
	Clock clock.Clock
}

// Service is MemoryService: Retain, Recall and Reflect, the memory listing and curation, the retain_document
// and rederive_memory jobs, and the stranded-fact and retain-batch sweeps of the maintenance tick.
type Service struct {
	d        Deps
	ingest   *ingestor
	retain   *retainer
	curate   *curator
	rederive *rederiver
}

// New returns the Service over d.
func New(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	if d.Labels == nil {
		d.Labels = linking.NewLabelResolver()
	}
	rd := retainerDeps{
		Store: d.DB, Blob: d.Blob, Config: d.Config, Extractor: newExtractor(d.LLM, d.Router), Dates: d.Times,
		Embedder: d.Embedder, Entities: d.Entities, Labels: d.Labels, Linker: d.Linker, Clock: d.Clock,
	}
	if d.Tokens != nil {
		rd.Tokens = d.Tokens
	}
	r := newRetainer(rd)
	return &Service{
		d:        d,
		retain:   r,
		ingest:   newIngestor(ingestorDeps{Store: d.DB, Blob: d.Blob, Config: d.Config, Retainer: r, Clock: d.Clock}),
		curate:   newCurator(curatorDeps{Store: d.DB, Clock: d.Clock, Resolver: d.Entities, Config: d.Config}),
		rederive: newRederiver(rederiverDeps{Store: d.DB, Embedder: d.Embedder, Linker: d.Linker}),
	}
}

// Retain stores each item's text and extracts its facts: inline for a sync retain (the response carries
// the units created and the usage), or as one retain_document job per changed document for run_async. See
// the package doc for the steps and the no-op rule.
func (s *Service) Retain(ctx context.Context, req RetainRequest) (RetainResult, error) {
	return s.ingest.Ingest(ctx, req)
}

// Recall retrieves memories for a query through the recall engine.
func (s *Service) Recall(ctx context.Context, req recall.Request) (recall.Result, error) {
	return s.d.Recall.Recall(ctx, req) //nolint:wrapcheck // the engine returns domain error classes
}

// ListRecallArms lists the recall engine's registered arms, in fusion order.
func (s *Service) ListRecallArms() []recall.ArmInfo { return s.d.Recall.Arms() }

// Reflect answers a query with the reflect agent.
func (s *Service) Reflect(ctx context.Context, req reflect.Request) (reflect.Result, error) {
	return s.d.Agent.Reflect(ctx, req) //nolint:wrapcheck // the agent returns domain error classes
}

// UpdateMemory edits a live fact in one transaction and returns the unit and its rederive operation.
// Observations are rejected with domain.ErrMemoryIsObservation; an archived or unknown unit is
// domain.ErrNotFound.
func (s *Service) UpdateMemory(ctx context.Context, req UpdateRequest) (domain.Unit, *domain.Operation, error) {
	return s.curate.Update(ctx, curation{UpdateRequest: req, act: actionEdit})
}

// InvalidateMemory archives a live fact in one transaction and returns it with state invalidated. No job
// follows, so there is no operation.
func (s *Service) InvalidateMemory(
	ctx context.Context, b domain.BankRef, id domain.UnitID, reason string,
) (domain.Unit, error) {
	u, _, err := s.curate.Update(ctx, curation{
		UpdateRequest: UpdateRequest{Bank: b, ID: id, Reason: reason}, act: actionInvalidate,
	})
	return u, err
}

// RestoreMemory brings an archived fact back and queues its rederive. Restoring a live fact changes nothing
// and returns no operation.
func (s *Service) RestoreMemory(ctx context.Context, req RestoreRequest) (domain.Unit, *domain.Operation, error) {
	return s.curate.Update(ctx, curation{UpdateRequest: UpdateRequest{
		Bank: req.Bank, ID: req.ID, Reason: req.Reason, ResolveEntities: req.ResolveEntities,
		Overrides: req.Overrides,
	}, act: actionRestore})
}

// RunRetainJob runs a retain_document job: it processes the job's document and records the result
// (document_id, memories_created, unchanged, superseded, chunks_total, chunks_skipped) and the EXTRACT usage on
// run. The run enqueues the bank's consolidation in its completing transaction. retain_batch parents are
// never run; SettleRetainBatches ends them.
func (s *Service) RunRetainJob(ctx context.Context, run *jobs.Run) error {
	res, err := s.retain.ProcessDocument(ctx, run)
	if err != nil {
		return fmt.Errorf("retain_document: %w", err)
	}
	run.SetResult(map[string]any{
		"document_id": res.DocumentID, "memories_created": res.MemoriesCreated, "unchanged": res.Unchanged,
		"superseded": res.Superseded, "chunks_total": res.ChunksTotal, "chunks_skipped": res.ChunksSkipped,
	}, res.Usage...)
	return nil
}

// RunRederiveJob runs a rederive_memory job. It first reports the run's progress (stage rederive_memory, 0 of
// 1): that is the job's one progress write and cancel check, so a cancelled job, or one whose bank is being
// deleted, stops there (domain.ErrCancelled) before the embedding call. There is no report afterwards: a late
// cancel would turn committed work into a failure.
func (s *Service) RunRederiveJob(ctx context.Context, run *jobs.Run) error {
	p := domain.Progress{Stage: string(run.Job.Kind), RecordedAt: s.d.Clock.Now(), Total: 1}
	if err := run.Report(ctx, p); err != nil {
		return fmt.Errorf("%s: checkpoint: %w", run.Job.Kind, err)
	}
	if err := s.rederive.Rederive(ctx, run.Job); err != nil {
		return fmt.Errorf("rederive_memory: %w", err)
	}
	return nil
}

// Settings of the stranded-fact sweep.
const (
	// strandedBatch caps the rederives one sweep enqueues.
	strandedBatch = 100
	// strandedRetryAfter is how long after a rederive ended the sweep may queue another one for the same
	// unit, so a fact whose rederive keeps failing is retried every few minutes, not every tick.
	strandedRetryAfter = 10 * time.Minute
)

// RederiveStranded re-enqueues rederive_memory for stranded facts (a NULL embedding left by a rederive that
// ended FAILED or CANCELLED at least 10 minutes ago), one short transaction per fact, with the payload and
// serial key curation uses (jobs.EnqueueRederive, no overrides). The scan skips units whose rederive is queued
// or running, so ticks don't pile jobs up; two workers ticking at once may queue a unit twice, which is
// harmless (rederive is idempotent). A bank being deleted or gone is skipped. It takes no tx.Seq: a job row is
// not memory content. It returns how many it queued.
func (s *Service) RederiveStranded(ctx context.Context) (int, error) {
	units, err := s.d.Maintenance.StrandedFacts(ctx, strandedRetryAfter, strandedBatch)
	if err != nil {
		return 0, fmt.Errorf("list stranded facts: %w", err)
	}
	n := 0
	var errs []error
	for _, u := range units {
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			_, _, err := jobs.EnqueueRederive(ctx, tx, u, nil)
			return err
		})
		switch {
		case errors.Is(err, domain.ErrBankDeleting), errors.Is(err, domain.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("unit %s: %w", u.ID, err))
		default:
			n++
		}
	}
	return n, errors.Join(errs...)
}

// SettleRetainBatches ends retain_batch parents whose children are all terminal (a parent is never run; its
// status aggregates its children) and returns how many it settled.
func (s *Service) SettleRetainBatches(ctx context.Context) (int, error) {
	n, err := s.d.Maintenance.SettleRetainBatches(ctx)
	if err != nil {
		return n, fmt.Errorf("settle retain batches: %w", err)
	}
	return n, nil
}
