package arms

import (
	"memd/internal/recall"
	"memd/internal/store"
)

// hits converts store hits (best first) to recall hits in the same order.
func hits(hs []store.Hit) []recall.Hit {
	out := make([]recall.Hit, len(hs))
	for i, h := range hs {
		out[i] = recall.Hit{ID: h.ID, Score: h.Score}
	}
	return out
}
