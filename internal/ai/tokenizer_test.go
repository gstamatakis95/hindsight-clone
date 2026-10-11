package ai

import (
	"strings"
	"sync"
	"testing"
)

// TestTokenizerConcurrent shares one Tokenizer across goroutines, as every RPC and worker slot does; run
// with -race.
func TestTokenizerConcurrent(t *testing.T) {
	tok, err := NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	want := tok.Count("Alice moved to Lisbon in 2021.")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			for j := range 50 {
				if got := tok.Count("Alice moved to Lisbon in 2021."); got != want {
					t.Errorf("goroutine %d: %d tokens, want %d", i, got, want)
				}
				tok.Count(strings.Repeat("token ", i+j))
			}
		})
	}
	wg.Wait()
}

// TestTokenizer checks the embedded tiktoken encoding counts tokens without a download.
func TestTokenizer(t *testing.T) {
	tok, err := NewTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	if n := tok.Count("hello world"); n != 2 {
		t.Fatalf("hello world = %d tokens, want 2", n)
	}
	if n := tok.Count(""); n != 0 {
		t.Fatalf("empty = %d", n)
	}
}
