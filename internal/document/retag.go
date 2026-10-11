package document

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
)

// RetagDocument replaces doc's tag set and returns the retag_document operation, read in a second short
// transaction with the bank checked live; a set-equal update is a no-op and returns the zero Operation. The
// new tags are committed on the document row and its units in the same transaction as the enqueue
// (DedupePending), so a retag that joins a pending one is never lost. An unknown bank or document is
// domain.ErrNotFound, a tombstoned bank domain.ErrBankDeleting.
func (s *Service) RetagDocument(
	ctx context.Context, b domain.BankRef, doc domain.DocumentID, tags []string,
) (domain.Operation, error) {
	id, err := s.retag(ctx, b, doc, tags)
	if err != nil || id == "" {
		return domain.Operation{}, err
	}
	var op domain.Operation
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
		op = domain.Operation{}
		if err := liveBank(ctx, tx, b); err != nil {
			return err
		}
		var err error
		op, err = tx.Queue().Get(ctx, b, id)
		return err
	})
	return op, err
}

// retag commits doc's new tag set and enqueues retag_document, or does nothing when the set is unchanged.
// An unchanged set is a no-op only while the document's newest retag_document operation (if any) did not
// end FAILED or CANCELLED; otherwise the observations derived under the old tags were never cleared, so
// the job is enqueued again. The common no-op path reads only: it bumps no change seq. A real change takes
// the bank's change seq (which serializes concurrent retags of the bank), re-reads the tags under that lock
// and only then writes: the document's tags and, in the same transaction, its units' document-derived tags
// (old to new; their label tags stay, decision 1900).
func (s *Service) retag(
	ctx context.Context, b domain.BankRef, doc domain.DocumentID, tags []string,
) (domain.OperationID, error) {
	want := domain.NormalizeTags(tags)
	var id domain.OperationID
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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
	key := jobs.DocumentSerialKey(doc)
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
	id, _, err := jobs.EnqueueRetag(ctx, tx, b, doc)
	return id, err
}

// RunRetagJob runs retag_document. It first reports the run's progress (stage = the job kind, 0 of 1 done),
// its one progress write and cancel check: a cancelled job, or one whose bank is being deleted, stops there.
// Then it re-asserts the document's current tags on its units (RetagDocument already replaced them, keeping
// their label tags) and resets their consolidation stamps, deletes the observations citing them (resetting
// their co-sources) and enqueues consolidation, in one transaction, plus graph_maintenance when observations
// were deleted. A document that is gone is a no-op; a gone bank (domain.ErrNotFound) or a tombstoned one
// (domain.ErrBankDeleting) fails from Tx.Seq, and an error after ctx ended is domain.ErrCancelled. It never
// creates a bank. Running it twice repeats only the stamp reset.
func (s *Service) RunRetagJob(ctx context.Context, run *jobs.Run) error {
	p := domain.Progress{Stage: string(run.Job.Kind), RecordedAt: s.d.Clock.Now(), Total: 1}
	if err := run.Report(ctx, p); err != nil {
		return fmt.Errorf("%s: checkpoint: %w", run.Job.Kind, err)
	}
	if err := s.applyRetag(ctx, run.Job); err != nil {
		return fmt.Errorf("retag_document: %w", err)
	}
	return nil
}

// applyRetag is the body of the retag_document job (see RunRetagJob).
func (s *Service) applyRetag(ctx context.Context, j store.Job) error {
	var p jobs.RetagPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: retag_document payload %s: %w", domain.ErrInvalidArgument, j.Payload, err)
	}
	if p.DocumentID == "" {
		return fmt.Errorf("%w: retag_document payload %s has no document_id", domain.ErrInvalidArgument, j.Payload)
	}
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		return retagTx(ctx, tx, j.Bank, p.DocumentID)
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

// retagTx is the body of the retag job's transaction.
func retagTx(ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID) error {
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
			if _, _, err := jobs.EnqueueGraph(ctx, tx, b); err != nil {
				return err
			}
		}
	}
	_, _, err = jobs.EnqueueConsolidate(ctx, tx, b)
	return err
}
