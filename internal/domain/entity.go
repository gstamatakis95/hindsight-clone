package domain

import "time"

// Entity is a resolved entity: a person, place, thing or an exact-match label value.
type Entity struct {
	// ID is the entity id.
	ID EntityID
	// Bank owns the entity.
	Bank BankRef
	// CanonicalName is the resolved display name.
	CanonicalName string
	// Kind is the entity type for free-form entities, or the label key for label entities.
	Kind string
	// LabelValue is set for label entities, which resolve by exact (Kind, LabelValue) match.
	LabelValue string
	// MentionCount is how many units mention the entity.
	MentionCount int
	// FirstSeen is the earliest mention time.
	FirstSeen *time.Time
	// LastSeen is the latest mention time.
	LastSeen *time.Time
	// CreatedAt is the creation time.
	CreatedAt time.Time
	// UpdatedAt is the last update time.
	UpdatedAt time.Time
}

// IsLabel reports whether the entity is an exact-match label entity.
func (e Entity) IsLabel() bool { return e.LabelValue != "" }

// EntityCandidate is an existing entity proposed for a mention, with its name similarity.
type EntityCandidate struct {
	// Entity is the candidate.
	Entity Entity
	// NameSimilarity is the trigram similarity of the candidate's name to the mention, in [0,1].
	NameSimilarity float64
}

// EntityHint is a caller-supplied entity for a retain item, merged with extracted mentions.
type EntityHint struct {
	// Text is the entity name.
	Text string
	// Type is the optional entity type.
	Type string
}

// EntityState is the entity view attached to recall results.
type EntityState struct {
	// EntityID is the entity id.
	EntityID EntityID
	// CanonicalName is the entity's name.
	CanonicalName string
	// ObservationIDs are observations about the entity.
	ObservationIDs []UnitID
}
