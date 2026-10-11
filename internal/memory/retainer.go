package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"memd/internal/ai"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/store"
	"memd/internal/timex"
	"time"

	"github.com/google/uuid"
)

// documentResult summarizes one processed document.
type documentResult struct {
	// DocumentID is the processed document.
	DocumentID domain.DocumentID
	// MemoriesCreated counts the units this run inserted.
	MemoriesCreated int
	// UnitIDs are the inserted units, in chunk and extraction order.
	UnitIDs []domain.UnitID
	// Unchanged reports a no-op: same content and overrides hash as the last completed retain.
	Unchanged bool
	// Superseded reports that a newer retain of the document took over (its blob or overrides replaced this
	// run's, or it rewrote a chunk this run committed). The run stopped without completing the document;
	// whatever it committed before was replaced by, or is replaced by, the newer retain. A
	// change of the document's tags alone never supersedes: the run adopts them.
	Superseded bool
	// ChunksTotal is the number of chunks the document split into.
	ChunksTotal int
	// ChunksSkipped counts chunks already committed with a matching hash (a resumed retain).
	ChunksSkipped int
	// Scopes are the observation scopes the new units consolidate under (domain.ExpandScopes over the
	// document's tags).
	Scopes []domain.ScopeKey
	// Usage is per-slot token usage of the extraction calls (EXTRACT), summed per slot and model.
	Usage []domain.Usage
}

// retainerDeps are the retainer's dependencies.
type retainerDeps struct {
	// Store opens the read and write transactions.
	Store store.DB
	// Blob holds the original text.
	Blob blob.Store
	// Config resolves the effective config (server < tenant < bank < job overrides).
	Config *config.Resolver
	// Extractor turns chunks into facts.
	Extractor extractor
	// Dates resolves relative time expressions; nil = facts keep only their absolute dates.
	Dates *timex.Normalizer
	// Embedder embeds fact texts.
	Embedder ai.Embedder
	// Entities resolves free-form mentions.
	Entities *linking.Resolver
	// Labels resolves label entities.
	Labels *linking.LabelResolver
	// Linker adds temporal, semantic and causal links.
	Linker *linking.Linker
	// Tokens counts tokens for chunking; nil = approxTokens.
	Tokens tokenCounter
	// Clock stamps events and progress.
	Clock clock.Clock
	// NewID generates unit, chunk and run ids; nil = uuid.NewString.
	NewID func() string
	// BatchChunks is how many chunks one transaction commits; 0 = defaultBatchChunks.
	BatchChunks int
}

// retainer processes one document: the retain_document job, and the inline run of a sync Retain.
type retainer struct {
	d retainerDeps
}

// newRetainer returns the retainer.
func newRetainer(d retainerDeps) *retainer {
	if d.Tokens == nil {
		d.Tokens = approxTokens{}
	}
	if d.NewID == nil {
		d.NewID = uuid.NewString
	}
	if d.BatchChunks <= 0 {
		d.BatchChunks = defaultBatchChunks
	}
	return &retainer{d: d}
}

// ErrSuperseded is returned by a sync Retain whose document was taken over by a newer retain while it ran
// (ABORTED: the client may retry). Async runs end without error and report documentResult.Superseded.
var ErrSuperseded = fmt.Errorf("%w: a newer retain of the document took over", domain.ErrConflict)

// errSuperseded aborts a write transaction of a superseded run.
var errSuperseded = errors.New("retain: run superseded")

// run is one ProcessDocument call's state.
type run struct {
	job       *jobs.Run // reports progress; inline runs report nothing
	j         store.Job
	p         jobs.RetainPayload
	cfg       domain.BankConfig
	t         tuning
	oh        string // no-op hash: overrides + item scopes (stamped on the document)
	ownOH     string // hash of the job's overrides alone, compared with the document's stored overrides
	tags      []string
	hashes    map[int]string // this run's salted hash per chunk index
	owned     []int          // chunk indexes this run (or an earlier attempt of it) committed
	mentioned *time.Time     // the units' mention time; nil when timeless
	replaced  bool           // the document's old units are gone (this run, or an earlier attempt of it, replaced them)
}

// ProcessDocument runs chunk -> extract -> normalize -> embed -> resolve -> link -> commit, one tx per chunk
// batch. Unchanged content + overrides hash with extracted_at set = no-op (unless force_reextract). A changed
// document replaces its old units in the first batch's tx, which also clears extracted_at if an overlapping
// retain completed the document meanwhile; a retried job skips chunks already committed with a
// matching hash. job.Job.Payload is a jobs.RetainPayload and job.Job.Overrides the merged request < item
// overrides. A claimed run resumes across retries and reports progress through job; an inline run
// (jobs.Inline, no id) starts fresh and reports nothing.
func (r *retainer) ProcessDocument(ctx context.Context, job *jobs.Run) (documentResult, error) {
	rn, err := r.start(ctx, job)
	if err != nil {
		return documentResult{}, err
	}
	res := documentResult{DocumentID: rn.p.DocumentID}
	doc, committed, err := r.readState(ctx, rn.j.Bank, rn.p.DocumentID)
	if err != nil {
		return res, err
	}
	if !rn.p.ForceReextract && doc.ExtractedAt != nil && doc.ContentHash == rn.p.ContentHash &&
		doc.OverridesHash == rn.oh {
		res.Unchanged = true
		return res, nil
	}
	if superseded, err := r.docSuperseded(doc, rn); err != nil || superseded {
		res.Superseded = superseded
		return res, err
	}
	chunks, err := r.chunks(ctx, rn)
	if err != nil {
		return res, err
	}
	committed = trustedCommitted(committed, chunks, doc)
	rn.adopt(committed, chunks)
	res.ChunksTotal = len(chunks)
	err = r.processChunks(ctx, rn, chunks, committed, &res)
	if err == nil {
		err = r.complete(ctx, rn)
	}
	// The scopes follow the tags the run ended with: a retag mid-run is adopted (docSuperseded).
	res.MemoriesCreated, res.Usage, res.Scopes = len(res.UnitIDs), sumUsage(res.Usage), r.scopes(rn)
	if errors.Is(err, errSuperseded) {
		res.Superseded, res.MemoriesCreated, res.UnitIDs = true, 0, nil
		return res, nil
	}
	return res, err
}

// processChunks extracts and commits every chunk this run has not committed yet, batch by batch.
func (r *retainer) processChunks(
	ctx context.Context, rn *run, chunks []domain.Chunk, committed map[int]string, res *documentResult,
) error {
	for start := 0; start < len(chunks); start += r.d.BatchChunks {
		batch := chunks[start:min(start+r.d.BatchChunks, len(chunks))]
		pending := uncommitted(committed, batch)
		res.ChunksSkipped += len(batch) - len(pending)
		if len(pending) == 0 {
			continue
		}
		if err := r.checkpoint(ctx, rn, start, len(chunks)); err != nil {
			return err
		}
		ids, usage, err := r.processBatch(ctx, rn, pending)
		res.Usage = append(res.Usage, usage...)
		if err != nil {
			return err
		}
		res.UnitIDs = append(res.UnitIDs, ids...)
	}
	return nil
}

// start decodes the job and resolves its config.
func (r *retainer) start(ctx context.Context, job *jobs.Run) (*run, error) {
	j := job.Job
	p, err := decodePayload(j.Payload)
	if err != nil {
		return nil, err
	}
	var levels []domain.BankConfig
	if j.Overrides != nil {
		levels = append(levels, *j.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, j.Bank, levels...)
	if err != nil {
		return nil, fmt.Errorf("retain: %w", err)
	}
	oh, err := noopHash(j.Overrides, p.ObservationScopeModes)
	if err != nil {
		return nil, err
	}
	ownOH, err := overridesHash(j.Overrides)
	if err != nil {
		return nil, err
	}
	rn := &run{
		job: job, j: j, p: p, cfg: cfg, t: pinTuning(p, tuningOf(cfg)), oh: oh, ownOH: ownOH,
		tags: domain.NormalizeTags(p.Tags),
	}
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
	ctx context.Context, b domain.BankRef, id domain.DocumentID,
) (domain.Document, map[int]string, error) {
	var doc domain.Document
	var committed map[int]string
	err := r.d.Store.InTx(ctx, func(tx store.Tx) error {
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
	if got := contentHash(string(raw)); got != rn.p.ContentHash {
		return nil, fmt.Errorf("%w: blob %s hashes to %s, payload says %s", domain.ErrFailedPrecondition,
			rn.p.BlobKey, got, rn.p.ContentHash)
	}
	runKey := string(rn.j.ID)
	if runKey == "" {
		runKey = r.d.NewID()
	}
	chunks := chunkWith(string(raw), chunkOptions{
		MaxTokens: rn.t.chunkMax, OverlapTokens: rn.t.chunkOverlap, StructuredMaxTokens: rn.t.structuredMax,
	}, r.d.Tokens)
	rn.hashes = make(map[int]string, len(chunks))
	for i := range chunks {
		chunks[i].Bank, chunks[i].DocumentID = rn.j.Bank, rn.p.DocumentID
		chunks[i].ContentHash = runHash(runKey, chunks[i].ContentHash)
		rn.hashes[chunks[i].Index] = chunks[i].ContentHash
	}
	return chunks, nil
}

// trustedCommitted keeps the committed chunk hashes only when this run's first chunk is among them and the
// document is still in flight; otherwise nothing of this run is trusted to be committed and the run starts
// over with a full replacement. Rows at indexes past the current content's chunks are dropped.
func trustedCommitted(committed map[int]string, chunks []domain.Chunk, doc domain.Document) map[int]string {
	if len(chunks) == 0 || doc.ExtractedAt != nil || committed[0] != chunks[0].ContentHash {
		return nil
	}
	out := make(map[int]string, len(chunks))
	for _, c := range chunks {
		if h, ok := committed[c.Index]; ok {
			out[c.Index] = h
		}
	}
	return out
}

// adopt records the chunks this run already committed; if there are any, an earlier attempt of the run
// already replaced the document's old units.
func (rn *run) adopt(committed map[int]string, chunks []domain.Chunk) {
	for _, c := range chunks {
		if committed[c.Index] == c.ContentHash {
			rn.owned = append(rn.owned, c.Index)
		}
	}
	rn.replaced = len(rn.owned) > 0
}

// docSuperseded reports whether a newer retain owns the document: its stored blob key or overrides are no longer this
// run's. When only the tags differ (RetagDocument retagged the document, or a newer retain of the same content and
// overrides changed them), the run still owns it: it adopts the document's current tags for the units it writes from
// now on, and the retag_document job queued behind it on the same "doc:" key retags the units it already wrote
// (docs/design/retain.md#ownership-and-resume). So no run ever writes stale tags over newer ones.
func (r *retainer) docSuperseded(doc domain.Document, rn *run) (bool, error) {
	if doc.BlobKey != rn.p.BlobKey {
		return true, nil
	}
	oh, err := overridesHash(doc.Overrides)
	if err != nil {
		return false, err
	}
	if oh != rn.ownOH {
		return true, nil
	}
	if !domain.SameTagSet(doc.Tags, rn.tags) {
		rn.tags = domain.NormalizeTags(doc.Tags)
	}
	return false, nil
}

// checkOwner runs inside a write transaction, after Tx.Seq has row-locked the bank (so it cannot race
// another retain's write or a retag): it fails with errSuperseded when a newer retain owns the document or
// rewrote a chunk this run committed, and otherwise adopts the document's current tags (docSuperseded), so
// the units this transaction writes carry them.
func (r *retainer) checkOwner(ctx context.Context, tx store.Tx, rn *run) error {
	doc, err := tx.Documents().Get(ctx, rn.j.Bank, rn.p.DocumentID)
	if err != nil {
		return err
	}
	if superseded, err := r.docSuperseded(doc, rn); err != nil || superseded {
		if err != nil {
			return err
		}
		return errSuperseded
	}
	if len(rn.owned) == 0 {
		return nil
	}
	committed, err := tx.Chunks().Committed(ctx, rn.j.Bank, rn.p.DocumentID)
	if err != nil {
		return err
	}
	for _, i := range rn.owned {
		if committed[i] != rn.hashes[i] {
			return errSuperseded
		}
	}
	return nil
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

// scopes expands the document's tags under its scope mode.
func (r *retainer) scopes(rn *run) []domain.ScopeKey {
	s, err := domain.ExpandScopes(rn.j.Bank, rn.tags, scopeMode(rn.p.ObservationScopeModes, rn.cfg))
	if err != nil {
		return nil
	}
	return s
}

// scopeMode is the item's fan-out, else the bank default, else combined.
func scopeMode(item *domain.ObservationScopeModes, cfg domain.BankConfig) domain.ObservationScopeModes {
	switch {
	case item != nil && item.Mode != domain.ScopeModeUnspecified:
		return *item
	case cfg.GetRetain().DefaultObservationScopeModes != nil &&
		cfg.GetRetain().DefaultObservationScopeModes.Mode != domain.ScopeModeUnspecified:
		return *cfg.GetRetain().DefaultObservationScopeModes
	default:
		return domain.ObservationScopeModes{Mode: domain.ScopeModeCombined}
	}
}

// checkpoint reports progress for a job and stops it once cancelled.
func (r *retainer) checkpoint(ctx context.Context, rn *run, done, total int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrCancelled, err)
	}
	if err := rn.job.Report(ctx, domain.Progress{
		Stage: "retain_chunks", RecordedAt: r.now(), Processed: int64(done), Total: int64(total),
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

// sumUsage sums usage entries per (slot, model), keeping first-seen order.
func sumUsage(us []domain.Usage) []domain.Usage {
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
