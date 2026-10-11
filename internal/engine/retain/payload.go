package retain

import (
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"time"
)

// DocumentPayload is the payload of a retain_document job: everything the Retainer needs besides the stored
// document row and Job.Overrides (the merged request < item overrides). It pins the content by hash and blob
// key, so a job that resumes after a newer retain of the same document still processes its own content.
// ReprocessDocument (WP-13) enqueues the same payload.
type DocumentPayload struct {
	// DocumentID is the document to process.
	DocumentID domain.DocumentID `json:"document_id"`
	// ContentHash is the hex SHA-256 of the original text (ContentHash).
	ContentHash string `json:"content_hash"`
	// BlobKey is where the original text is stored (blob.Key).
	BlobKey string `json:"blob_key"`
	// Tags are the document's normalized tags, written on every unit.
	Tags []string `json:"tags,omitempty"`
	// Context is the item's free-form context, stored on every unit.
	Context string `json:"context,omitempty"`
	// Timestamp is when the content was said; nil with Timeless (or "now" when neither is set).
	Timestamp *time.Time `json:"timestamp,omitempty"`
	// Timeless marks content with no time anchor: units get no mention or occurrence dates.
	Timeless bool `json:"timeless,omitempty"`
	// Metadata is the item's string metadata, stored on every unit.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Entities are the caller's entity hints.
	Entities []PayloadHint `json:"entities,omitempty"`
	// ResolveEntities false takes the hint names literally; nil = true.
	ResolveEntities *bool `json:"resolve_entities,omitempty"`
	// ObservationScopes is the item's scope fan-out; nil = the bank default.
	ObservationScopes *domain.ObservationScopes `json:"observation_scopes,omitempty"`
	// ForceReextract re-runs extraction even when content and overrides are unchanged.
	ForceReextract bool `json:"force_reextract,omitempty"`
	// SkipConsolidation suppresses the consolidate job after the retain.
	SkipConsolidation bool `json:"skip_consolidation,omitempty"`
	// ChunkMaxTokens, ChunkOverlapTokens and StructuredChunkMaxTokens pin the chunking resolved at ingest, so a
	// config change between attempts cannot move chunk boundaries under a resuming job; 0 = resolve now.
	ChunkMaxTokens int `json:"chunk_max_tokens,omitempty"`
	// ChunkOverlapTokens is the pinned overlap (see ChunkMaxTokens).
	ChunkOverlapTokens int `json:"chunk_overlap_tokens,omitempty"`
	// StructuredChunkMaxTokens is the pinned structured limit (see ChunkMaxTokens).
	StructuredChunkMaxTokens int `json:"structured_chunk_max_tokens,omitempty"`
}

// pinTuning applies the payload's pinned chunking to t.
func (p DocumentPayload) pinTuning(t tuning) tuning {
	if p.ChunkMaxTokens > 0 {
		t.chunkMax, t.chunkOverlap, t.structuredMax = p.ChunkMaxTokens, p.ChunkOverlapTokens, p.StructuredChunkMaxTokens
		if t.structuredMax <= 0 {
			t.structuredMax = t.chunkMax
		}
	}
	return t
}

// PayloadHint is one entity hint in a DocumentPayload (domain.EntityHint with JSON names).
type PayloadHint struct {
	// Text is the entity name.
	Text string `json:"text"`
	// Type is the optional entity type.
	Type string `json:"type,omitempty"`
}

// hints converts the payload's hints to domain hints.
func (p DocumentPayload) hints() []domain.EntityHint {
	out := make([]domain.EntityHint, len(p.Entities))
	for i, h := range p.Entities {
		out[i] = domain.EntityHint{Text: h.Text, Type: h.Type}
	}
	return out
}

// payloadHints converts domain hints to payload hints.
func payloadHints(hs []domain.EntityHint) []PayloadHint {
	if len(hs) == 0 {
		return nil
	}
	out := make([]PayloadHint, len(hs))
	for i, h := range hs {
		out[i] = PayloadHint{Text: h.Text, Type: h.Type}
	}
	return out
}

// Marshal encodes the payload as a job payload.
func (p DocumentPayload) Marshal() (json.RawMessage, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("retain: encode payload: %w", err)
	}
	return raw, nil
}

// DecodePayload decodes a retain_document job payload; a payload without document id, content hash or blob
// key is domain.ErrInvalidArgument.
func DecodePayload(raw json.RawMessage) (DocumentPayload, error) {
	var p DocumentPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return DocumentPayload{}, fmt.Errorf("%w: retain_document payload: %w", domain.ErrInvalidArgument, err)
	}
	if p.DocumentID == "" || p.ContentHash == "" || p.BlobKey == "" {
		return DocumentPayload{}, fmt.Errorf("%w: retain_document payload needs document_id, content_hash and blob_key",
			domain.ErrInvalidArgument)
	}
	return p, nil
}

// ContentHash is the hex SHA-256 of a document's original text, the content half of the no-op check.
func ContentHash(content string) string { return hashText(content) }

// OverridesHash is the hex SHA-256 of the overrides' JSON, the overrides half of the no-op check; nil
// overrides hash to "".
func OverridesHash(o *domain.BankConfig) (string, error) {
	if o == nil {
		return "", nil
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("retain: hash overrides: %w", err)
	}
	return hashText(string(raw)), nil
}

// NoopHash is the hash stamped as the document's overrides_hash and compared by the no-op check: the
// overrides' hash, extended with the item's observation scopes when it set any, so a retain that only changes
// the scopes is not "unchanged".
func NoopHash(o *domain.BankConfig, scopes *domain.ObservationScopes) (string, error) {
	oh, err := OverridesHash(o)
	if err != nil || scopes == nil {
		return oh, err
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return "", fmt.Errorf("retain: hash scopes: %w", err)
	}
	return hashText(oh + "\x00" + string(raw)), nil
}

// DocumentSerialKey is the serial key of a document's jobs (retain_document, rederive, retag): "doc:{id}".
func DocumentSerialKey(doc domain.DocumentID) string { return "doc:" + string(doc) }

// Serial keys of the jobs retain enqueues (SPEC Jobs table).
const (
	consolidateSerialKey = "consolidate"
	graphSerialKey       = "graph"
)

// runHash is a chunk's stored hash: its content hash salted with the run key, so Chunks.Committed tells the
// chunks this retain committed from leftovers of an earlier one (decision 801).
func runHash(runKey, contentHash string) string { return hashText(runKey + "\x00" + contentHash) }
