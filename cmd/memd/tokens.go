package main

import (
	"fmt"

	"github.com/tiktoken-go/tokenizer"
)

// tokenCounter counts tokens with tiktoken's cl100k_base encoding, embedded in the binary (no download at
// start). One value serves recall's packing and include budgets, retain's chunker and consolidation's
// source-fact budgets (recall.TokenCounter, retain.TokenCounter). The codec only reads its tables after
// construction, so it is safe for concurrent use.
type tokenCounter struct{ codec tokenizer.Codec }

// newTokenCounter loads the cl100k_base encoding.
func newTokenCounter() (*tokenCounter, error) {
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return nil, fmt.Errorf("memd: tokenizer: %w", err)
	}
	return &tokenCounter{codec: codec}, nil
}

// Count returns the number of tokens in text. A text the codec can't encode (it never happens for valid
// UTF-8) falls back to about four characters per token, so budgets never break on one odd input.
func (t *tokenCounter) Count(text string) int {
	n, err := t.codec.Count(text)
	if err != nil {
		return (len(text) + 3) / 4
	}
	return n
}
