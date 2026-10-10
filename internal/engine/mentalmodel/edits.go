package mentalmodel

import "memd/internal/domain"

// EditOp is one delta edit operation over a document's sections and blocks, as proposed by
// MENTAL_MODEL_DELTA and as applied (or skipped, with the reason). Alias of domain.DeltaOp so applied
// ops go straight into domain.RefreshTrace.DeltaOperations.
type EditOp = domain.DeltaOp
