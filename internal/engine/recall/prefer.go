package recall

// ObservationPreference drops raw facts covered by a returned observation and backfills.
type ObservationPreference interface {
	// Apply removes facts cited by a higher-ranked observation in ranked, then backfills from the rest
	// of ranked until maxTokens (counted with count) is reached.
	Apply(ranked []Candidate, maxTokens int, count TokenCounter) []Candidate
}
