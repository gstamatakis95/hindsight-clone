package retain

import (
	"context"
	"fmt"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"strings"
)

// Reprocessor re-runs retain over a stored document (ReprocessDocument). docs.Reprocessor declares the same
// method; this implementation satisfies it.
type Reprocessor interface {
	// Reprocess enqueues a retain_document job that re-extracts document id from its stored text,
	// replacing its stored overrides when overrides is non-nil, and returns the operation id. The
	// document's item-level observation scopes are kept. An unknown document is domain.ErrNotFound; a
	// tombstoned bank is domain.ErrBankDeleting.
	Reprocess(
		ctx context.Context, b domain.BankRef, id domain.DocumentID, overrides *domain.BankConfig,
	) (domain.OperationID, error)
}

// reprocessScanLimit is how many of a document's units (newest first) Reprocess scans for one never curated.
const reprocessScanLimit = 100

// reprocessor implements Reprocessor over store.DB.
type reprocessor struct {
	s store.DB
}

var _ Reprocessor = (*reprocessor)(nil)

// NewReprocessor returns the Reprocessor. It makes no model call: it re-upserts the document and enqueues
// a forced retain_document job in one transaction (decision 1600).
func NewReprocessor(s store.DB) Reprocessor {
	return &reprocessor{s: s}
}

// Reprocess implements Reprocessor.
func (r *reprocessor) Reprocess(
	ctx context.Context, b domain.BankRef, id domain.DocumentID, overrides *domain.BankConfig,
) (domain.OperationID, error) {
	var opID domain.OperationID
	err := r.s.InTx(ctx, func(tx store.Tx) error {
		// Seq first: it locks the bank before the reads and the enqueue, and fails on a tombstoned bank.
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		doc, err := tx.Documents().Get(ctx, b, id)
		if err != nil {
			return err
		}
		if overrides != nil {
			doc.Overrides = overrides
		}
		p, err := reprocessPayload(ctx, tx, b, doc)
		if err != nil {
			return err
		}
		if err := tx.Documents().Upsert(ctx, domain.Document{
			ID: doc.ID, Bank: b, Overrides: doc.Overrides, Tags: doc.Tags, BlobKey: doc.BlobKey,
			ObservationScopes: doc.ObservationScopes,
		}); err != nil {
			return err
		}
		raw, err := p.Marshal()
		if err != nil {
			return err
		}
		opID, _, err = tx.Queue().Enqueue(ctx, store.Job{
			Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: DocumentSerialKey(id),
			Payload: raw, Overrides: doc.Overrides, Dedupe: store.DedupeNone,
		})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("retain: reprocess document %s: %w", id, err)
	}
	return opID, nil
}

// reprocessPayload rebuilds a retain_document payload from the stored document: its blob key (whose last
// segment is the content hash, blob.Key), tags and item scopes. The item's context, metadata,
// mention time and timeless flag are not stored on the document, so they are taken from one of its units
// (all units of one retain share them): the newest one never curated (a curated context or timeless flag must
// not spread to every re-extracted unit, since reprocess resets curation), live or archived, else the newest.
// Entity hints are not stored anywhere and are lost (decision 1600).
func reprocessPayload(
	ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.Document,
) (DocumentPayload, error) {
	hash := doc.BlobKey[strings.LastIndex(doc.BlobKey, "/")+1:]
	if doc.BlobKey == "" || blob.Key(b, doc.ID, hash) != doc.BlobKey {
		return DocumentPayload{}, fmt.Errorf("%w: document %s has no stored text", domain.ErrFailedPrecondition, doc.ID)
	}
	p := DocumentPayload{
		DocumentID: doc.ID, ContentHash: hash, BlobKey: doc.BlobKey, Tags: domain.NormalizeTags(doc.Tags),
		ObservationScopes: doc.ObservationScopes, ForceReextract: true,
	}
	us, err := tx.ListUnits(ctx, store.UnitQuery{Bank: b, DocumentID: doc.ID, Limit: reprocessScanLimit})
	if err != nil || len(us) == 0 {
		return p, err
	}
	u := us[0]
	for _, c := range us {
		if c.EditedAt == nil {
			u = c
			break
		}
	}
	p.Context, p.Metadata, p.Timeless = u.Context, u.Metadata, u.Timeless
	if !u.Timeless && u.MentionedAt != nil {
		at := u.MentionedAt.UTC()
		p.Timestamp = &at
	}
	return p, nil
}
