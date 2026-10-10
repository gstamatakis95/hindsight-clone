package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// Serial keys and payloads of the jobs the retagger enqueues (decision 1301).
const (
	// docSerialPrefix prefixes a document's serial key: "doc:{document_id}".
	docSerialPrefix = "doc:"
	// consolidateSerialKey is the bank's single consolidation key.
	consolidateSerialKey = "consolidate"
)

// retagger implements Retagger over the ports.
type retagger struct {
	s port.Store
}

var _ Retagger = (*retagger)(nil)

// NewRetagger returns the Retagger: Retag is UpdateDocument's API side, Apply the retag_document job. It
// uses the ports directly (UnitRepo.SetDocumentTags, EvidenceRepo.DeleteCiting), see decision 1307.
func NewRetagger(s port.Store) Retagger {
	return &retagger{s: s}
}

// Retag commits doc's new tag set and enqueues retag_document, or does nothing when the set is unchanged.
// The common no-op path reads only: it bumps no change seq. A real change takes the bank's change seq
// (which serializes concurrent retags of the bank), re-reads the tags under that lock and only then writes.
func (r *retagger) Retag(
	ctx context.Context, b port.BankRef, doc port.DocumentID, tags []string,
) (port.OperationID, error) {
	want := domain.NormalizeTags(tags)
	var id port.OperationID
	err := r.s.InTx(ctx, func(tx port.Tx) error {
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
			return nil
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		if d, err = tx.Documents().Get(ctx, b, doc); err != nil {
			return err
		}
		if domain.SameTagSet(d.Tags, want) {
			return nil // a concurrent retag set the same tags first
		}
		id, err = enqueueRetag(ctx, tx, b, doc, want)
		return err
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// enqueueRetag stores the document's new tags and enqueues (or joins) its retag_document job.
func enqueueRetag(
	ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID, tags []string,
) (port.OperationID, error) {
	if err := tx.Documents().SetTags(ctx, b, doc, tags); err != nil {
		return "", err
	}
	payload, err := json.Marshal(RetagPayload{DocumentID: doc})
	if err != nil {
		return "", fmt.Errorf("retag payload: %w", err)
	}
	id, _, err := tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindRetagDocument, SerialKey: docSerialPrefix + string(doc),
		Payload: payload, Dedupe: port.DedupePending,
	})
	return id, err
}

// Apply runs retag_document: it rewrites the tags of the document's live units to the document's current
// tags, deletes the observations citing them (resetting their co-sources) and enqueues consolidation, in
// one transaction. A document (or bank) that is gone is a no-op; a tombstoned bank fails with
// domain.ErrBankDeleting from Tx.Seq. It never creates a bank.
func (r *retagger) Apply(ctx context.Context, j port.Job) error {
	var p RetagPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return fmt.Errorf("%w: retag_document payload %s: %w", domain.ErrInvalidArgument, j.Payload, err)
	}
	if p.DocumentID == "" {
		return fmt.Errorf("%w: retag_document payload %s has no document_id", domain.ErrInvalidArgument, j.Payload)
	}
	err := r.s.InTx(ctx, func(tx port.Tx) error {
		return applyRetag(ctx, tx, j.Bank, p.DocumentID)
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil // the document or the bank is gone: nothing left to retag
	}
	return err
}

// applyRetag is the body of Apply's transaction.
func applyRetag(ctx context.Context, tx port.Tx, b port.BankRef, doc port.DocumentID) error {
	seq, err := tx.Seq(ctx, b)
	if err != nil {
		return err
	}
	d, err := tx.Documents().Get(ctx, b, doc)
	if err != nil {
		return err
	}
	ids, err := tx.Units().SetDocumentTags(ctx, b, doc, domain.NormalizeTags(d.Tags), seq)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if _, _, err := tx.Evidence().DeleteCiting(ctx, b, ids); err != nil {
			return err
		}
	}
	_, _, err = tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
		Payload: json.RawMessage(`{}`), Dedupe: port.DedupePending,
	})
	return err
}
