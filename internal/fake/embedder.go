package fake

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// EmbedMode selects how an Embedder turns text into a vector.
type EmbedMode int

const (
	// EmbedHash (the default) gives each distinct text an unrelated pseudo-random unit vector derived from
	// sha256(text): equal texts get equal vectors, different texts are near-orthogonal (|cos| ~ 0.04).
	// The empty text hashes like any other.
	EmbedHash EmbedMode = iota
	// EmbedSimilar is a bag-of-words embedding: texts that share tokens get similar vectors. Each lowercase
	// token (a run of Unicode letters and digits) adds weight to one bucket chosen by the FNV-1a hash of
	// the token modulo domain.EmbeddingDim, and the result is normalized. Texts with no shared tokens are
	// (near) orthogonal; word order and punctuation don't matter. A text with no tokens falls back to its
	// EmbedHash vector. There is no stopword list: "the", "a", "is" count like any word, so two texts
	// sharing only function words still get a positive cosine. Keep fixture texts distinct in content words.
	EmbedSimilar
)

// embedHashBlocks is how many sha256 blocks fill one hash-mode vector: 8 uint32 values per 32-byte block.
const embedHashBlocks = domain.EmbeddingDim / (sha256.Size / 4)

// EmbedCall records one Embedder call.
type EmbedCall struct {
	// Query is true for EmbedQuery and false for EmbedDocuments.
	Query bool
	// Texts are the inputs: the one query, or the documents in order.
	Texts []string
}

// Embedder is a deterministic port.Embedder. Vectors are domain.EmbeddingDim float32 values of unit L2
// length, identical across processes, never NaN. No nomic task prefix is applied, so EmbedQuery(x) equals
// EmbedDocuments([x])[0] equals Vector(x). Every call is recorded (see Calls), including calls that fail
// because of FailWith or a done ctx. It is safe for concurrent use.
type Embedder struct {
	mu    sync.Mutex
	mode  EmbedMode
	fail  error
	calls []EmbedCall
}

var _ port.Embedder = (*Embedder)(nil)

// NewEmbedder returns an Embedder using mode.
func NewEmbedder(mode EmbedMode) *Embedder {
	return &Embedder{mode: mode}
}

// EmbedDocuments returns Vector(t) for each text, in order.
func (e *Embedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if err := e.begin(ctx, EmbedCall{Texts: slices.Clone(texts)}); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.Vector(t)
	}
	return out, nil
}

// EmbedQuery returns Vector(text).
func (e *Embedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := e.begin(ctx, EmbedCall{Query: true, Texts: []string{text}}); err != nil {
		return nil, err
	}
	return e.Vector(text), nil
}

// begin records c, then reports a done ctx or the FailWith error.
func (e *Embedder) begin(ctx context.Context, c EmbedCall) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, c)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake embedder: %w", err)
	}
	if e.fail != nil {
		return fmt.Errorf("fake embedder: %w", e.fail)
	}
	return nil
}

// Vector returns the vector both embed methods return for text under the Embedder's mode. It is not
// recorded as a call and ignores FailWith.
func (e *Embedder) Vector(text string) []float32 {
	if e.mode == EmbedSimilar {
		if v, ok := embedSimilarVector(text); ok {
			return v
		}
	}
	return embedHashVector(text)
}

// FailWith makes every later embed call return an error wrapping err (errors.Is matches) until
// FailWith(nil).
func (e *Embedder) FailWith(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail = err
}

// Calls returns a copy of the recorded calls in call order.
func (e *Embedder) Calls() []EmbedCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]EmbedCall, len(e.calls))
	for i, c := range e.calls {
		out[i] = EmbedCall{Query: c.Query, Texts: slices.Clone(c.Texts)}
	}
	return out
}

// embedHashVector derives a unit vector from sha256("fake-embed\x00" + text + block counter).
func embedHashVector(text string) []float32 {
	vec := make([]float64, domain.EmbeddingDim)
	var ctr [4]byte
	i := 0
	for blk := uint32(0); blk < embedHashBlocks; blk++ {
		binary.BigEndian.PutUint32(ctr[:], blk)
		h := sha256.New()
		_, _ = h.Write([]byte("fake-embed\x00"))
		_, _ = h.Write([]byte(text))
		_, _ = h.Write(ctr[:])
		sum := h.Sum(nil)
		for off := 0; off < len(sum); off += 4 {
			u := binary.BigEndian.Uint32(sum[off : off+4])
			vec[i] = float64(u)/math.MaxUint32*2 - 1
			i++
		}
	}
	return embedNormalize(vec)
}

// embedSimilarVector is the bag-of-words vector of text; ok is false when text has no tokens.
func embedSimilarVector(text string) ([]float32, bool) {
	toks := modelTokens(text)
	if len(toks) == 0 {
		return nil, false
	}
	vec := make([]float64, domain.EmbeddingDim)
	for _, tok := range toks {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		vec[h.Sum32()%domain.EmbeddingDim]++
	}
	return embedNormalize(vec), true
}

// embedNormalize scales vec to unit L2 length; an all-zero vec becomes the first basis vector.
func embedNormalize(vec []float64) []float32 {
	var sq float64
	for _, x := range vec {
		sq += x * x
	}
	out := make([]float32, len(vec))
	if sq == 0 {
		out[0] = 1
		return out
	}
	n := math.Sqrt(sq)
	for i, x := range vec {
		out[i] = float32(x / n)
	}
	return out
}

// modelTokens splits text into lowercase runs of Unicode letters and digits, in order, with repeats.
func modelTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
