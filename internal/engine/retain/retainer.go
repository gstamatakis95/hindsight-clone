package retain

import (
	"context"
	"fmt"
	"io"
	"memd/internal/domain"
	"memd/internal/port"
	"time"

	"github.com/google/uuid"
)

// Retainer processes one document; it is also the retain_document job handler.
type Retainer interface {
	// ProcessDocument runs chunk -> extract -> normalize -> embed -> resolve -> link -> commit, one tx per
	// chunk batch. Unchanged content + overrides hash with extracted_at set = no-op (unless
	// force_reextract). A changed document replaces its old units in the first batch's tx; a retried job
	// skips chunks already committed with a matching hash. Runs inline for sync Retain, else as a job.
	// j.Payload is a DocumentPayload and j.Overrides the merged request < item overrides. A job with an id
	// resumes across retries; an inline run (empty j.ID) starts fresh.
	ProcessDocument(ctx context.Context, j port.Job) (RetainResult, error)
}

// RetainResult summarizes one processed document.
type RetainResult struct { //nolint:revive // stutter: name fixed by the spec
	// DocumentID is the processed document.
	DocumentID port.DocumentID
	// UnitsCreated counts the units this run inserted.
	UnitsCreated int
	// UnitIDs are the inserted units, in chunk and extraction order.
	UnitIDs []port.UnitID
	// Unchanged reports a no-op: same content and overrides hash as the last completed retain.
	Unchanged bool
	// ChunksTotal is the number of chunks the document split into.
	ChunksTotal int
	// ChunksSkipped counts chunks already committed with a matching hash (a resumed retain).
	ChunksSkipped int
	// Scopes are the observation scopes the new units consolidate under (from the document's tags; empty
	// when no ScopeExpander is wired).
	Scopes []port.ScopeKey
	// Usage is per-slot token usage of the extraction calls (EXTRACT), summed per slot and model.
	Usage []domain.Usage
}

// RetainerDeps are the Retainer's dependencies.
type RetainerDeps struct {
	// Store opens the read and write transactions.
	Store port.Store
	// Blob holds the original text.
	Blob port.Blob
	// Config resolves the effective config (server < tenant < bank < job overrides).
	Config port.ConfigResolver
	// Extractor turns chunks into facts.
	Extractor Extractor
	// Dates resolves relative time expressions (timex.TimeNormalizer).
	Dates DateNormalizer
	// Embedder embeds fact texts.
	Embedder port.Embedder
	// Entities resolves free-form mentions.
	Entities EntityResolver
	// Labels resolves label entities.
	Labels LabelResolver
	// Linker adds temporal, semantic and causal links.
	Linker Linker
	// Scopes expands the document's tags into scopes for RetainResult.Scopes; nil = none reported.
	Scopes ScopeExpander
	// Tokens counts tokens for chunking; nil = ApproxTokens.
	Tokens TokenCounter
	// Clock stamps events and progress.
	Clock port.Clock
	// Checkpoint returns the progress reporter of a job; nil = no progress (inline runs never report).
	Checkpoint func(port.OperationID) Checkpoint
	// NewID generates unit, chunk and run ids; nil = uuid.NewString.
	NewID func() string
	// BatchChunks is how many chunks one transaction commits; 0 = DefaultBatchChunks.
	BatchChunks int
}

// retainer is the Retainer.
type retainer struct {
	d RetainerDeps
}

var _ Retainer = (*retainer)(nil)

// NewRetainer returns the Retainer.
func NewRetainer(d RetainerDeps) Retainer {
	if d.Tokens == nil {
		d.Tokens = ApproxTokens{}
	}
	if d.NewID == nil {
		d.NewID = uuid.NewString
	}
	if d.BatchChunks <= 0 {
		d.BatchChunks = DefaultBatchChunks
	}
	return &retainer{d: d}
}

// run is one ProcessDocument call's state.
type run struct {
	j         port.Job
	p         DocumentPayload
	cfg       domain.BankConfig
	t         tuning
	oh        string
	tags      []string
	mentioned *time.Time // the units' mention time; nil when timeless
	replaced  bool       // the document's old units are gone (this run, or an earlier attempt of it, replaced them)
}

// ProcessDocument implements Retainer.
func (r *retainer) ProcessDocument(ctx context.Context, j port.Job) (RetainResult, error) {
	rn, err := r.start(ctx, j)
	if err != nil {
		return RetainResult{}, err
	}
	res := RetainResult{DocumentID: rn.p.DocumentID}
	doc, committed, err := r.readState(ctx, j.Bank, rn.p.DocumentID)
	if err != nil {
		return res, err
	}
	if !rn.p.ForceReextract && doc.ExtractedAt != nil && doc.ContentHash == rn.p.ContentHash &&
		doc.OverridesHash == rn.oh {
		res.Unchanged = true
		return res, nil
	}
	chunks, err := r.chunks(ctx, rn)
	if err != nil {
		return res, err
	}
	rn.replaced = anyCommitted(committed, chunks)
	res.ChunksTotal, res.Scopes = len(chunks), r.scopes(rn)
	for start := 0; start < len(chunks); start += r.d.BatchChunks {
		batch := chunks[start:min(start+r.d.BatchChunks, len(chunks))]
		pending := uncommitted(committed, batch)
		res.ChunksSkipped += len(batch) - len(pending)
		if len(pending) == 0 {
			continue
		}
		if err := r.checkpoint(ctx, j.ID, start, len(chunks)); err != nil {
			return res, err
		}
		ids, usage, err := r.processBatch(ctx, rn, pending)
		res.Usage = append(res.Usage, usage...)
		if err != nil {
			return res, err
		}
		res.UnitIDs = append(res.UnitIDs, ids...)
	}
	if err := r.complete(ctx, rn); err != nil {
		return res, err
	}
	res.UnitsCreated, res.Usage = len(res.UnitIDs), SumUsage(res.Usage)
	return res, nil
}

// start decodes the job and resolves its config.
func (r *retainer) start(ctx context.Context, j port.Job) (*run, error) {
	p, err := DecodePayload(j.Payload)
	if err != nil {
		return nil, err
	}
	var levels []domain.BankConfig
	if j.Overrides != nil {
		levels = append(levels, *j.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, j.Bank, levels...)
	if err != nil {
		return nil, err
	}
	oh, err := OverridesHash(j.Overrides)
	if err != nil {
		return nil, err
	}
	rn := &run{j: j, p: p, cfg: cfg, t: tuningOf(cfg), oh: oh, tags: domain.NormalizeTags(p.Tags)}
	switch {
	case p.Timeless:
	case p.Timestamp != nil:
		at := p.Timestamp.UTC()
		rn.mentioned = &at
	default:
		at := r.now().UTC()
		rn.mentioned = &at
	}
	return rn, nil
}

// readState reads the document and its committed chunk hashes in one short read transaction.
func (r *retainer) readState(
	ctx context.Context, b port.BankRef, id port.DocumentID,
) (domain.Document, map[int]string, error) {
	var doc domain.Document
	var committed map[int]string
	err := r.d.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		if doc, err = tx.Documents().Get(ctx, b, id); err != nil {
			return err
		}
		committed, err = tx.Chunks().Committed(ctx, b, id)
		return err
	})
	return doc, committed, err
}

// chunks reads the pinned blob and chunks it; each chunk's stored hash is salted with the run key (the job
// id, stable across retries; a fresh id for an inline run).
func (r *retainer) chunks(ctx context.Context, rn *run) ([]domain.Chunk, error) {
	rc, err := r.d.Blob.Get(ctx, rn.p.BlobKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("%w: read blob %s: %w", domain.ErrUnavailable, rn.p.BlobKey, err)
	}
	if got := ContentHash(string(raw)); got != rn.p.ContentHash {
		return nil, fmt.Errorf("%w: blob %s hashes to %s, payload says %s", domain.ErrFailedPrecondition,
			rn.p.BlobKey, got, rn.p.ContentHash)
	}
	runKey := string(rn.j.ID)
	if runKey == "" {
		runKey = r.d.NewID()
	}
	chunks := ChunkWith(string(raw), ChunkOptions{
		MaxTokens: rn.t.chunkMax, OverlapTokens: rn.t.chunkOverlap, StructuredMaxTokens: rn.t.structuredMax,
	}, r.d.Tokens)
	for i := range chunks {
		chunks[i].Bank, chunks[i].DocumentID = rn.j.Bank, rn.p.DocumentID
		chunks[i].ContentHash = runHash(runKey, chunks[i].ContentHash)
	}
	return chunks, nil
}

// anyCommitted reports whether some chunk of this run is already committed, i.e. an earlier attempt of the
// run already replaced the document's old units.
func anyCommitted(committed map[int]string, chunks []domain.Chunk) bool {
	for _, c := range chunks {
		if committed[c.Index] == c.ContentHash {
			return true
		}
	}
	return false
}

// uncommitted returns the chunks of batch this run has not committed yet.
func uncommitted(committed map[int]string, batch []domain.Chunk) []domain.Chunk {
	var out []domain.Chunk
	for _, c := range batch {
		if committed[c.Index] != c.ContentHash {
			out = append(out, c)
		}
	}
	return out
}

// scopes expands the document's tags under its scope mode, when a ScopeExpander is wired.
func (r *retainer) scopes(rn *run) []port.ScopeKey {
	if r.d.Scopes == nil {
		return nil
	}
	s, err := r.d.Scopes.Expand(rn.j.Bank, rn.tags, scopeMode(rn.p.ObservationScopes, rn.cfg))
	if err != nil {
		return nil
	}
	return s
}

// scopeMode is the item's fan-out, else the bank default, else combined.
func scopeMode(item *domain.ObservationScopes, cfg domain.BankConfig) domain.ObservationScopes {
	switch {
	case item != nil && item.Mode != domain.ScopeModeUnspecified:
		return *item
	case cfg.DefaultObservationScopes != nil && cfg.DefaultObservationScopes.Mode != domain.ScopeModeUnspecified:
		return *cfg.DefaultObservationScopes
	default:
		return domain.ObservationScopes{Mode: domain.ScopeModeCombined}
	}
}

// checkpoint reports progress for a job and stops it once cancelled.
func (r *retainer) checkpoint(ctx context.Context, id port.OperationID, done, total int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrCancelled, err)
	}
	if id == "" || r.d.Checkpoint == nil {
		return nil
	}
	cp := r.d.Checkpoint(id)
	if cp == nil {
		return nil
	}
	if err := cp.Report(ctx, domain.Progress{
		Stage: "retain_chunks", At: r.now(), Processed: int64(done), Total: int64(total),
	}); err != nil {
		return fmt.Errorf("retain: checkpoint: %w", err)
	}
	return nil
}

// now reads the injected clock.
func (r *retainer) now() time.Time {
	if r.d.Clock == nil {
		return time.Time{}
	}
	return r.d.Clock.Now()
}

// SumUsage sums usage entries per (slot, model), keeping first-seen order.
func SumUsage(us []domain.Usage) []domain.Usage {
	var out []domain.Usage
	for _, u := range us {
		found := false
		for i := range out {
			if out[i].Slot == u.Slot && out[i].Model == u.Model {
				out[i].InputTokens += u.InputTokens
				out[i].OutputTokens += u.OutputTokens
				found = true
				break
			}
		}
		if !found {
			out = append(out, u)
		}
	}
	return out
}
