package gatewayemu

import (
	"hash/fnv"
	"math"
	"memd/internal/domain"
	"net/http"
	"strings"
	"unicode"
)

// EmbedMode selects how texts become vectors.
type EmbedMode int

const (
	// EmbedSharedTokens hashes each content word into a few dimensions (a bag of words), so texts that share
	// words have a positive cosine similarity and recall's vector arm finds them. The default.
	EmbedSharedTokens EmbedMode = iota
	// EmbedTextHash derives the whole vector from a hash of the full text: equal texts are identical, any two
	// different texts are nearly orthogonal.
	EmbedTextHash
)

// nomicPrefixes are the task prefixes the gateway client adds; they are stripped before hashing so that every
// document and every query don't share a token.
var nomicPrefixes = []string{"search_document: ", "search_query: "}

// biasWeight is a constant first component: it keeps every vector non-zero (pgvector's cosine distance on a
// zero vector is NaN) while adding little similarity between unrelated texts.
const biasWeight = 0.05

// stopwords are frequent words that carry no topic; they are ignored by embeddings and rerank.
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "did": true,
	"do": true, "does": true, "for": true, "from": true, "had": true, "has": true, "have": true, "he": true,
	"her": true, "his": true, "how": true, "i": true, "in": true, "is": true, "it": true, "its": true, "me": true,
	"my": true, "of": true, "on": true, "or": true, "she": true, "that": true, "the": true, "their": true,
	"they": true, "this": true, "to": true, "was": true, "were": true, "what": true, "when": true, "where": true,
	"which": true, "who": true, "why": true, "with": true, "you": true, "your": true,
}

// Tokens returns the lowercase content words of text (letters and digits, stopwords dropped), in order.
func Tokens(text string) []string {
	for _, p := range nomicPrefixes {
		text = strings.TrimPrefix(text, p)
	}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := words[:0]
	for _, w := range words {
		if !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

// Embed returns the deterministic domain.EmbeddingDim-dimensional unit vector of text in mode.
func Embed(mode EmbedMode, text string) []float32 {
	v := make([]float64, domain.EmbeddingDim)
	v[0] = biasWeight
	if mode == EmbedTextHash {
		fillHashed(v, text)
	} else {
		for _, tok := range Tokens(text) {
			h := hash64(tok)
			// Two dimensions per token keep collisions between different words rare.
			for k := range 2 {
				idx := 1 + int((h>>(16*k))%uint64(domain.EmbeddingDim-1))
				v[idx]++
			}
		}
	}
	return normalize(v)
}

// fillHashed fills v[1:] from a hash chain seeded by text.
func fillHashed(v []float64, text string) {
	for _, p := range nomicPrefixes {
		text = strings.TrimPrefix(text, p)
	}
	h := hash64(text)
	for i := 1; i < len(v); i++ {
		h ^= h << 13
		h ^= h >> 7
		h ^= h << 17
		v[i] = float64(int64(h%2001)-1000) / 1000
	}
}

// hash64 is FNV-1a over s.
func hash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// normalize scales v to unit length.
func normalize(v []float64) []float32 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	n := math.Sqrt(sum)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x / n)
	}
	return out
}

// Overlap is the share of query tokens that appear in doc, in [0,1]; 0 for a query without content words.
func Overlap(query, doc string) float64 {
	q := Tokens(query)
	if len(q) == 0 {
		return 0
	}
	in := map[string]bool{}
	for _, t := range Tokens(doc) {
		in[t] = true
	}
	seen, hit := map[string]bool{}, 0
	for _, t := range q {
		if seen[t] {
			continue
		}
		seen[t] = true
		if in[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(seen))
}

// embeddingsRequest is the /embeddings body the gateway client sends.
type embeddingsRequest struct {
	// Model is the embedding model.
	Model string `json:"model"`
	// Input are the texts.
	Input []string `json:"input"`
}

// embeddingItem is one vector of an embeddings response.
type embeddingItem struct {
	// Object is always "embedding".
	Object string `json:"object"`
	// Index is the input position.
	Index int `json:"index"`
	// Embedding is the vector.
	Embedding []float32 `json:"embedding"`
}

// serveEmbeddings answers /embeddings with one vector per input, in order.
func (s *Server) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req embeddingsRequest
	if err := readJSON(r, &req); err != nil {
		s.record(Call{Op: OpEmbeddings, Status: http.StatusBadRequest})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	call := Call{Op: OpEmbeddings, Model: req.Model, Status: http.StatusOK}
	f := s.takeFault(OpEmbeddings)
	call.Fault = f != nil
	if done, status := applyFault(w, r, f); done {
		call.Status = status
		s.record(call)
		return
	}
	s.record(call)
	if f != nil && f.InvalidJSON {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(invalidJSON))
		return
	}
	data := make([]embeddingItem, len(req.Input))
	for i, text := range req.Input {
		data[i] = embeddingItem{Object: "embedding", Index: i, Embedding: Embed(s.opts.Embed, text)}
	}
	writeJSON(w, map[string]any{
		"object": "list", "model": req.Model, "data": data, "usage": newUsage(totalLen(req.Input), -1),
	})
}

// rerankRequest is the Cohere/Jina-style rerank body the gateway client sends.
type rerankRequest struct {
	// Model is the cross-encoder.
	Model string `json:"model"`
	// Query is the search query.
	Query string `json:"query"`
	// Documents are the candidate texts.
	Documents []string `json:"documents"`
}

// rerankResult is one scored document.
type rerankResult struct {
	// Index is the document position.
	Index int `json:"index"`
	// RelevanceScore is the query-token overlap in [0,1].
	RelevanceScore float64 `json:"relevance_score"`
}

// serveRerank answers /rerank with every document's query-token overlap.
func (s *Server) serveRerank(w http.ResponseWriter, r *http.Request) {
	var req rerankRequest
	if err := readJSON(r, &req); err != nil {
		s.record(Call{Op: OpRerank, Status: http.StatusBadRequest})
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	call := Call{Op: OpRerank, Model: req.Model, Status: http.StatusOK}
	f := s.takeFault(OpRerank)
	call.Fault = f != nil
	if done, status := applyFault(w, r, f); done {
		call.Status = status
		s.record(call)
		return
	}
	s.record(call)
	if f != nil && f.InvalidJSON {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(invalidJSON))
		return
	}
	results := make([]rerankResult, len(req.Documents))
	for i, d := range req.Documents {
		results[i] = rerankResult{Index: i, RelevanceScore: Overlap(req.Query, d)}
	}
	writeJSON(w, map[string]any{
		"model": req.Model, "results": results,
		"usage": map[string]int{"total_tokens": newUsage(len(req.Query)+totalLen(req.Documents), -1).PromptTokens},
	})
}
