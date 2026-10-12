package memory

import (
	"context"
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

// RetainRequest mirrors memory.v1.RetainRequest with domain types.
type RetainRequest struct {
	// Bank receives the items; an unknown bank of an existing org is created (an unknown org is
	// domain.ErrOrgNotFound).
	Bank domain.BankRef
	// Items are the documents to retain.
	Items []RetainItem
	// DocumentTags are added to every item's tags.
	DocumentTags []string
	// Overrides apply to every item, below each item's own overrides; nil = none.
	Overrides *domain.BankConfig
	// SkipConsolidation suppresses the automatic consolidate job after retain.
	SkipConsolidation bool
	// RunAsync enqueues retain_document jobs and returns operation ids instead of waiting for units.
	RunAsync bool
}

// RetainItem mirrors memory.v1.RetainItem: one document to retain.
type RetainItem struct {
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
	// Tags are the item's tags (merged with RetainRequest.DocumentTags).
	Tags []string
	// ObservationScopeModes picks the item's scope fan-out; nil = the bank's default.
	ObservationScopeModes *domain.ObservationScopeModes
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
	// Revision is the client's version of the content; nil = unversioned. A revision lower than the
	// document's stored one makes the item stale: nothing is written for it.
	Revision *int64
}

// RetainResult mirrors memory.v1.RetainResponse.
type RetainResult struct {
	// Documents has one entry per item, in item order: sync = filled; async = document ids, unchanged flags and
	// operations only.
	Documents []RetainedDocument
	// BatchOperation is the retain_batch parent, set when an async retain enqueues more than one document.
	BatchOperation *domain.Operation
	// Usage is the per-slot token usage of a sync retain's model calls (RetainResponse.usage); empty for
	// async (each job records its own).
	Usage []domain.Usage
}

// RetainedDocument mirrors memory.v1.RetainedDocument: the outcome for one item.
type RetainedDocument struct {
	// DocumentID is the item's document (generated when the item left it empty).
	DocumentID domain.DocumentID
	// MemoriesCreated counts the units inserted (sync only).
	MemoriesCreated int
	// Unchanged reports the same content + overrides hash; nothing re-extracted.
	Unchanged bool
	// Stale reports a revision lower than the document's stored one; nothing written.
	Stale bool
	// Operation is the retain_document operation (async, changed document), or nil for an inline run.
	Operation *domain.Operation
}

// ingestorDeps are the ingestor's dependencies.
type ingestorDeps struct {
	// Store opens the transactions.
	Store store.DB
	// Blob stores the original text.
	Blob blob.Store
	// Config resolves the bank's effective config.
	Config *config.Resolver
	// Retainer runs a sync retain inline.
	Retainer *retainer
	// Clock is "now" for items without a timestamp.
	Clock clock.Clock
	// NewID generates document ids; nil = uuid.NewString.
	NewID func() string
	// Limits are the request limits and the backlog gate (zero fields = the defaults).
	Limits IngestLimits
}

// ingestor is the API side of Retain: validation, blobs and document rows, then the inline run or the
// retain_document jobs.
type ingestor struct {
	d ingestorDeps
}

// newIngestor returns the ingestor.
func newIngestor(d ingestorDeps) *ingestor {
	if d.NewID == nil {
		d.NewID = uuid.NewString
	}
	d.Limits = d.Limits.withDefaults()
	return &ingestor{d: d}
}

// outcome is what the no-op and revision checks decided for an item.
type outcome int

// Item outcomes.
const (
	// outcomeWrite: the item changes its document; it is written.
	outcomeWrite outcome = iota
	// outcomeUnchanged: same content and no-op hash as the document's last completed retain; nothing written.
	outcomeUnchanged
	// outcomeStale: the item's revision is lower than the document's stored one; nothing written.
	outcomeStale
)

// itemPlan is one validated item: its document, payload and hashes, and what the checks decided for it.
type itemPlan struct {
	doc       domain.DocumentID
	content   string
	overrides *domain.BankConfig
	oh        string
	payload   jobs.RetainPayload
	revision  *int64
	pre       outcome // decided by the pre-read; final unless outcomeWrite
	out       outcome // decided again under the write transaction's Seq lock (starts as pre)
}

// skip reports whether nothing is written for the item.
func (p *itemPlan) skip() bool { return p.out != outcomeWrite }

// Ingest validates (scope fan-out, overrides), stores blobs, then runs inline or enqueues. An ALL_COMBINATIONS
// fan-out over max_combinations fails with domain.ErrTooManyScopes before anything is stored. An item whose
// content and overrides hash match its document's last completed retain is a no-op (Unchanged) unless
// ForceReextract is set, and one whose revision is lower than the document's is Stale: nothing is written for
// either and no model is called. Both checks run before the blob Put and again inside the write transaction.
func (i *ingestor) Ingest(ctx context.Context, req RetainRequest) (RetainResult, error) {
	if req.Bank.IsZero() {
		return RetainResult{}, fmt.Errorf("%w: retain needs a bank", domain.ErrInvalidArgument)
	}
	if len(req.Items) == 0 {
		return RetainResult{}, fmt.Errorf("%w: retain needs at least one item", domain.ErrInvalidArgument)
	}
	if n := i.d.Limits.MaxRetainItems; len(req.Items) > n {
		return RetainResult{}, domain.InvalidField("items",
			"retain takes at most %d items (ingest.max_retain_items); use RetainBatch for bulk ingestion", n)
	}
	base, known, err := i.bankConfig(ctx, req.Bank)
	if err != nil {
		return RetainResult{}, err
	}
	plans := make([]*itemPlan, len(req.Items))
	seen := map[domain.DocumentID]bool{}
	for k, it := range req.Items {
		if n := i.d.Limits.MaxDocumentBytes; len(it.Content) > n {
			return RetainResult{}, domain.InvalidField(fmt.Sprintf("items[%d].content", k),
				"content is %d bytes, over %d (ingest.max_document_bytes)", len(it.Content), n)
		}
		if plans[k], err = i.plan(ctx, req, it, base, known); err != nil {
			return RetainResult{}, fmt.Errorf("item %d: %w", k, err)
		}
		if seen[plans[k].doc] {
			return RetainResult{}, fmt.Errorf("%w: item %d: document %q appears twice", domain.ErrInvalidArgument, k,
				plans[k].doc)
		}
		seen[plans[k].doc] = true
	}
	if err := i.preRead(ctx, req.Bank, plans); err != nil {
		return RetainResult{}, err
	}
	if err := i.storeBlobs(ctx, plans); err != nil {
		return RetainResult{}, err
	}
	res, err := i.write(ctx, req, plans)
	if err != nil {
		i.dropBlobs(ctx, plans, err)
		return res, err
	}
	if req.RunAsync {
		return res, nil
	}
	return i.runInline(ctx, req.Bank, plans, res)
}

// bankConfig returns the bank's effective config, and whether the bank exists. An unknown bank is not created
// here (validation must store nothing); its config for validation is tenant < org, and the write transaction
// creates it. The server level only adds defaults that cannot fail validation. Orgs are never created on first
// use: an unknown org is domain.ErrOrgNotFound, and one being deleted domain.ErrOrgDeleting.
func (i *ingestor) bankConfig(ctx context.Context, b domain.BankRef) (domain.BankConfig, bool, error) {
	cfg, err := i.d.Config.Effective(ctx, b)
	if err == nil {
		return cfg, true, nil
	}
	if !errors.Is(err, domain.ErrBankNotFound) {
		return cfg, false, fmt.Errorf("retain: %w", i.deletingOrg(ctx, b.OrgRef(), err))
	}
	var tenant, org domain.BankConfig
	err = i.d.Store.InTx(ctx, func(tx store.Tx) error {
		var err error
		tenant, org, err = config.StoredOrgLevels(ctx, tx, b.OrgRef())
		return err //nolint:wrapcheck // wrapped once below, after the transaction
	})
	if err != nil {
		return domain.BankConfig{}, false, fmt.Errorf("retain: %w", i.deletingOrg(ctx, b.OrgRef(), err))
	}
	return config.Merge(tenant, org), false, nil
}

// deletingOrg turns err, the domain.ErrOrgNotFound a config read returns for an org that is being deleted, into
// domain.ErrOrgDeleting: a write under such an org is refused as deleting, as Ensure refuses it. Any other error,
// and the not-found of an org that does not exist, is returned unchanged.
func (i *ingestor) deletingOrg(ctx context.Context, o domain.OrgRef, err error) error {
	if !errors.Is(err, domain.ErrOrgNotFound) {
		return err
	}
	var deleting bool
	if rerr := i.d.Store.InTx(ctx, func(tx store.Tx) error {
		org, err := tx.Orgs().Get(ctx, o)
		deleting = err == nil && org.Deleting()
		if errors.Is(err, domain.ErrOrgNotFound) {
			return nil
		}
		return err
	}); rerr != nil {
		return errors.Join(err, rerr)
	}
	if deleting {
		return fmt.Errorf("%w: org %s", domain.ErrOrgDeleting, o)
	}
	return err
}

// plan validates one item and builds its document payload. Nothing is stored.
func (i *ingestor) plan(
	ctx context.Context, req RetainRequest, it RetainItem, base domain.BankConfig, known bool,
) (*itemPlan, error) {
	overrides := mergeOverrides(req.Overrides, it.Overrides)
	cfg, err := i.itemConfig(ctx, req.Bank, base, known, overrides)
	if err != nil {
		return nil, err
	}
	if r := cfg.GetRetain(); r.ExtractionMode == domain.ExtractionModeCustom &&
		strings.TrimSpace(r.CustomExtractionPrompt) == "" {
		return nil, fmt.Errorf("%w: extraction mode custom needs custom_extraction_prompt", domain.ErrInvalidArgument)
	}
	tags := domain.NormalizeTags(append(slices.Clone(it.Tags), req.DocumentTags...))
	if err := i.checkFanout(req.Bank, tags, scopeMode(it.ObservationScopeModes, cfg)); err != nil {
		return nil, err
	}
	oh, err := noopHash(overrides, it.ObservationScopeModes)
	if err != nil {
		return nil, err
	}
	doc := it.DocumentID
	if doc == "" {
		doc = domain.DocumentID(i.d.NewID())
	}
	p := jobs.RetainPayload{
		DocumentID: doc, ContentHash: contentHash(it.Content), Tags: tags, Context: it.Context,
		Timeless: it.Timeless, Metadata: it.Metadata, Entities: payloadHints(it.Entities),
		ResolveEntities: it.ResolveEntities, ObservationScopeModes: it.ObservationScopeModes,
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
	return &itemPlan{doc: doc, content: it.Content, overrides: overrides, oh: oh, payload: p, revision: it.Revision}, nil
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
	}
	return config.Merge(base, *overrides), nil
}

// mergeOverrides merges request < item overrides; nil when neither is set.
func mergeOverrides(req, item *domain.BankConfig) *domain.BankConfig {
	switch {
	case req == nil && item == nil:
		return nil
	case req == nil:
		o := *item
		return &o
	case item == nil:
		o := *req
		return &o
	}
	o := config.Merge(*req, *item)
	return &o
}

// checkFanout rejects a scope fan-out over its cap (domain.ExpandScopes).
func (i *ingestor) checkFanout(b domain.BankRef, tags []string, s domain.ObservationScopeModes) error {
	if _, err := domain.ExpandScopes(b, tags, s); err != nil {
		return fmt.Errorf("retain: observation scopes: %w", err)
	}
	return nil
}
