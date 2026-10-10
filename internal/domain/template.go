package domain

// BankTemplate is a bank's portable setup: config, directives and mental-model specs (no memories).
type BankTemplate struct {
	// Config is the bank's own overrides.
	Config BankConfig `json:"config"`
	// Directives are the bank's directives (ids are reassigned on create).
	Directives []Directive `json:"directives,omitempty"`
	// MentalModels are the writable specs of the bank's mental models; each gets a first build on create.
	MentalModels []MentalModelSpec `json:"mental_models,omitempty"`
}
