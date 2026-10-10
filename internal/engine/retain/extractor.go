package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
	"time"
)

// ExtractedFact is one fact produced by extraction, before normalization, embedding and storage. It is
// an alias of domain.ExtractedFact, which lives in domain so timex can normalize it without importing
// retain.
type ExtractedFact = domain.ExtractedFact

// Mention is one entity mention (name + kind) of an extracted fact; an alias of domain.Mention.
type Mention = domain.Mention

// Extractor turns one chunk into facts.
type Extractor interface {
	// Extract makes one LLM call per chunk (EXTRACT slot); mission + speaker context injected. The
	// strategy for in.Config's extraction mode builds the prompt; chunks mode returns the chunk as a
	// single fact with no call.
	Extract(ctx context.Context, in ExtractInput) ([]ExtractedFact, error)
}

// ExtractInput is one chunk to extract facts from, with the item context the prompt needs.
type ExtractInput struct {
	// Bank owns the document.
	Bank port.BankRef
	// DocumentID is the source document.
	DocumentID port.DocumentID
	// ChunkIndex is the chunk's position in the document, from 0.
	ChunkIndex int
	// Text is the chunk text.
	Text string
	// Context is the retain item's free-form context (speaker, conversation, source).
	Context string
	// MentionedAt is the item timestamp, the reference time for relative dates; nil when timeless.
	MentionedAt *time.Time
	// Metadata is the retain item's string metadata.
	Metadata map[string]string
	// Hints are caller-supplied entities, shown to the extractor and merged with extracted mentions.
	Hints []domain.EntityHint
	// Config is the effective config (bank < request < item): mission, extraction mode, custom prompt,
	// entity labels and the EXTRACT binding.
	Config domain.BankConfig
}
