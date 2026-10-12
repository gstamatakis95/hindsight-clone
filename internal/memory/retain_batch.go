package memory

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/config"
	"memd/internal/domain"
)

// RetainBatchRequest mirrors memory.v1.RetainBatchRequest with domain types.
type RetainBatchRequest struct {
	// Bank receives the items; an unknown bank of an existing org is created (an unknown org is
	// domain.ErrOrgNotFound).
	Bank domain.BankRef
	// Items are the documents to retain; every item needs a DocumentID.
	Items []RetainItem
	// Rejected, when set, has one entry per item: a non-nil entry is the transport's decode error for that
	// item, which is then REJECTED with it and otherwise ignored.
	Rejected []error
	// DocumentTags are added to every item's tags.
	DocumentTags []string
	// Overrides apply to every item, below each item's own overrides; nil = none.
	Overrides *domain.BankConfig
	// SkipConsolidation suppresses the automatic consolidate job after each retain.
	SkipConsolidation bool
}

// BatchStatus is what happened to one RetainBatch item (memory.v1.RetainBatchResult.Status).
type BatchStatus int

// Batch item statuses.
const (
	// BatchUnspecified is the zero value: the item has no outcome yet.
	BatchUnspecified BatchStatus = iota
	// BatchAccepted: committed; Operation is the item's new retain_document or the identical one it joined.
	BatchAccepted
	// BatchUnchanged: same content and no-op hash as the document's last completed retain; nothing written.
	BatchUnchanged
	// BatchStale: the item's revision is lower than the document's (or a same-document item's); nothing written.
	BatchStale
	// BatchSuperseded: a later item of the call has the same document id; nothing written.
	BatchSuperseded
	// BatchRejected: the item is invalid (Err); retrying it unchanged fails the same way.
	BatchRejected
	// BatchFailed: nothing was committed for the item (Err); retry it with backoff.
	BatchFailed
)

// BatchItemResult is the outcome of one RetainBatch item.
type BatchItemResult struct {
	// DocumentID is the item's document.
	DocumentID domain.DocumentID
	// Status says what happened to the item.
	Status BatchStatus
	// Operation is the retain_document operation of an accepted item; nil otherwise.
	Operation *domain.Operation
	// Err says why the item was rejected or failed; nil otherwise. Its field violations name fields of the item
	// (document_id, content, or a BankConfig path of its overrides).
	Err error
}

// RetainBatchResult mirrors memory.v1.RetainBatchResponse.
type RetainBatchResult struct {
	// Items has one result per request item, in request order.
	Items []BatchItemResult
}

// batchWriteItems is how many items one RetainBatch write transaction takes: each takes the bank's Seq lock, so
// a large batch holds it several times briefly rather than once for long.
const batchWriteItems = 100

// batchItem is one RetainBatch item on its way through the batch path.
type batchItem struct {
	res  BatchItemResult
	plan *itemPlan // nil once rejected before planning
	put  bool      // this call stored the item's blob
}

// pending reports whether the item still has no outcome.
func (it *batchItem) pending() bool { return it.res.Status == BatchUnspecified }

// settle gives the item its outcome.
func (it *batchItem) settle(s BatchStatus, err error) { it.res.Status, it.res.Err = s, err }

// IngestBatch is RetainBatch, the path every bulk producer shares (the RPC, and the queue consumer). It
// validates the call (domain.ErrInvalidArgument), applies the backlog gate (domain.ErrIngestBacklog with a retry
// delay) and resolves the bank's config (domain.ErrOrgNotFound, ...) before anything is written; a call-level
// error means nothing was written. Then every item gets a status: invalid items are rejected, same-document
// items deduplicated (the highest revision wins, the last among equals), unchanged and stale items found by a
// pre-read, blobs stored, and the rest written in transactions of up to 100 items, each re-checking its items
// under the bank's Seq lock and joining an identical retain already in flight instead of enqueuing another.
func (i *ingestor) IngestBatch(ctx context.Context, req RetainBatchRequest) (RetainBatchResult, error) {
	if err := i.checkBatch(req); err != nil {
		return RetainBatchResult{}, err
	}
	if err := i.checkBacklog(ctx, req.Bank); err != nil {
		return RetainBatchResult{}, err
	}
	base, known, err := i.bankConfig(ctx, req.Bank)
	if err != nil {
		return RetainBatchResult{}, err
	}
	items := i.planBatch(ctx, req, base, known)
	dedupe(items)
	if err := i.preReadBatch(ctx, req.Bank, items); err != nil {
		return RetainBatchResult{}, err
	}
	i.putBlobs(ctx, items)
	i.writeBatch(ctx, req.Bank, items)
	i.compensate(ctx, items)
	out := RetainBatchResult{Items: make([]BatchItemResult, len(items))}
	for k, it := range items {
		out.Items[k] = it.res
	}
	return out, nil
}

// checkBatch validates the call as a whole: a bank, 1 to MaxBatchItems items, at most MaxRequestBytes of content.
func (i *ingestor) checkBatch(req RetainBatchRequest) error {
	l := i.d.Limits
	total := 0
	for _, it := range req.Items {
		total += len(it.Content)
	}
	switch {
	case req.Bank.IsZero():
		return fmt.Errorf("%w: retain batch needs a bank", domain.ErrInvalidArgument)
	case len(req.Items) == 0:
		return domain.InvalidField("items", "retain batch needs at least one item")
	case len(req.Items) > l.MaxBatchItems:
		return domain.InvalidField("items", "retain batch takes at most %d items (ingest.max_batch_items), got %d",
			l.MaxBatchItems, len(req.Items))
	case total > l.MaxRequestBytes:
		return domain.InvalidField("items", "retain batch content is %d bytes, over %d (ingest.max_request_bytes)",
			total, l.MaxRequestBytes)
	case req.Rejected != nil && len(req.Rejected) != len(req.Items):
		return fmt.Errorf("%w: retain batch: %d decode results for %d items", domain.ErrInvalidArgument,
			len(req.Rejected), len(req.Items))
	}
	return nil
}

// planBatch checks and plans every item; an invalid one is rejected (a transient failure, such as a config read,
// fails it).
func (i *ingestor) planBatch(
	ctx context.Context, req RetainBatchRequest, base domain.BankConfig, known bool,
) []*batchItem {
	asRetain := RetainRequest{
		Bank: req.Bank, DocumentTags: req.DocumentTags, Overrides: req.Overrides,
		SkipConsolidation: req.SkipConsolidation, RunAsync: true,
	}
	items := make([]*batchItem, len(req.Items))
	for k, it := range req.Items {
		items[k] = &batchItem{res: BatchItemResult{DocumentID: it.DocumentID}}
		var err error
		if req.Rejected != nil {
			err = req.Rejected[k]
		}
		if err == nil {
			err = i.checkItem(it)
		}
		if err == nil {
			items[k].plan, err = i.plan(ctx, asRetain, it, base, known)
		}
		if err != nil {
			items[k].settle(failStatus(err), err)
		}
	}
	return items
}

// checkItem validates what RetainBatch requires of an item beyond what Retain does: a document id, a content
// size within MaxDocumentBytes, and valid item overrides.
func (i *ingestor) checkItem(it RetainItem) error {
	switch {
	case it.DocumentID == "":
		return domain.InvalidField("document_id", "retain batch needs a document_id on every item")
	case len(it.Content) > i.d.Limits.MaxDocumentBytes:
		return domain.InvalidField("content", "content is %d bytes, over %d (ingest.max_document_bytes)",
			len(it.Content), i.d.Limits.MaxDocumentBytes)
	case it.Overrides != nil:
		return config.Validate(*it.Overrides) //nolint:wrapcheck // config errors are already domain error classes
	}
	return nil
}

// failStatus is Rejected for a permanent error (invalid argument, failed precondition, not found) and Failed
// for anything else (a dependency, a conflict, a cancelled context), which a retry may fix.
func failStatus(err error) BatchStatus {
	if errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, domain.ErrFailedPrecondition) ||
		errors.Is(err, domain.ErrNotFound) {
		return BatchRejected
	}
	return BatchFailed
}

// dedupe leaves one pending item per document: the one with the highest revision, the last one among equals (or
// when revisions are unset). Earlier items it replaces are superseded; a later item with a lower revision is stale.
func dedupe(items []*batchItem) {
	best := map[domain.DocumentID]*batchItem{}
	for _, it := range items {
		if !it.pending() {
			continue
		}
		cur, ok := best[it.plan.doc]
		switch {
		case !ok:
		case lowerRevision(it.plan.revision, cur.plan.revision):
			it.settle(BatchStale, nil)
			continue
		default:
			cur.settle(BatchSuperseded, nil)
		}
		best[it.plan.doc] = it
	}
}

// lowerRevision reports whether revision a is set and lower than a set revision b.
func lowerRevision(a, b *int64) bool { return a != nil && b != nil && *a < *b }

// pendingItems returns the items that still have no outcome.
func pendingItems(items []*batchItem) []*batchItem {
	var out []*batchItem
	for _, it := range items {
		if it.pending() {
			out = append(out, it)
		}
	}
	return out
}

// preReadBatch runs the pre-read (preRead: bank tombstone, no-op and revision checks) over the pending items
// and settles the unchanged and stale ones. A bank being deleted fails the call before any blob is stored.
func (i *ingestor) preReadBatch(ctx context.Context, b domain.BankRef, items []*batchItem) error {
	pending := pendingItems(items)
	if len(pending) == 0 {
		return nil
	}
	plans := make([]*itemPlan, len(pending))
	for k, it := range pending {
		plans[k] = it.plan
	}
	if err := i.preRead(ctx, b, plans); err != nil {
		return err
	}
	for _, it := range pending {
		switch it.plan.pre {
		case outcomeUnchanged:
			it.settle(BatchUnchanged, nil)
		case outcomeStale:
			it.settle(BatchStale, nil)
		case outcomeWrite:
		}
	}
	return nil
}
