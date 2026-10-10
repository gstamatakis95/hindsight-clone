package retain

// TokenCounter counts tokens for chunking and budgets. It is declared here by its consumer; any
// tokenizer with this method satisfies it.
type TokenCounter interface {
	// Count returns the number of tokens in text.
	Count(text string) int
}
