package mentalmodel

import (
	"context"
	"memd/internal/engine/recall"
)

// Recaller is the part of recall.Recaller the single-shot synthesizer consumes; recall.Recaller
// satisfies it.
type Recaller interface {
	// Recall runs one recall request (bounded by RecallRequest.Seq for a stable snapshot).
	Recall(ctx context.Context, req recall.RecallRequest) (recall.RecallResult, error)
}
