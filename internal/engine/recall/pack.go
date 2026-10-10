package recall

// Packer fits ranked candidates into a token budget.
type Packer interface {
	// Pack keeps candidates in order until the next one would exceed maxTokens, setting Candidate.Tokens.
	Pack(cs []Candidate, maxTokens int) []Candidate
}

// TokenCounter counts tokens for packing and include budgets. It is declared here by its consumer; any
// tokenizer with this method satisfies it.
type TokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}
