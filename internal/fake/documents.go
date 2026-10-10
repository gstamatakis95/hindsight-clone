package fake

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
)

// docRepo is the DocumentRepo of one transaction.
type docRepo struct{ t *tx }

var _ port.DocumentRepo = docRepo{}

// Get returns the document with UnitCount and UnitsByFactType filled; unknown is domain.ErrNotFound.
func (r docRepo) Get(_ context.Context, b port.BankRef, id port.DocumentID) (domain.Document, error) {
	row, ok := r.t.st.documents[docKey{b, id}]
	if !ok {
		return domain.Document{}, notFound("document %s", id)
	}
	d := row.d
	d.Tags = slices.Clone(d.Tags)
	d.Overrides = fromJSON[domain.BankConfig](row.overrides)
	d.ExtractedAt = ptrCopy(d.ExtractedAt)
	d.UnitsByFactType = map[domain.FactType]int64{}
	for _, u := range r.t.st.units {
		if u.u.Bank == b && u.u.DocumentID == id {
			d.UnitCount++
			d.UnitsByFactType[u.u.Type]++
		}
	}
	return d, nil
}

// Upsert inserts the document or replaces its tags, overrides and blob key and clears extracted_at; the
// hashes of the last completed retain are kept. The bank must exist (domain.ErrNotFound otherwise).
func (r docRepo) Upsert(_ context.Context, d domain.Document) error {
	if _, ok := r.t.st.banks[d.Bank]; !ok {
		return notFound("bank %s/%s", d.Bank.Tenant, d.Bank.Bank)
	}
	if err := checkTags(d.Tags); err != nil {
		return err
	}
	k := docKey{d.Bank, d.ID}
	row, ok := r.t.st.documents[k]
	if !ok {
		row.d = domain.Document{ID: d.ID, Bank: d.Bank, CreatedAt: r.t.now}
	}
	row.d.Tags, row.d.BlobKey, row.d.ExtractedAt = slices.Clone(d.Tags), d.BlobKey, nil
	row.d.UpdatedAt = r.t.now
	row.overrides = jsonOf(d.Overrides)
	r.t.st.documents[k] = row
	return nil
}

// Complete stamps content_hash, overrides_hash and extracted_at (the transaction time).
func (r docRepo) Complete(
	_ context.Context, b port.BankRef, id port.DocumentID, contentHash, overridesHash string,
) error {
	k := docKey{b, id}
	row, ok := r.t.st.documents[k]
	if !ok {
		return notFound("document %s", id)
	}
	row.d.ContentHash, row.d.OverridesHash = contentHash, overridesHash
	row.d.ExtractedAt, row.d.UpdatedAt = ptrCopy(&r.t.now), r.t.now
	r.t.st.documents[k] = row
	return nil
}

// SetTags replaces the document's tag set.
func (r docRepo) SetTags(_ context.Context, b port.BankRef, id port.DocumentID, tags []string) error {
	if err := checkTags(tags); err != nil {
		return err
	}
	k := docKey{b, id}
	row, ok := r.t.st.documents[k]
	if !ok {
		return notFound("document %s", id)
	}
	row.d.Tags, row.d.UpdatedAt = slices.Clone(tags), r.t.now
	r.t.st.documents[k] = row
	return nil
}

// chunkRepo is the ChunkRepo of one transaction.
type chunkRepo struct{ t *tx }

var _ port.ChunkRepo = chunkRepo{}

// Committed returns the document's committed chunks as index -> content hash.
func (r chunkRepo) Committed(_ context.Context, b port.BankRef, doc port.DocumentID) (map[int]string, error) {
	out := map[int]string{}
	for k, c := range r.t.st.chunks {
		if k.bank == b && k.doc == doc {
			out[k.index] = c.ContentHash
		}
	}
	return out, nil
}

// Insert stores chunks, replacing a chunk at an already committed (document, index). The document must
// exist (domain.ErrNotFound otherwise).
func (r chunkRepo) Insert(_ context.Context, cs []domain.Chunk) error {
	for _, c := range cs {
		if _, ok := r.t.st.documents[docKey{c.Bank, c.DocumentID}]; !ok {
			return notFound("document %s of chunk %d", c.DocumentID, c.Index)
		}
		if c.ID == "" {
			c.ID = port.ChunkID(newUUID())
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = r.t.now
		}
		k := chunkKey{c.Bank, c.DocumentID, c.Index}
		if old, ok := r.t.st.chunks[k]; ok && old.ID != c.ID {
			r.t.st.dropChunkRefs(old.ID)
		}
		r.t.st.chunks[k] = c
	}
	return nil
}

// dropChunkRefs clears the chunk id of units that referenced a replaced chunk (ON DELETE SET NULL).
func (s *state) dropChunkRefs(id port.ChunkID) {
	for uid, row := range s.units {
		if row.u.ChunkID == id {
			row.u.ChunkID = ""
			s.units[uid] = row
		}
	}
}
