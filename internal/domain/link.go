package domain

import "time"

// LinkKind names an edge type in the memory graph.
type LinkKind string

// Link kinds.
const (
	// LinkTemporal joins units close in time (capped by temporal_link_cap per unit).
	LinkTemporal LinkKind = "temporal"
	// LinkSemantic joins units whose embeddings are close (capped by semantic_link_k per unit).
	LinkSemantic LinkKind = "semantic"
	// LinkCausal joins a cause to its effect, extracted from one batch.
	LinkCausal LinkKind = "causal"
)

// Link is a directed, weighted edge between two live units of one bank.
type Link struct {
	// Bank owns both units.
	Bank BankRef
	// From is the source unit.
	From UnitID
	// To is the target unit.
	To UnitID
	// Kind is the edge type.
	Kind LinkKind
	// Weight is the edge weight (similarity, proximity or confidence) in [0,1].
	Weight float64
	// CreatedAt is the creation time.
	CreatedAt time.Time
}

// Evidence ties an observation to one fact it rests on, with the exact supporting quote.
type Evidence struct {
	// Unit is the cited fact.
	Unit UnitID
	// Quote is the exact text of the fact that supports the observation.
	Quote string
}
