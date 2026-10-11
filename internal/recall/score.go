package recall

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/domain"
	"slices"
	"time"
)

// boosts are the multipliers the scorer applied to a candidate's base score; 1 is neutral. Each is
// 1 + alpha * (signal - 0.5) with the signal in [0, 1], so a boost moves the score by at most alpha / 2.
type boosts struct {
	// Recency favours recent units (recency_alpha, linear decay over recency_horizon_days).
	Recency float64
	// Temporal favours units close to the middle of the query's time window (temporal_alpha, Hindsight's
	// linear proximity; neutral for units the temporal arm did not return).
	Temporal float64
	// Proof favours observations with many supporting facts (proof_alpha, log of proof_count).
	Proof float64
}

// scorer applies the cross-encoder, then recency, temporal and proof boosts.
type scorer struct {
	rr     ai.Reranker
	router *ai.Router
	clock  clock.Clock
}

// newScorer returns the scorer. rr may be nil, which behaves as recall.rerank = false.
func newScorer(rr ai.Reranker, router *ai.Router, clock clock.Clock) *scorer {
	return &scorer{rr: rr, router: router, clock: clock}
}

// Score keeps the top reranker_max_candidates of cs (already trimmed and hydrated by the caller), scores them with
// the cross-encoder (RERANK slot) when recall.rerank is on, else seeds the base score from the unboosted RRF rank
// (1.0 down to 0.1, as Hindsight does for a passthrough reranker), multiplies in the boosts, sets candidate.Final
// and returns the kept candidates ordered by it, best first.
func (s *scorer) Score(ctx context.Context, q *parsedQuery, cs []candidate) ([]candidate, error) {
	set := settingsOf(q.Config)
	if set.rerankMax > 0 && len(cs) > set.rerankMax {
		cs = cs[:set.rerankMax]
	}
	out := slices.Clone(cs)
	if len(out) == 0 {
		return out, nil
	}
	if set.rerank && s.rr != nil {
		if err := s.rerank(ctx, q, out); err != nil {
			return nil, err
		}
	} else {
		passthroughBase(out)
	}
	for i := range out {
		out[i].Boosts = boost(out[i], q, set)
		out[i].Final *= out[i].Boosts.Recency * out[i].Boosts.Temporal * out[i].Boosts.Proof
	}
	slices.SortStableFunc(out, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.Final, a.Final), cmp.Compare(b.RRF, a.RRF), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// rerank sets candidate.Reranker and the base score (Final) of every candidate from the cross-encoder.
func (s *scorer) rerank(ctx context.Context, q *parsedQuery, cs []candidate) error {
	binding, err := s.router.Resolve(q.Config, domain.ModelSlotRerank)
	if err != nil {
		return fmt.Errorf("recall: rerank binding: %w", err)
	}
	docs := make([]string, len(cs))
	for i, c := range cs {
		docs[i] = rerankDoc(c.Unit)
	}
	scores, err := s.rr.Rerank(ctx, binding, q.Text, docs)
	if err != nil {
		return err
	}
	if len(scores) != len(cs) {
		return fmt.Errorf("%w: recall: reranker returned %d scores for %d documents", domain.ErrUnavailable,
			len(scores), len(cs))
	}
	for i := range cs {
		v := float64(scores[i])
		if math.IsNaN(v) {
			v = 0
		}
		cs[i].Reranker = &v
		cs[i].Final = v
	}
	return nil
}

// rerankDoc is the text the cross-encoder sees: Hindsight's "[Date: June 05, 2022 (2022-06-05)] context: text".
func rerankDoc(u *domain.Unit) string {
	if u == nil {
		return ""
	}
	doc := u.Text
	if u.Context != "" {
		doc = u.Context + ": " + doc
	}
	if u.OccurredStart != nil {
		d := u.OccurredStart.UTC()
		doc = fmt.Sprintf("[Date: %s (%s)] %s", d.Format("January 02, 2006"), d.Format(time.DateOnly), doc)
	}
	return doc
}

// passthroughBase orders cs by their unboosted RRF score (strategy boosts only decide the cap trim, as in
// Hindsight) and seeds the base score from that rank: 1.0 for the first down to 0.1 for the last, so the
// boosts modulate relevance instead of replacing it.
func passthroughBase(cs []candidate) {
	slices.SortStableFunc(cs, func(a, b candidate) int { return cmp.Compare(b.RRF, a.RRF) })
	denom := float64(max(1, len(cs)-1))
	for i := range cs {
		cs[i].Reranker = nil
		cs[i].Final = 1 - 0.9*float64(i)/denom
	}
}

// boost returns the recency, temporal and proof multipliers of c under the settings s.
func boost(c candidate, q *parsedQuery, s settings) boosts {
	temporal := 0.5
	if _, ok := c.Arms[ArmTemporal]; ok && c.Unit != nil && q.Window != nil {
		temporal = temporalProximity(*c.Unit, *q.Window, s.recencyHorizonDays)
	}
	recency, proof := 0.5, 0.5
	if c.Unit != nil {
		recency = recencySignal(*c.Unit, q.At, s.recencyHorizonDays)
		proof = proofSignal(*c.Unit)
	}
	return boosts{
		Recency:  1 + s.recencyAlpha*(recency-0.5),
		Temporal: 1 + s.temporalAlpha*(temporal-0.5),
		Proof:    1 + s.proofAlpha*(proof-0.5),
	}
}

// temporalProximity is Hindsight's linear proximity of a unit to the query window (pg/recall.py):
// 1 - min(|best - mid| / half, 1), where best is the unit's occurrence midpoint, else occurred_start, else
// occurred_end, else mentioned_at; mid is the window's midpoint and half its half-width in days. An undated
// unit is neutral (0.5) and a zero-length window gives 1. A window with one open bound anchors on the set
// bound with horizonDays as the scale (decision 901).
func temporalProximity(u domain.Unit, w domain.TimeWindow, horizonDays int) float64 {
	best := bestDate(u)
	if best == nil {
		return 0.5
	}
	var mid time.Time
	var half float64
	switch {
	case !w.Start.IsZero() && !w.End.IsZero():
		mid = w.Start.Add(w.End.Sub(w.Start) / 2)
		half = w.End.Sub(w.Start).Hours() / 24 / 2
		if half <= 0 {
			return 1
		}
	case !w.Start.IsZero():
		mid, half = w.Start, float64(horizonDays)
	case !w.End.IsZero():
		mid, half = w.End, float64(horizonDays)
	default:
		return 0.5
	}
	days := math.Abs(best.Sub(mid).Hours() / 24)
	return 1 - min(days/half, 1)
}

// bestDate is the instant a unit is placed at for temporal proximity: the occurrence midpoint, else
// occurred_start, else occurred_end, else mentioned_at; nil when the unit has none.
func bestDate(u domain.Unit) *time.Time {
	switch {
	case u.OccurredStart != nil && u.OccurredEnd != nil:
		m := u.OccurredStart.Add(u.OccurredEnd.Sub(*u.OccurredStart) / 2)
		return &m
	case u.OccurredStart != nil:
		return u.OccurredStart
	case u.OccurredEnd != nil:
		return u.OccurredEnd
	default:
		return u.MentionedAt
	}
}

// recencySignal maps a unit's age at at onto [0.1, 1] (0.5 = neutral) with Hindsight's linear decay over
// horizonDays. Timeless and undated units are neutral. A unit dated to a whole calendar month or year is aged
// from the end of that period and capped at neutral (Hindsight issue #3893); otherwise the effective instant
// is occurred_start, else mentioned_at, else occurred_end.
func recencySignal(u domain.Unit, at time.Time, horizonDays int) float64 {
	if u.Timeless {
		return 0.5
	}
	if u.OccurredStart != nil && u.OccurredEnd != nil && calendarPeriod(*u.OccurredStart, *u.OccurredEnd) {
		return min(0.5, linearDecay(at.Sub(*u.OccurredEnd), horizonDays))
	}
	eff := u.OccurredStart
	if eff == nil {
		eff = u.MentionedAt
	}
	if eff == nil {
		eff = u.OccurredEnd
	}
	if eff == nil {
		return 0.5
	}
	return linearDecay(at.Sub(*eff), horizonDays)
}

// linearDecay is 1 for age <= 0, falling linearly to a floor of 0.1 at horizonDays.
func linearDecay(age time.Duration, horizonDays int) float64 {
	days := age.Hours() / 24
	return max(0.1, min(1, 1-days/float64(horizonDays)))
}

// calendarPeriod reports whether [start, end] spans exactly one calendar month or year of start, up to one
// day short (extraction spells a period's last instant several ways).
func calendarPeriod(start, end time.Time) bool {
	span := end.Sub(start)
	if span <= 0 {
		return false
	}
	s := start.UTC()
	monthStart := time.Date(s.Year(), s.Month(), 1, 0, 0, 0, 0, time.UTC)
	month := monthStart.AddDate(0, 1, 0).Sub(monthStart)
	yearStart := time.Date(s.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	year := yearStart.AddDate(1, 0, 0).Sub(yearStart)
	for _, p := range []time.Duration{month, year} {
		if span >= p-24*time.Hour && span <= p {
			return true
		}
	}
	return false
}

// proofSignal maps an observation's proof count onto [0, 1] (0.5 + ln(count) / 10, clamped); facts and
// observations without a count are neutral.
func proofSignal(u domain.Unit) float64 {
	if u.Type != domain.FactTypeObservation || u.ProofCount < 1 {
		return 0.5
	}
	return min(1, max(0, 0.5+math.Log(float64(u.ProofCount))/10))
}
