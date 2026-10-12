package memory

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"

	"golang.org/x/sync/errgroup"
)

// blobPutParallelism bounds the concurrent blob Puts of one RetainBatch call.
const blobPutParallelism = 8

// putBlobs stores the original text of every pending item, up to blobPutParallelism at a time, outside any
// transaction. An item whose Put fails is failed; the others go on.
func (i *ingestor) putBlobs(ctx context.Context, items []*batchItem) {
	var g errgroup.Group
	g.SetLimit(blobPutParallelism)
	for _, it := range pendingItems(items) {
		g.Go(func() error {
			if err := i.putBlob(ctx, it.plan); err != nil {
				it.settle(BatchFailed, fmt.Errorf("store blob: %w", err))
				return nil
			}
			it.put = true
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil; failures are recorded on their items
}

// writeBatch writes the pending items in transactions of up to batchWriteItems. When a transaction fails with an
// invalid argument, its items are written again one per transaction, so the invalid one is rejected alone; any
// other failure fails the transaction's items (nothing of them was committed).
func (i *ingestor) writeBatch(ctx context.Context, b domain.BankRef, items []*batchItem) {
	pending := pendingItems(items)
	for start := 0; start < len(pending); start += batchWriteItems {
		chunk := pending[start:min(start+batchWriteItems, len(pending))]
		err := i.writeChunk(ctx, b, chunk)
		switch {
		case err == nil:
		case errors.Is(err, domain.ErrInvalidArgument) && len(chunk) > 1:
			for _, it := range chunk {
				if err := i.writeChunk(ctx, b, []*batchItem{it}); err != nil {
					it.settle(failStatus(err), err)
				}
			}
		default:
			for _, it := range chunk {
				it.settle(failStatus(err), err)
			}
		}
	}
}

// writeChunk writes items in one transaction: Banks().Ensure, tx.Seq (before any Enqueue), then each item
// (writeItem). Every outcome captured in the closure is reset at its top, since InTx may run it again. A
// transaction that ends up writing nothing (every item unchanged, stale or joined) rolls back, so it bumps no
// change seq and creates no bank.
func (i *ingestor) writeChunk(ctx context.Context, b domain.BankRef, items []*batchItem) error {
	err := i.d.Store.InTx(ctx, func(tx store.Tx) error {
		for _, it := range items {
			it.res.Status, it.res.Operation, it.res.Err = BatchUnspecified, nil, nil
			it.plan.out = it.plan.pre
		}
		if _, err := tx.Banks().Ensure(ctx, b); err != nil { // a write to an unknown bank creates it
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil { // Seq before Enqueue (lock order with ReclaimStale)
			return err
		}
		wrote := false
		for _, it := range items {
			w, err := i.writeItem(ctx, tx, b, it)
			if err != nil {
				return fmt.Errorf("document %s: %w", it.plan.doc, err)
			}
			wrote = wrote || w
		}
		if !wrote {
			return errNothingWritten
		}
		return nil
	})
	if errors.Is(err, errNothingWritten) {
		return nil
	}
	return err
}

// writeItem re-checks one item under the Seq lock (recheck), then joins the identical retain in flight or
// upserts the document and enqueues its retain_document. It reports whether it wrote anything.
func (i *ingestor) writeItem(ctx context.Context, tx store.Tx, b domain.BankRef, it *batchItem) (bool, error) {
	p := it.plan
	d, found, err := recheck(ctx, tx, b, p)
	if err != nil {
		return false, err
	}
	switch p.out {
	case outcomeUnchanged:
		it.settle(BatchUnchanged, nil)
		return false, nil
	case outcomeStale:
		it.settle(BatchStale, nil)
		return false, nil
	case outcomeWrite:
	}
	op, err := joinable(ctx, tx, b, d, found, p)
	if err != nil {
		return false, err
	}
	wrote := false
	if op == nil {
		if err := upsert(ctx, tx, b, p); err != nil {
			return false, err
		}
		id, _, err := jobs.EnqueueRetainDocument(ctx, tx, b, p.payload, p.overrides, "")
		if err != nil {
			return false, err
		}
		if op, err = readOperation(ctx, tx, b, id); err != nil {
			return false, err
		}
		wrote = true
	}
	it.settle(BatchAccepted, nil)
	it.res.Operation = op
	return wrote, nil
}

// joinable returns the retain already in flight that the item would only repeat, or nil. The document must be in
// flight with exactly what the item would write (blob key, so content; overrides and observation scopes, so the
// no-op hash; tags; a revision the item would not raise), and the newest active retain_document on the
// document's serial key must carry the same blob key and no-op hash: that job owns the document and will
// extract this content. A document in flight because its last run failed has no active job, so the item is
// enqueued afresh.
func joinable(
	ctx context.Context, tx store.Tx, b domain.BankRef, d domain.Document, found bool, p *itemPlan,
) (*domain.Operation, error) {
	if same, err := inFlightAs(d, found, p); err != nil || !same {
		return nil, err
	}
	active, err := tx.Queue().ActiveBySerialKey(ctx, b, domain.OperationKindRetainDocument,
		jobs.DocumentSerialKey(p.doc))
	if err != nil || len(active) == 0 {
		return nil, err
	}
	j := active[0]
	jp, err := decodePayload(j.Payload)
	if err != nil {
		return nil, nil //nolint:nilerr // an undecodable job is never joined; the item is enqueued afresh
	}
	if oh, err := noopHash(j.Overrides, jp.ObservationScopeModes); err != nil || oh != p.oh ||
		jp.BlobKey != p.payload.BlobKey {
		return nil, err
	}
	return readOperation(ctx, tx, b, j.ID)
}

// inFlightAs reports whether the document is in flight with exactly what the plan would write: blob key, tags,
// no-op hash, and a revision the plan would not raise.
func inFlightAs(d domain.Document, found bool, p *itemPlan) (bool, error) {
	if !found || d.ExtractedAt != nil || d.BlobKey != p.payload.BlobKey || !domain.SameTagSet(d.Tags, p.payload.Tags) ||
		(p.revision != nil && (d.Revision == nil || *p.revision > *d.Revision)) {
		return false, nil
	}
	oh, err := noopHash(d.Overrides, d.ObservationScopeModes)
	return err == nil && oh == p.oh, err
}

// compensate deletes the blobs this call stored for items whose write failed because their org or bank is gone
// or going (org not found, org or bank being deleted), so the call leaves nothing under a deleted prefix. Only
// those errors: for any other failure the content-addressed key may be the blob of a retain already in flight.
// A failed delete leaves the blob for the bank or org deletion's own sweep.
func (i *ingestor) compensate(ctx context.Context, items []*batchItem) {
	for _, it := range items {
		if !it.put || it.res.Status == BatchAccepted || !orgOrBankGone(it.res.Err) {
			continue
		}
		_, _ = i.d.Blob.DeletePrefix(ctx, it.plan.payload.BlobKey)
	}
}

// orgOrBankGone reports whether err says the item's org or bank no longer takes writes.
func orgOrBankGone(err error) bool {
	return errors.Is(err, domain.ErrOrgNotFound) || errors.Is(err, domain.ErrOrgDeleting) ||
		errors.Is(err, domain.ErrBankDeleting)
}
