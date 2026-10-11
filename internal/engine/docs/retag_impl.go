package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
)

// Serial keys and payloads of the jobs the retagger enqueues (decision 1301).
const (
	// docSerialPrefix prefixes a document's serial key: "doc:{document_id}".
	docSerialPrefix = "doc:"
	// consolidateSerialKey is the bank's single consolidation key.
	consolidateSerialKey = "consolidate"
	// graphSerialKey is the bank's single graph-maintenance key.
	graphSerialKey = "graph"
)

// retagger implements Retagger over the ports.
type retagger struct {
	s store.DB
}

var _ Retagger = (*retagger)(nil)

// NewRetagger returns the Retagger: Retag is UpdateDocument's API side, Apply the retag_document job. It
// uses the ports directly (UnitRepo.SetDocumentTags, EvidenceRepo.DeleteCiting), see decision 1307.
func NewRetagger(s store.DB) Retagger {
	return &retagger{s: s}
}

// Retag commits doc's new tag set and enqueues retag_document, or does nothing when the set is unchanged.
// An unchanged set is a no-op only while the document's newest retag_document operation (if any) did not
// end FAILED or CANCELLED; otherwise the observations derived under the old tags were never cleared, so
// the job is enqueued again. The common no-op path reads only: it bumps no change seq. A real change takes
// the bank's change seq (which serializes concurrent retags of the bank), re-reads the tags under that lock
// and only then writes: the document's tags and, in the same transaction, its units' document-derived tags
// (old to new; their label tags stay, decision 1900).
func (r *retagger) Retag(
	ctx context.Context, b domain.BankRef, doc domain.DocumentID, tags []string,
) (domain.OperationID, error) {
	want := domain.NormalizeTags(tags)
	var id domain.OperationID
	err := r.s.InTx(ctx, func(tx store.Tx) error {
		id = ""
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.Deleting() {
			return fmt.Errorf("%w: retag in bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
		}
		d, err := tx.Documents().Get(ctx, b, doc)
		if err != nil {
			return err
		}
		if domain.SameTagSet(d.Tags, want) {
			id, err = retryFailedRetag(ctx, tx, b, doc)
			return err
		}
		seq, err := tx.Seq(ctx, b)
		if err != nil {
			return err
		}
		if d, err = tx.Documents().Get(ctx, b, doc); err != nil {
			return err
		}
		if domain.SameTagSet(d.Tags, want) {
			return nil // a concurrent retag set the same tags first
		}
		id, err = enqueueRetag(ctx, tx, b, doc, domain.NormalizeTags(d.Tags), want, seq)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// retryFailedRetag re-enqueues retag_document for doc (tags unchanged) when the document's newest retag
// operation ended FAILED or CANCELLED, and returns its id; otherwise it is the no-op and returns "".
func retryFailedRetag(
	ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID,
) (domain.OperationID, error) {
	failed, err := lastRetagFailed(ctx, tx, b, doc)
	if err != nil || !failed {
		return "", err
	}
	if _, err := tx.Seq(ctx, b); err != nil {
		return "", err
	}
	return enqueueRetagJob(ctx, tx, b, doc)
}

// lastRetagFailed reports whether doc's newest retag_document operation ended FAILED or CANCELLED (false
// when it has none). It pages the bank's retag operations newest first until it finds one of doc.
func lastRetagFailed(ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID) (bool, error) {
	key := docSerialPrefix + string(doc)
	f := store.OperationFilter{Kind: domain.OperationKindRetagDocument, PageSize: 100}
	for {
		ops, next, err := tx.Queue().List(ctx, b, f)
		if err != nil {
			return false, err
		}
		for _, op := range ops {
			if op.SerialKey == key {
				return op.Status == domain.OperationStatusFailed || op.Status == domain.OperationStatusCancelled, nil
			}
		}
		if next == "" {
			return false, nil
		}
		f.PageToken = next
	}
}

// enqueueRetag stores the document's new tags, replaces the document-derived tags of its units (oldTags,
// read under tx.Seq, become newTags; label tags stay) and enqueues (or joins) its retag_document job.
func enqueueRetag(
	ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID, oldTags, newTags []string, seq int64,
) (domain.OperationID, error) {
	if err := tx.Documents().SetTags(ctx, b, doc, newTags); err != nil {
		return "", err
	}
	if _, err := tx.Units().SetDocumentTags(ctx, b, doc, oldTags, newTags, seq); err != nil {
		return "", err
	}
	return enqueueRetagJob(ctx, tx, b, doc)
}

// enqueueRetagJob enqueues (or joins, DedupePending) doc's retag_document job.
func enqueueRetagJob(
	ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID,
) (domain.OperationID, error) {
	payload, err := json.Marshal(RetagPayload{DocumentID: doc})
	if err != nil {
		return "", fmt.Errorf("retag payload: %w", err)
	}
	id, _, err := tx.Queue().Enqueue(ctx, store.Job{
		Bank: b, Kind: domain.OperationKindRetagDocument, SerialKey: docSerialPrefix + string(doc),
		Payload: payload, Dedupe: store.DedupePending,
	})
	return id, err
}

// Apply runs retag_document: it re-asserts the document's current tags on its units (Retag already replaced
// them, keeping their label tags) and resets their consolidation stamps, deletes the observations citing
// them (resetting their co-sources) and enqueues consolidation, in one transaction, plus graph_maintenance
// when observations were deleted. A document that is gone is a
// no-op; a gone bank (domain.ErrNotFound) or a tombstoned one (domain.ErrBankDeleting) fails from Tx.Seq,
// and an error after ctx ended is domain.ErrCancelled. It never creates a bank.
func (r *retagger) Apply(ctx context.Context, j store.Job) error {
	var p RetagPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: retag_document payload %s: %w", domain.ErrInvalidArgument, j.Payload, err)
	}
	if p.DocumentID == "" {
		return fmt.Errorf("%w: retag_document payload %s has no document_id", domain.ErrInvalidArgument, j.Payload)
	}
	err := r.s.InTx(ctx, func(tx store.Tx) error {
		return applyRetag(ctx, tx, j.Bank, p.DocumentID)
	})
	if errors.Is(err, errDocumentGone) {
		return nil // the document is gone: nothing left to retag
	}
	if err != nil && (ctx.Err() != nil || errors.Is(err, domain.ErrNotFound)) {
		// Cancelled mid-transaction, or the bank is gone (Tx.Seq NOT_FOUND): stop, never recreate it.
		return fmt.Errorf("%w: retag_document %s: %w", domain.ErrCancelled, j.ID, err)
	}
	return err
}

// errDocumentGone marks a retag whose document no longer exists (Documents().Get was NOT_FOUND).
var errDocumentGone = errors.New("retag: document gone")

// applyRetag is the body of Apply's transaction.
func applyRetag(ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID) error {
	seq, err := tx.Seq(ctx, b)
	if err != nil {
		return err
	}
	d, err := tx.Documents().Get(ctx, b, doc)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: %w", errDocumentGone, err)
	}
	if err != nil {
		return err
	}
	cur := domain.NormalizeTags(d.Tags)
	ids, err := tx.Units().SetDocumentTags(ctx, b, doc, cur, cur, seq)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		deleted, _, err := tx.Evidence().DeleteCiting(ctx, b, ids)
		if err != nil {
			return err
		}
		if len(deleted) > 0 {
			if err := enqueueGraph(ctx, tx, b); err != nil {
				return err
			}
		}
	}
	_, _, err = tx.Queue().Enqueue(ctx, store.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Payload: json.RawMessage(`{}`), Dedupe: store.DedupePending,
	})
	return err
}

// enqueueGraph enqueues (or joins a pending or running) graph_maintenance: deleting observations removes
// units, so their link neighbours and entities need maintenance.
func enqueueGraph(ctx context.Context, tx store.Tx, b domain.BankRef) error {
	_, _, err := tx.Queue().Enqueue(ctx, store.Job{
		Bank: b, Kind: domain.OperationKindGraphMaintenance, SerialKey: graphSerialKey,
		Payload: json.RawMessage(`{}`), Dedupe: store.DedupePendingOrRunning,
	})
	return err
}
