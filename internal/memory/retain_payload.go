package memory

import (
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
)

// pinTuning applies the payload's pinned chunking to t.
func pinTuning(p jobs.RetainPayload, t tuning) tuning {
	if p.ChunkMaxTokens > 0 {
		t.chunkMax, t.chunkOverlap, t.structuredMax = p.ChunkMaxTokens, p.ChunkOverlapTokens, p.StructuredChunkMaxTokens
		if t.structuredMax <= 0 {
			t.structuredMax = t.chunkMax
		}
	}
	return t
}

// hints converts the payload's hints to domain hints.
func hints(p jobs.RetainPayload) []domain.EntityHint {
	out := make([]domain.EntityHint, len(p.Entities))
	for i, h := range p.Entities {
		out[i] = domain.EntityHint{Text: h.Text, Type: h.Type}
	}
	return out
}

// payloadHints converts domain hints to payload hints.
func payloadHints(hs []domain.EntityHint) []jobs.RetainHint {
	if len(hs) == 0 {
		return nil
	}
	out := make([]jobs.RetainHint, len(hs))
	for i, h := range hs {
		out[i] = jobs.RetainHint{Text: h.Text, Type: h.Type}
	}
	return out
}

// decodePayload decodes a retain_document job payload; a payload without document id, content hash or blob
// key is domain.ErrInvalidArgument.
func decodePayload(raw json.RawMessage) (jobs.RetainPayload, error) {
	var p jobs.RetainPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return jobs.RetainPayload{}, fmt.Errorf("%w: retain_document payload: %w", domain.ErrInvalidArgument, err)
	}
	if p.DocumentID == "" || p.ContentHash == "" || p.BlobKey == "" {
		return jobs.RetainPayload{}, fmt.Errorf("%w: retain_document payload needs document_id, content_hash and blob_key",
			domain.ErrInvalidArgument)
	}
	return p, nil
}

// contentHash is the hex SHA-256 of a document's original text, the content half of the no-op check.
func contentHash(content string) string { return hashText(content) }

// overridesHash is the hex SHA-256 of the overrides' JSON, the overrides half of the no-op check; nil
// overrides hash to "".
func overridesHash(o *domain.BankConfig) (string, error) {
	if o == nil {
		return "", nil
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("retain: hash overrides: %w", err)
	}
	return hashText(string(raw)), nil
}

// noopHash is the hash stamped as the document's overrides_hash and compared by the no-op check: the
// overrides' hash, extended with the item's observation scopes when it set any, so a retain that only changes
// the scopes is not "unchanged".
func noopHash(o *domain.BankConfig, scopes *domain.ObservationScopes) (string, error) {
	oh, err := overridesHash(o)
	if err != nil || scopes == nil {
		return oh, err
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return "", fmt.Errorf("retain: hash scopes: %w", err)
	}
	return hashText(oh + "\x00" + string(raw)), nil
}

// runHash is a chunk's stored hash: its content hash salted with the run key, so Chunks.Committed tells the
// chunks this retain committed from leftovers of an earlier one (decision 801).
func runHash(runKey, hash string) string { return hashText(runKey + "\x00" + hash) }
