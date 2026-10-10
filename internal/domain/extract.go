package domain

import "time"

// Mention is one entity mention in an extracted fact: a name and an optional kind.
type Mention struct {
	// Name is the surface name.
	Name string
	// Kind is the entity type (person, place, ...); empty = unknown.
	Kind string
}

// ExtractedFact is one fact produced by extraction, before normalization, embedding and storage.
// It lives in domain (not the retain engine) because both retain and timex use it and timex must not
// import retain.
type ExtractedFact struct {
	// Text is the fact text.
	Text string
	// Type is FactTypeWorld or FactTypeExperience.
	Type FactType
	// OccurredStart is when the fact started; nil = unknown.
	OccurredStart *time.Time
	// OccurredEnd is when the fact ended; nil = unknown.
	OccurredEnd *time.Time
	// TimeExpression is the raw relative date the extractor saw ("last spring"), resolved by timex.
	TimeExpression string
	// Entities are the fact's entity mentions (name + kind).
	Entities []Mention
	// Labels are extracted key:value labels.
	Labels []Label
	// Causes are indexes into the same batch of the facts that caused this one.
	Causes []int
}
