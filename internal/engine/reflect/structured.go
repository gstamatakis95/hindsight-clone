package reflect

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
)

// StructuredProjector extracts a JSON object matching a schema from a finished document.
type StructuredProjector interface {
	// Project makes one STRUCTURED_OUTPUT call and returns JSON valid against req.Schema.
	Project(ctx context.Context, req ProjectRequest) (json.RawMessage, error)
}

// ProjectRequest is one structured-output projection.
type ProjectRequest struct {
	// Bank owns the document (config resolution and usage accounting).
	Bank port.BankRef
	// Schema is the JSON Schema to project onto.
	Schema json.RawMessage
	// Document is the finished answer or mental-model content.
	Document string
	// Overrides are request-level config overrides (the STRUCTURED_OUTPUT binding); nil = none.
	Overrides *domain.BankConfig
}
