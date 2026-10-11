package fake

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"time"
	"unicode"
)

// BM25 parameters of the keyword arm (the usual Lucene / pg_search defaults).
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// admit reports whether a live unit passes an arm's bank, fact-type, tag and seq filters.
func admit(u domain.Unit, b port.BankRef, types []domain.FactType, tags *port.SQLPredicate, seq port.SeqRange) bool {
	return u.Bank == b && (len(types) == 0 || slices.Contains(types, u.Type)) && tags.Eval(u.Tags) &&
		seq.Contains(u.Seq)
}

// rank sorts hits best first (ties by id, for determinism) and applies limit (<= 0 = no limit).
func rank(hs []port.Hit, limit int) []port.Hit {
	slices.SortFunc(hs, func(x, y port.Hit) int { return cmp.Or(cmp.Compare(y.Score, x.Score), cmp.Compare(x.ID, y.ID)) })
	return capped(hs, limit)
}

// searchVector is the semantic arm: cosine similarity to q.Embedding over units with an embedding, at or
// above MinSimilarity when it is set. A query of the wrong dimension is domain.ErrInvalidArgument.
func searchVector(ctx context.Context, st *state, q port.VectorQuery) ([]port.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	if len(q.Embedding) != domain.EmbeddingDim {
		return nil, fmt.Errorf("%w: query embedding has %d dims, want %d", domain.ErrInvalidArgument,
			len(q.Embedding), domain.EmbeddingDim)
	}
	var hs []port.Hit
	for id, row := range st.units {
		if len(row.u.Embedding) == 0 || !admit(row.u, q.Bank, q.Types, q.Tags, q.Seq) {
			continue
		}
		sim := cosine(q.Embedding, row.u.Embedding)
		if q.MinSimilarity != 0 && sim < q.MinSimilarity {
			continue
		}
		hs = append(hs, port.Hit{ID: id, Score: sim})
	}
	return rank(hs, q.Limit), nil
}

// cosine returns the cosine similarity of a and b (0 when either is a zero vector).
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range min(len(a), len(b)) {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// tokens splits text into lowercase letter-or-digit words.
func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// searchText is the keyword arm: Okapi BM25 of q.Text over unit text, with document frequencies taken
// over the bank's live units. Only units matching at least one query term (and at or above MinScore) are
// hits.
func searchText(ctx context.Context, st *state, q port.TextQuery) ([]port.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	terms := slices.Compact(slices.Sorted(slices.Values(tokens(q.Text))))
	docs := map[port.UnitID][]string{}
	df := map[string]int{}
	total := 0
	for id, row := range st.units {
		if row.u.Bank != q.Bank {
			continue
		}
		toks := tokens(row.u.Text)
		docs[id] = toks
		total += len(toks)
		for _, term := range slices.Compact(slices.Sorted(slices.Values(toks))) {
			df[term]++
		}
	}
	if len(docs) == 0 || len(terms) == 0 {
		return nil, nil
	}
	n, avgdl := float64(len(docs)), math.Max(float64(total)/float64(len(docs)), 1)
	var hs []port.Hit
	for id, toks := range docs {
		if !admit(st.units[id].u, q.Bank, q.Types, q.Tags, q.Seq) {
			continue
		}
		score := 0.0
		for _, term := range terms {
			tf := float64(countOf(toks, term))
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[term])+0.5)/(float64(df[term])+0.5))
			score += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*float64(len(toks))/avgdl))
		}
		if score > 0 && (q.MinScore == 0 || score >= q.MinScore) {
			hs = append(hs, port.Hit{ID: id, Score: score})
		}
	}
	return rank(hs, q.Limit), nil
}

// countOf counts occurrences of s in xs.
func countOf(xs []string, s string) int {
	n := 0
	for _, x := range xs {
		if x == s {
			n++
		}
	}
	return n
}

// occurrence returns the unit's occurrence interval: occurred_start..occurred_end, either bound standing
// in for a missing other, falling back to mentioned_at; ok is false for timeless or undated units.
func occurrence(u domain.Unit) (start, end time.Time, ok bool) {
	if u.Timeless {
		return start, end, false
	}
	s, e := u.OccurredStart, u.OccurredEnd
	if s == nil {
		s = e
	}
	if e == nil {
		e = s
	}
	if s == nil {
		s, e = u.MentionedAt, u.MentionedAt
	}
	if s == nil {
		return start, end, false
	}
	return *s, *e, true
}

// temporalHit is a temporal-arm candidate with the midpoint used for bucketing.
type temporalHit struct {
	hit port.Hit
	mid time.Time
}

// searchTemporal is the temporal arm: units whose occurrence overlaps the half-open window (and that pass
// the relevance filter, see relevant), scored by the proximity of their midpoint to the window's centre
// (1 / (1 + distance in days)). With Buckets > 1 and both bounds set, hits are taken round-robin from that
// many equal sub-windows.
func searchTemporal(ctx context.Context, st *state, q port.TemporalQuery) ([]port.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	w := q.Window
	if w.Start.IsZero() && w.End.IsZero() {
		return nil, fmt.Errorf("%w: temporal window has no bound", domain.ErrInvalidArgument)
	}
	centre := windowCentre(w)
	var hs []temporalHit
	for _, row := range st.units {
		if h, ok := temporalCandidate(row.u, q, centre); ok {
			hs = append(hs, h)
		}
	}
	if q.Buckets > 1 && !w.Start.IsZero() && !w.End.IsZero() && q.Limit > 0 && len(hs) > q.Limit {
		hs = spreadBuckets(hs, w, q.Buckets, q.Limit)
	}
	out := make([]port.Hit, len(hs))
	for i, h := range hs {
		out[i] = h.hit
	}
	return rank(out, q.Limit), nil
}

// temporalCandidate scores u when it passes q's filters and overlaps q's window.
func temporalCandidate(u domain.Unit, q port.TemporalQuery, centre time.Time) (temporalHit, bool) {
	w := q.Window
	start, end, ok := occurrence(u)
	if !ok || !admit(u, q.Bank, q.Types, q.Tags, q.Seq) ||
		(!w.End.IsZero() && !start.Before(w.End)) || (!w.Start.IsZero() && end.Before(w.Start)) {
		return temporalHit{}, false
	}
	if !relevant(u, q) {
		return temporalHit{}, false
	}
	mid := start.Add(end.Sub(start) / 2)
	days := math.Abs(mid.Sub(centre).Hours()) / 24
	return temporalHit{port.Hit{ID: u.ID, Score: 1 / (1 + days)}, mid}, true
}

// relevant applies the temporal arm's relevance filter: on only when q has an Embedding and MinSimilarity > 0,
// it then drops units with no embedding and those whose cosine similarity to q.Embedding is below
// MinSimilarity. The hit score stays the temporal score.
func relevant(u domain.Unit, q port.TemporalQuery) bool {
	if len(q.Embedding) == 0 || q.MinSimilarity <= 0 {
		return true
	}
	return len(u.Embedding) > 0 && cosine(u.Embedding, q.Embedding) >= q.MinSimilarity
}

// windowCentre is the reference time of proximity scores: the centre of a closed window, else its bound.
func windowCentre(w domain.TimeWindow) time.Time {
	switch {
	case w.Start.IsZero():
		return w.End
	case w.End.IsZero():
		return w.Start
	default:
		return w.Start.Add(w.End.Sub(w.Start) / 2)
	}
}

// spreadBuckets picks limit hits round-robin across n equal sub-windows of w, best first in each.
func spreadBuckets(hs []temporalHit, w domain.TimeWindow, n, limit int) []temporalHit {
	buckets := make([][]temporalHit, n)
	width := w.End.Sub(w.Start)
	for _, h := range hs {
		i := int(float64(h.mid.Sub(w.Start)) / float64(width) * float64(n))
		i = min(max(i, 0), n-1)
		buckets[i] = append(buckets[i], h)
	}
	for _, b := range buckets {
		slices.SortFunc(b, func(x, y temporalHit) int {
			return cmp.Or(cmp.Compare(y.hit.Score, x.hit.Score), cmp.Compare(x.hit.ID, y.hit.ID))
		})
	}
	var out []temporalHit
	for round := 0; len(out) < limit; round++ {
		took := false
		for _, b := range buckets {
			if round < len(b) && len(out) < limit {
				out, took = append(out, b[round]), true
			}
		}
		if !took {
			break
		}
	}
	return out
}

// expandGraph is the graph arm: it follows outgoing links (of Kinds, when set) of q.Bank from the live
// seeds for up to MaxHops hops. A reached unit's score is the best product of edge weights over its paths;
// seeds are never hits and the Types, Tags and Seq filters apply to reached units.
func expandGraph(ctx context.Context, st *state, q port.GraphQuery) ([]port.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	seeds := map[port.UnitID]bool{}
	frontier := map[port.UnitID]float64{}
	for _, id := range q.Seeds {
		if row, ok := st.units[id]; ok && row.u.Bank == q.Bank {
			seeds[id], frontier[id] = true, 1
		}
	}
	best := map[port.UnitID]float64{}
	for hop := 0; hop < q.MaxHops && len(frontier) > 0; hop++ {
		next := st.hop(q, seeds, frontier)
		frontier = map[port.UnitID]float64{}
		for id, s := range next {
			if old, ok := best[id]; !ok || s > old {
				best[id], frontier[id] = s, s
			}
		}
	}
	var hs []port.Hit
	for id, s := range best {
		if row, ok := st.units[id]; ok && admit(row.u, q.Bank, q.Types, q.Tags, q.Seq) {
			hs = append(hs, port.Hit{ID: id, Score: s})
		}
	}
	return rank(hs, q.Limit), nil
}

// hop follows one step of q's links out of frontier (unit -> path score) and returns the best score of
// every non-seed unit reached.
func (s *state) hop(
	q port.GraphQuery, seeds map[port.UnitID]bool, frontier map[port.UnitID]float64,
) map[port.UnitID]float64 {
	next := map[port.UnitID]float64{}
	for k, row := range s.links {
		w, ok := frontier[k.from]
		if !ok || seeds[k.to] || row.l.Bank != q.Bank || (len(q.Kinds) > 0 && !slices.Contains(q.Kinds, k.kind)) {
			continue
		}
		if score, seen := next[k.to]; !seen || w*row.l.Weight > score {
			next[k.to] = w * row.l.Weight
		}
	}
	return next
}
