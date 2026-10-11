package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

// Report is the outcome of one run over one benchmark, for one or more targets side by side.
type Report struct {
	// Benchmark names the benchmark (locomo, longmemeval, golden, ...).
	Benchmark string `json:"benchmark"`
	// Mode is ModeReflect, ModeRecall or ModeRAG.
	Mode string `json:"mode"`
	// Targets holds one report per target, in run order.
	Targets []TargetReport `json:"targets"`
}

// TargetReport is one target's results.
type TargetReport struct {
	// Target is the target's name.
	Target string `json:"target"`
	// Conversations is how many conversations (banks) the run covers.
	Conversations int `json:"conversations"`
	// Overall aggregates every question.
	Overall CategoryStats `json:"overall"`
	// Categories has one entry per question category, sorted by name.
	Categories []CategoryStats `json:"categories"`
	// Usage is token usage summed per slot (retain, answers, background operations and the judge),
	// sorted by slot.
	Usage []Usage `json:"usage"`
	// Latency has p50/p95 per operation (retain, barrier, recall, answer, reflect, judge), sorted by
	// operation.
	Latency []LatencyStats `json:"latency"`
	// FailedOperations counts background operations that ended FAILED.
	FailedOperations int `json:"failed_operations"`
	// Pending counts questions not run yet (an interrupted run); they are not in the accuracy.
	Pending int `json:"pending,omitempty"`
	// Errors lists conversation-level problems (retain, barrier, usage collection).
	Errors []string `json:"errors,omitempty"`
	// Questions holds every graded question, in dataset order. report.json leaves it out; the full rows
	// are in results.jsonl.
	Questions []QuestionResult `json:"questions,omitempty"`
}

// CategoryStats is the accuracy of one category.
type CategoryStats struct {
	// Category is the bucket name; "overall" for the total.
	Category string `json:"category"`
	// Total is the number of questions graded (not-applicable ones excluded).
	Total int `json:"total"`
	// Correct is the number graded correct.
	Correct int `json:"correct"`
	// Errors is the number that failed before grading (they count as incorrect).
	Errors int `json:"errors"`
	// NotApplicable is the number of questions the judge could not grade in this mode; they are not in
	// Total.
	NotApplicable int `json:"not_applicable,omitempty"`
	// Accuracy is Correct / Total, 0 when Total is 0.
	Accuracy float64 `json:"accuracy"`
}

// LatencyStats summarizes the wall time of one operation.
type LatencyStats struct {
	// Op is the operation: retain, barrier, recall, answer, reflect or judge.
	Op string `json:"op"`
	// Count is the number of samples.
	Count int `json:"count"`
	// P50Ms is the median in milliseconds (nearest rank).
	P50Ms float64 `json:"p50_ms"`
	// P95Ms is the 95th percentile in milliseconds (nearest rank).
	P95Ms float64 `json:"p95_ms"`
}

// QuestionResult is one graded question: one line of results.jsonl.
type QuestionResult struct {
	// Target is the target's name.
	Target string `json:"target"`
	// Conversation is the conversation id.
	Conversation string `json:"conversation"`
	// ID is the question id.
	ID string `json:"id"`
	// Category is the report bucket.
	Category string `json:"category"`
	// Query is the question text.
	Query string `json:"query"`
	// Gold is the gold answer.
	Gold string `json:"gold"`
	// Response is the answer (reflect or rag), or the recalled memories joined by newlines (recall).
	Response string `json:"response"`
	// Reasoning is the rag answer model's explanation.
	Reasoning string `json:"reasoning,omitempty"`
	// Memories is how many memories recall returned (recall and rag modes).
	Memories int `json:"memories,omitempty"`
	// Correct is the judge's verdict.
	Correct bool `json:"correct"`
	// NotApplicable is set when the question can't be graded in this mode; it is left out of accuracy.
	NotApplicable bool `json:"not_applicable,omitempty"`
	// Reason is the judge's explanation.
	Reason string `json:"reason,omitempty"`
	// Error is set when the question failed before grading.
	Error string `json:"error,omitempty"`
	// LatencyMs is the wall time per operation (recall, answer, reflect, judge) in milliseconds.
	LatencyMs map[string]float64 `json:"latency_ms,omitempty"`
	// Usage is the token usage of answering and grading this question.
	Usage []Usage `json:"usage,omitempty"`
	// At is when the question finished.
	At time.Time `json:"at"`
}

// WriteJSON writes the report as indented JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("harness: write json report: %w", err)
	}
	return nil
}

// usageSum sums usage per slot.
type usageSum map[string]Usage

func (s usageSum) add(us []Usage) {
	for _, u := range us {
		acc := s[u.Slot]
		acc.Slot = u.Slot
		acc.InputTokens += u.InputTokens
		acc.OutputTokens += u.OutputTokens
		s[u.Slot] = acc
	}
}

func (s usageSum) sorted() []Usage {
	out := make([]Usage, 0, len(s))
	for _, u := range s {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// Report builds target's report over convs from everything journaled, including earlier attempts of a
// resumed run: the latest result of each question counts; token usage counts every attempt (it was
// spent); background usage is the latest cumulative read per bank.
func (j *Journal) Report(target string, convs []Conversation) TargetReport {
	j.mu.Lock()
	defer j.mu.Unlock()
	inRun := map[string]bool{}
	for _, c := range convs {
		inRun[c.ID] = true
	}
	usage, lat := usageSum{}, map[string][]float64{}
	rep := TargetReport{Target: target, Conversations: len(convs), Questions: []QuestionResult{}}
	rep.FailedOperations, rep.Errors = j.stepStats(target, inRun, usage, lat)
	latest := map[[2]string]QuestionResult{}
	for _, q := range j.answers {
		if q.Target == target && inRun[q.Conversation] {
			latest[[2]string{q.Conversation, q.ID}] = q
			usage.add(q.Usage)
		}
	}
	for _, c := range convs {
		for _, q := range c.Questions {
			res, ok := latest[[2]string{c.ID, q.ID}]
			if !ok {
				rep.Pending++
				continue
			}
			rep.Questions = append(rep.Questions, res)
			for op, ms := range res.LatencyMs {
				lat[op] = append(lat[op], ms)
			}
		}
	}
	rep.Overall, rep.Categories = categoryStats(rep.Questions)
	rep.Usage = usage.sorted()
	for op, ms := range lat {
		rep.Latency = append(rep.Latency, latencyStats(op, ms))
	}
	sort.Slice(rep.Latency, func(i, k int) bool { return rep.Latency[i].Op < rep.Latency[k].Op })
	return rep
}

// stepStats folds target's progress steps for the conversations in inRun into usage and lat, and returns
// the failed background operations and the conversation errors still standing (an ingest failure is
// cleared by a later settle, a background failure by a later background read).
func (j *Journal) stepStats(target string, inRun map[string]bool, usage usageSum,
	lat map[string][]float64,
) (int, []string) {
	background := map[string]Step{}
	failures := map[[2]string]string{}
	for _, s := range j.steps {
		if s.Target != target || !inRun[s.Conversation] {
			continue
		}
		switch s.Kind {
		case StepRetained:
			usage.add(s.Usage)
			lat[opRetain] = append(lat[opRetain], s.Ms)
		case StepSettled:
			lat[opBarrier] = append(lat[opBarrier], s.Ms)
			delete(failures, [2]string{s.Conversation, stageIngest})
		case StepBackground:
			background[s.Conversation] = s
			delete(failures, [2]string{s.Conversation, stageBackground})
		case StepFailed:
			failures[[2]string{s.Conversation, s.Stage}] = s.Conversation + ": " + s.Error
		}
	}
	failed := 0
	for _, s := range background {
		usage.add(s.Usage)
		failed += s.FailedOperations
	}
	errs := make([]string, 0, len(failures))
	for _, e := range failures {
		errs = append(errs, e)
	}
	sort.Strings(errs)
	return failed, errs
}

// categoryStats returns the overall and per-category accuracy of results.
func categoryStats(results []QuestionResult) (CategoryStats, []CategoryStats) {
	overall := CategoryStats{Category: "overall"}
	byCat := map[string]*CategoryStats{}
	for _, q := range results {
		c, ok := byCat[q.Category]
		if !ok {
			c = &CategoryStats{Category: q.Category}
			byCat[q.Category] = c
		}
		for _, s := range []*CategoryStats{&overall, c} {
			if q.NotApplicable {
				s.NotApplicable++
				continue
			}
			s.Total++
			if q.Correct {
				s.Correct++
			}
			if q.Error != "" {
				s.Errors++
			}
		}
	}
	cats := make([]CategoryStats, 0, len(byCat))
	for _, c := range byCat {
		c.Accuracy = accuracy(*c)
		cats = append(cats, *c)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i].Category < cats[j].Category })
	overall.Accuracy = accuracy(overall)
	return overall, cats
}

func accuracy(c CategoryStats) float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Correct) / float64(c.Total)
}

// latencyStats computes nearest-rank p50 and p95 of ms.
func latencyStats(op string, ms []float64) LatencyStats {
	sorted := append([]float64(nil), ms...)
	sort.Float64s(sorted)
	return LatencyStats{Op: op, Count: len(sorted), P50Ms: percentile(sorted, 50), P95Ms: percentile(sorted, 95)}
}

// percentile returns the nearest-rank p-th percentile of sorted (ascending); 0 when sorted is empty.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(1, min(rank, len(sorted)))
	return sorted[rank-1]
}

// ms converts d to milliseconds.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
