package retain

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// Linker adds graph edges for freshly inserted (or re-derived) units.
type Linker interface {
	// Link adds temporal + semantic (kNN >= threshold) + causal edges for freshly inserted units, within
	// the per-unit caps (temporal_link_cap, semantic_link_k). It runs in the caller's transaction.
	Link(ctx context.Context, tx port.Tx, b port.BankRef, units []domain.Unit, causes CauseMap) error
}

// CauseMap maps an effect unit to the units that caused it, from the batch-local
// domain.ExtractedFact.Causes indexes once the units have ids. Nil = no causal edges.
type CauseMap map[port.UnitID][]port.UnitID
