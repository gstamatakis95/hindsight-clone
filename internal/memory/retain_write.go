package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"strings"
)

// errNothingWritten rolls back a write transaction whose re-check left nothing to write, so the call bumps no
// change seq and creates no bank; the caller treats it as success.
var errNothingWritten = errors.New("retain: nothing to write")

// classify applies the stale and no-op rules to a plan against its document as read (found = false when the
// document does not exist yet): a revision lower than the stored one is stale; the same content and no-op hash
// as the last completed retain is unchanged, unless force_reextract is set.
func classify(d domain.Document, found bool, p *itemPlan) outcome {
	switch {
	case !found:
		return outcomeWrite
	case p.revision != nil && d.Revision != nil && *p.revision < *d.Revision:
		return outcomeStale
	case !p.payload.ForceReextract && d.ExtractedAt != nil && d.ContentHash == p.payload.ContentHash &&
		d.OverridesHash == p.oh:
		return outcomeUnchanged
	default:
		return outcomeWrite
	}
}

// readDocument reads document id of b in tx; found is false when it does not exist.
func readDocument(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.DocumentID,
) (domain.Document, bool, error) {
	d, err := tx.Documents().Get(ctx, b, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Document{}, false, nil
	case err != nil:
		return domain.Document{}, false, err
	}
	return d, true, nil
}

// preRead classifies every plan (classify) before anything is written: Upsert would clear extracted_at, and an
// unchanged or stale item must not even Put its blob. It also rejects an org being deleted (domain.ErrOrgDeleting),
// an unknown org (domain.ErrOrgNotFound) and a bank being deleted (domain.ErrBankDeleting) before the blob Put, so a
// retain that loses the race with DeleteOrg or DeleteBank stores no blob; an unknown bank is fine (the write
// creates it). A retain that passes this check just before the tombstone deletes its blobs again when its write is
// refused (dropBlobs, compensate). An item it finds unchanged or stale stays so; one it
// finds changed is checked again under the write transaction's Seq lock (recheck).
func (i *ingestor) preRead(ctx context.Context, b domain.BankRef, plans []*itemPlan) error {
	return i.d.Store.InTx(ctx, func(tx store.Tx) error {
		org, err := tx.Orgs().Get(ctx, b.OrgRef())
		switch {
		case err != nil:
			return err
		case org.Deleting():
			return fmt.Errorf("%w: org %s", domain.ErrOrgDeleting, b.OrgRef())
		}
		bank, err := tx.Banks().Get(ctx, b)
		switch {
		case err == nil && bank.Deleting():
			return fmt.Errorf("%w: bank %s", domain.ErrBankDeleting, b)
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			return err
		}
		for _, p := range plans {
			d, found, err := readDocument(ctx, tx, b, p.doc)
			if err != nil {
				return err
			}
			p.pre = classify(d, found, p)
			p.out = p.pre
		}
		return nil
	})
}

// recheck classifies a plan the pre-read found changed again, inside the write transaction after tx.Seq (the
// bank row lock serializes it with every other retain of the bank): a retain that completed or a newer
// revision that landed since the pre-read turns it unchanged or stale, so the upsert never clears a fresh
// extracted_at (which would force a paid re-extraction) and never overwrites a newer revision. It returns the
// document as read.
func recheck(ctx context.Context, tx store.Tx, b domain.BankRef, p *itemPlan) (domain.Document, bool, error) {
	d, found, err := readDocument(ctx, tx, b, p.doc)
	if err != nil {
		return d, found, err
	}
	p.out = classify(d, found, p)
	return d, found, nil
}

// upsert writes the plan's document row (clearing extracted_at; a nil revision keeps the stored one).
func upsert(ctx context.Context, tx store.Tx, b domain.BankRef, p *itemPlan) error {
	return tx.Documents().Upsert(ctx, domain.Document{
		ID: p.doc, Bank: b, Overrides: p.overrides, Tags: p.payload.Tags, BlobKey: p.payload.BlobKey,
		ObservationScopeModes: p.payload.ObservationScopeModes, Revision: p.revision,
	})
}

// storeBlobs writes each changed item's original text under its content-addressed key, before any
// transaction (a repeated Put of the same key is harmless).
func (i *ingestor) storeBlobs(ctx context.Context, plans []*itemPlan) error {
	for _, p := range plans {
		if p.skip() {
			continue
		}
		if err := i.putBlob(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// putBlob stores one plan's original text.
func (i *ingestor) putBlob(ctx context.Context, p *itemPlan) error {
	return i.d.Blob.Put(ctx, p.payload.BlobKey, strings.NewReader(p.content), int64(len(p.content)),
		"text/plain; charset=utf-8")
}

// dropBlobs deletes the blobs storeBlobs wrote for plans after their write failed with err because the org or bank
// no longer takes writes (orgOrBankGone): the delete may already have swept the prefix, and nothing will reference
// them. Other failures keep the blobs (a retry writes the same content-addressed keys). It runs even when ctx is
// done; a failed delete is left to the org or bank delete's own sweep.
func (i *ingestor) dropBlobs(ctx context.Context, plans []*itemPlan, err error) {
	if !orgOrBankGone(err) {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, p := range plans {
		if !p.skip() {
			_, _ = i.d.Blob.DeletePrefix(ctx, p.payload.BlobKey)
		}
	}
}

// write re-checks and upserts the changed documents and, for an async retain, enqueues their retain_document
// jobs (under a retain_batch parent when there are several) and reads the enqueued operations back, in one
// transaction. When the re-check leaves nothing to write the transaction rolls back.
func (i *ingestor) write(ctx context.Context, req RetainRequest, plans []*itemPlan) (RetainResult, error) {
	var res RetainResult
	var ops []*domain.Operation
	err := i.d.Store.InTx(ctx, func(tx store.Tx) error {
		res, ops = RetainResult{}, make([]*domain.Operation, len(plans))
		for _, p := range plans {
			p.out = p.pre
		}
		if !anyWrite(plans) {
			return errNothingWritten
		}
		if _, err := tx.Banks().Ensure(ctx, req.Bank); err != nil { // a write to an unknown bank creates it
			return err
		}
		if _, err := tx.Seq(ctx, req.Bank); err != nil { // Seq before Enqueue (lock order with ReclaimStale)
			return err
		}
		if err := writeDocuments(ctx, tx, req.Bank, plans); err != nil {
			return err
		}
		if !anyWrite(plans) {
			return errNothingWritten
		}
		if !req.RunAsync {
			return nil
		}
		var err error
		res.BatchOperation, err = i.enqueue(ctx, tx, req.Bank, plans, ops)
		return err
	})
	if err != nil && !errors.Is(err, errNothingWritten) {
		return RetainResult{}, err
	}
	for k, p := range plans {
		res.Documents = append(res.Documents, RetainedDocument{
			DocumentID: p.doc, Unchanged: p.out == outcomeUnchanged, Stale: p.out == outcomeStale, Operation: ops[k],
		})
	}
	return res, nil
}

// writeDocuments re-checks each changed plan under the Seq lock and upserts the ones still changed.
func writeDocuments(ctx context.Context, tx store.Tx, b domain.BankRef, plans []*itemPlan) error {
	for _, p := range plans {
		if p.skip() {
			continue
		}
		if _, _, err := recheck(ctx, tx, b, p); err != nil || p.skip() {
			if err != nil {
				return err
			}
			continue
		}
		if err := upsert(ctx, tx, b, p); err != nil {
			return err
		}
	}
	return nil
}

// anyWrite reports whether any plan is still to be written.
func anyWrite(plans []*itemPlan) bool {
	for _, p := range plans {
		if !p.skip() {
			return true
		}
	}
	return false
}

// enqueue inserts the retain_document jobs of the changed plans, under a retain_batch parent when there are
// several, reads each operation back into ops and returns the parent (nil when there is none).
func (i *ingestor) enqueue(
	ctx context.Context, tx store.Tx, b domain.BankRef, plans []*itemPlan, ops []*domain.Operation,
) (*domain.Operation, error) {
	changed := 0
	for _, p := range plans {
		if !p.skip() {
			changed++
		}
	}
	var parentID domain.OperationID
	if changed > 1 {
		id, _, err := jobs.EnqueueRetainBatch(ctx, tx, b, changed)
		if err != nil {
			return nil, err
		}
		parentID = id
	}
	for k, p := range plans {
		if p.skip() {
			continue
		}
		id, _, err := jobs.EnqueueRetainDocument(ctx, tx, b, p.payload, p.overrides, parentID)
		if err != nil {
			return nil, err
		}
		if ops[k], err = readOperation(ctx, tx, b, id); err != nil {
			return nil, err
		}
	}
	if parentID == "" {
		return nil, nil
	}
	return readOperation(ctx, tx, b, parentID)
}

// readOperation reads operation id of bank b in tx.
func readOperation(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.OperationID,
) (*domain.Operation, error) {
	op, err := tx.Queue().Get(ctx, b, id)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// runInline runs the Retainer for each changed document of a sync retain, in item order.
func (i *ingestor) runInline(
	ctx context.Context, b domain.BankRef, plans []*itemPlan, res RetainResult,
) (RetainResult, error) {
	var usage []domain.Usage
	for k, p := range plans {
		if p.skip() {
			continue
		}
		payload, err := json.Marshal(p.payload)
		if err != nil {
			return RetainResult{}, fmt.Errorf("retain: encode payload: %w", err)
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
			return RetainResult{}, fmt.Errorf("retain document %s: %w", p.doc, err)
		}
		res.Documents[k].MemoriesCreated = out.MemoriesCreated
		res.Documents[k].Unchanged = out.Unchanged
	}
	res.Usage = sumUsage(usage)
	return res, nil
}
