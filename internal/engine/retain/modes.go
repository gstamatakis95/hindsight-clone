package retain

import (
	"encoding/json"
	"memd/internal/domain"
)

// ExtractionStrategy produces facts for one mode; Extractor picks one per document.
type ExtractionStrategy interface {
	// Mode is the extraction mode the strategy implements: concise | verbose | verbatim | chunks | custom.
	Mode() domain.ExtractionMode
	// NeedsLLM is false for chunks mode, which stores chunks as units with no call.
	NeedsLLM() bool
	// Prompt renders the system and user messages and the JSON Schema of the EXTRACT call for one chunk.
	Prompt(cfg domain.BankConfig, in ExtractInput) (system, user string, schema json.RawMessage)
}
