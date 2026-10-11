package retain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/blob"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Ingestor is the api-side half of Retain.
type Ingestor interface {
	// Ingest validates (scope fan-out, overrides), stores blobs, then runs inline or enqueues. An
	// ALL_COMBINATIONS fan-out over max_combinations fails with domain.ErrTooManyScopes before anything
	// is stored. An item whose content and overrides hash match its document's last completed retain is a
	// no-op (Unchanged) unless ForceReextract is set: nothing is written for it and no model is called.
	Ingest(ctx context.Context, req IngestRequest) (IngestResult, error)
}

// IngestRequest mirrors memory.v1.RetainRequest with domain types.
type IngestRequest struct {
	// Bank receives the items; an unknown bank is created with tenant defaults.
	Bank domain.BankRef
	// Items are the documents to retain.
	Items []IngestItem
	// DocumentTags are added to every item's tags.
	DocumentTags []string
	// Overrides apply to every item, below each item's own overrides; nil = none.
	Overrides *domain.BankConfig
	// SkipConsolidation suppresses the automatic consolidate job after retain.
	SkipConsolidation bool
	// RunAsync enqueues retain_document jobs and returns operation ids instead of waiting for units.
	RunAsync bool
}

// IngestItem mirrors memory.v1.RetainItem: one document to retain.
type IngestItem struct {
	// Content is the original text.
	Content string
	// Context is free-form context stored on every unit (speaker, conversation, source).
	Context string
	// Timestamp is when the content was said; nil = now (from clock.Clock).
	Timestamp *time.Time
	// Timeless marks content with no time anchor; it wins over Timestamp.
	Timeless bool
	// DocumentID is the upsert key (replace); empty = generated.
	DocumentID domain.DocumentID
	// Tags are the item's tags (merged with IngestRequest.DocumentTags).
	Tags []string
	// ObservationScopes picks the item's scope fan-out; nil = the bank's default.
	ObservationScopes *domain.ObservationScopes
	// Metadata is string metadata stored on every unit.
	Metadata map[string]string
	// Entities are caller-supplied entity hints merged with extracted mentions.
	Entities []domain.EntityHint
	// ResolveEntities false takes entity names literally; nil = true.
	ResolveEntities *bool
	// Overrides are item-level overrides, the highest level; nil = none.
	Overrides *domain.BankConfig
	// ForceReextract re-runs extraction even when content and overrides are unchanged.
	ForceReextract bool
}

// IngestResult mirrors memory.v1.RetainResponse.
type IngestResult struct {
	// Documents has one entry per item, in item order: sync = filled; async = operation ids only.
	Documents []RetainedDocument
	// ParentOperationID is the retain_batch parent, set when an async retain enqueues more than one document.
	ParentOperationID domain.OperationID
	// Usage is the per-slot token usage of a sync retain's model calls (RetainResponse.usage); empty for
	// async (each job records its own).
	Usage []domain.Usage
}

// RetainedDocument mirrors memory.v1.RetainedDocument: the outcome for one item.
type RetainedDocument struct {
	// DocumentID is the item's document (generated when the item left it empty).
	DocumentID domain.DocumentID
	// UnitsCreated counts the units inserted (sync only).
	UnitsCreated int
	// Unchanged reports the same content + overrides hash; nothing re-extracted.
	Unchanged bool
	// OperationID is the retain_document operation (async), or empty for an inline run.
	OperationID domain.OperationID
}

// IngestorDeps are the Ingestor's dependencies.
type IngestorDeps struct {
	// Store opens the transactions.
	Store store.DB
	// Blob stores the original text.
	Blob blob.Store
	// Config resolves the bank's effective config.
	Config *config.Resolver
	// Retainer runs a sync retain inline.
	Retainer Retainer
	// Merge merges config levels (config.Merge): the request and item overrides stored on the document and
	// job. Needed only when both carry overrides.
	Merge ConfigMerger
	// Clock is "now" for items without a timestamp.
	Clock clock.Clock
	// NewID generates document ids; nil = uuid.NewString.
	NewID func() string
}

// ingestor is the Ingestor.
type ingestor struct {
	d IngestorDeps
}

var _ Ingestor = (*ingestor)(nil)

// NewIngestor returns the Ingestor.
func NewIngestor(d IngestorDeps) Ingestor {
	if d.NewID == nil {
		d.NewID = uuid.NewString
	}
	return &ingestor{d: d}
}

// itemPlan is one validated item: its document, payload and hashes.
type itemPlan struct {
	doc       domain.DocumentID
	content   string
	overrides *domain.BankConfig
	oh        string
	payload   jobs.RetainPayload
	unchanged bool
}

// Ingest implements Ingestor.
func (i *ingestor) Ingest(ctx context.Context, req IngestRequest) (IngestResult, error) {
	if req.Bank.IsZero() {
		return IngestResult{}, fmt.Errorf("%w: retain needs a bank", domain.ErrInvalidArgument)
	}
	if len(req.Items) == 0 {
		return IngestResult{}, fmt.Errorf("%w: retain needs at least one item", domain.ErrInvalidArgument)
	}
	base, known, err := i.bankConfig(ctx, req.Bank)
	if err != nil {
		return IngestResult{}, err
	}
	plans := make([]*itemPlan, len(req.Items))
	seen := map[domain.DocumentID]bool{}
	for k, it := range req.Items {
		if plans[k], err = i.plan(ctx, req, it, base, known); err != nil {
			return IngestResult{}, fmt.Errorf("item %d: %w", k, err)
		}
		if seen[plans[k].doc] {
			return IngestResult{}, fmt.Errorf("%w: item %d: document %q appears twice", domain.ErrInvalidArgument, k,
				plans[k].doc)
		}
		seen[plans[k].doc] = true
	}
	if err := i.markUnchanged(ctx, req.Bank, plans); err != nil {
		return IngestResult{}, err
	}
	if err := i.storeBlobs(ctx, plans); err != nil {
		return IngestResult{}, err
	}
	res, err := i.write(ctx, req, plans)
	if err != nil || req.RunAsync {
		return res, err
	}
	return i.runInline(ctx, req.Bank, plans, res)
}

// bankConfig returns the bank's effective config, and whether the bank exists. An unknown bank is not created
// here (validation must store nothing); its config for validation is the tenant level, and the write
// transaction creates it. The server level only adds defaults that cannot fail validation.
func (i *ingestor) bankConfig(ctx context.Context, b domain.BankRef) (domain.BankConfig, bool, error) {
	cfg, err := i.d.Config.Effective(ctx, b)
	if err == nil {
		return cfg, true, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return cfg, false, fmt.Errorf("retain: %w", err)
	}
	var tenant domain.BankConfig
	err = i.d.Store.InTx(ctx, func(tx store.Tx) error {
		var err error
		tenant, err = tx.Banks().TenantConfig(ctx, b.Tenant)
		return err
	})
	return tenant, false, err
}

// plan validates one item and builds its document payload. Nothing is stored.
func (i *ingestor) plan(
	ctx context.Context, req IngestRequest, it IngestItem, base domain.BankConfig, known bool,
) (*itemPlan, error) {
	overrides, err := i.mergeOverrides(req.Overrides, it.Overrides)
	if err != nil {
		return nil, err
	}
	cfg, err := i.itemConfig(ctx, req.Bank, base, known, overrides)
	if err != nil {
		return nil, err
	}
	if r := cfg.GetRetain(); r.ExtractionMode == domain.ExtractionModeCustom &&
		strings.TrimSpace(r.CustomExtractionPrompt) == "" {
		return nil, fmt.Errorf("%w: extraction mode custom needs custom_extraction_prompt", domain.ErrInvalidArgument)
	}
	tags := domain.NormalizeTags(append(slices.Clone(it.Tags), req.DocumentTags...))
	if err := i.checkFanout(req.Bank, tags, scopeMode(it.ObservationScopes, cfg)); err != nil {
		return nil, err
	}
	oh, err := NoopHash(overrides, it.ObservationScopes)
	if err != nil {
		return nil, err
	}
	doc := it.DocumentID
	if doc == "" {
		doc = domain.DocumentID(i.d.NewID())
	}
	p := jobs.RetainPayload{
		DocumentID: doc, ContentHash: ContentHash(it.Content), Tags: tags, Context: it.Context,
		Timeless: it.Timeless, Metadata: it.Metadata, Entities: payloadHints(it.Entities),
		ResolveEntities: it.ResolveEntities, ObservationScopes: it.ObservationScopes,
		ForceReextract: it.ForceReextract, SkipConsolidation: req.SkipConsolidation,
	}
	p.BlobKey = blob.Key(req.Bank, doc, p.ContentHash)
	t := tuningOf(cfg)
	p.ChunkMaxTokens, p.ChunkOverlapTokens, p.StructuredChunkMaxTokens = t.chunkMax, t.chunkOverlap, t.structuredMax
	if !it.Timeless {
		at := i.d.Clock.Now().UTC()
		if it.Timestamp != nil {
			at = it.Timestamp.UTC()
		}
		p.Timestamp = &at
	}
	return &itemPlan{doc: doc, content: it.Content, overrides: overrides, oh: oh, payload: p}, nil
}

// itemConfig is an item's effective config: the resolver's for a known bank, else the tenant level merged with
// the item's overrides.
func (i *ingestor) itemConfig(
	ctx context.Context, b domain.BankRef, base domain.BankConfig, known bool, overrides *domain.BankConfig,
) (domain.BankConfig, error) {
	switch {
	case overrides == nil:
		return base, nil
	case known:
		return i.d.Config.Effective(ctx, b, *overrides) //nolint:wrapcheck // already a domain error class
	case i.d.Merge == nil:
		return domain.BankConfig{}, fmt.Errorf("%w: retain: no config merger wired", domain.ErrUnavailable)
	}
	return i.d.Merge(base, *overrides), nil
}

// mergeOverrides merges request < item overrides; nil when neither is set.
func (i *ingestor) mergeOverrides(req, item *domain.BankConfig) (*domain.BankConfig, error) {
	switch {
	case req == nil && item == nil:
		return nil, nil
	case req == nil:
		o := *item
		return &o, nil
	case item == nil:
		o := *req
		return &o, nil
	case i.d.Merge == nil:
		return nil, fmt.Errorf("%w: retain: no config merger wired", domain.ErrUnavailable)
	}
	o := i.d.Merge(*req, *item)
	return &o, nil
}

// checkFanout rejects a scope fan-out over its cap (domain.ExpandScopes).
func (i *ingestor) checkFanout(b domain.BankRef, tags []string, s domain.ObservationScopes) error {
	if _, err := domain.ExpandScopes(b, tags, s); err != nil {
		return fmt.Errorf("retain: observation scopes: %w", err)
	}
	return nil
}

// markUnchanged flags items whose document's last completed retain had the same content and overrides
// hash (and no force_reextract). It reads before anything is written: Upsert would clear extracted_at. It
// also rejects a bank being deleted (domain.ErrBankDeleting) before storeBlobs, so a retain racing
// DeleteBank leaves no blob under the bank's prefix; an unknown bank is fine (the write creates it).
func (i *ingestor) markUnchanged(ctx context.Context, b domain.BankRef, plans []*itemPlan) error {
	return i.d.Store.InTx(ctx, func(tx store.Tx) error {
		bank, err := tx.Banks().Get(ctx, b)
		switch {
		case err == nil && bank.Deleting():
			return fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			return err
		}
		for _, p := range plans {
			p.unchanged = false
			if p.payload.ForceReextract {
				continue
			}
			d, err := tx.Documents().Get(ctx, b, p.doc)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			p.unchanged = d.ExtractedAt != nil && d.ContentHash == p.payload.ContentHash && d.OverridesHash == p.oh
		}
		return nil
	})
}

// storeBlobs writes each changed item's original text under its content-addressed key, before any
// transaction (a repeated Put of the same key is harmless).
func (i *ingestor) storeBlobs(ctx context.Context, plans []*itemPlan) error {
	for _, p := range plans {
		if p.unchanged {
			continue
		}
		err := i.d.Blob.Put(ctx, p.payload.BlobKey, strings.NewReader(p.content), int64(len(p.content)),
			"text/plain; charset=utf-8")
		if err != nil {
			return err
		}
	}
	return nil
}

// write upserts the changed documents and, for an async retain, enqueues their retain_document jobs (under a
// retain_batch parent when there are several), in one transaction.
func (i *ingestor) write(ctx context.Context, req IngestRequest, plans []*itemPlan) (IngestResult, error) {
	var res IngestResult
	var opIDs []domain.OperationID
	err := i.d.Store.InTx(ctx, func(tx store.Tx) error {
		res, opIDs = IngestResult{}, make([]domain.OperationID, len(plans))
		changed := 0
		for _, p := range plans {
			if !p.unchanged {
				changed++
			}
		}
		if changed == 0 {
			return nil
		}
		if _, err := tx.Banks().Ensure(ctx, req.Bank); err != nil { // a write to an unknown bank creates it
			return err
		}
		if _, err := tx.Seq(ctx, req.Bank); err != nil { // Seq before Enqueue (lock order with ReclaimStale)
			return err
		}
		for _, p := range plans {
			if p.unchanged {
				continue
			}
			if err := tx.Documents().Upsert(ctx, domain.Document{
				ID: p.doc, Bank: req.Bank, Overrides: p.overrides, Tags: p.payload.Tags, BlobKey: p.payload.BlobKey,
				ObservationScopes: p.payload.ObservationScopes,
			}); err != nil {
				return err
			}
		}
		if !req.RunAsync {
			return nil
		}
		var err error
		res.ParentOperationID, err = i.enqueue(ctx, tx, req.Bank, plans, changed, opIDs)
		return err
	})
	if err != nil {
		return IngestResult{}, err
	}
	for k, p := range plans {
		res.Documents = append(res.Documents, RetainedDocument{
			DocumentID: p.doc, Unchanged: p.unchanged, OperationID: opIDs[k],
		})
	}
	return res, nil
}

// enqueue inserts the retain_document jobs of the changed plans, under a retain_batch parent when there are
// several, and records their ids in opIDs.
func (i *ingestor) enqueue(
	ctx context.Context, tx store.Tx, b domain.BankRef, plans []*itemPlan, changed int, opIDs []domain.OperationID,
) (domain.OperationID, error) {
	var parent domain.OperationID
	if changed > 1 {
		id, _, err := jobs.EnqueueRetainBatch(ctx, tx, b, changed)
		if err != nil {
			return "", err
		}
		parent = id
	}
	for k, p := range plans {
		if p.unchanged {
			continue
		}
		var err error
		opIDs[k], _, err = jobs.EnqueueRetainDocument(ctx, tx, b, p.payload, p.overrides, parent)
		if err != nil {
			return "", err
		}
	}
	return parent, nil
}

// runInline runs the Retainer for each changed document of a sync retain, in item order.
func (i *ingestor) runInline(
	ctx context.Context, b domain.BankRef, plans []*itemPlan, res IngestResult,
) (IngestResult, error) {
	var usage []domain.Usage
	for k, p := range plans {
		if p.unchanged {
			continue
		}
		payload, err := json.Marshal(p.payload)
		if err != nil {
			return IngestResult{}, fmt.Errorf("retain: encode payload: %w", err)
		}
		out, err := i.d.Retainer.ProcessDocument(ctx, jobs.Inline(store.Job{
			Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: jobs.DocumentSerialKey(p.doc),
			Payload: payload, Overrides: p.overrides,
		}))
		usage = append(usage, out.Usage...)
		if err == nil && out.Superseded {
			err = ErrSuperseded
		}
		if err != nil {
			return IngestResult{}, fmt.Errorf("retain document %s: %w", p.doc, err)
		}
		res.Documents[k].UnitsCreated = out.UnitsCreated
		res.Documents[k].Unchanged = out.Unchanged
	}
	res.Usage = sumUsage(usage)
	return res, nil
}
