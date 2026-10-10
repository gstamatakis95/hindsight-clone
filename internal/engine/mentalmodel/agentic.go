package mentalmodel

import (
	"context"
	"memd/internal/engine/reflect"
)

// Reflector is the part of reflect.Reflector the agentic synthesizer consumes; reflect.Reflector
// satisfies it.
type Reflector interface {
	// Reflect runs one reflect loop (answering as a document for refreshes).
	Reflect(ctx context.Context, req reflect.ReflectRequest) (reflect.ReflectResult, error)
}
