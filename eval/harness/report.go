package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"
)

// Report is the outcome of one eval run over one dataset, for one or more targets side by side.
type Report struct {
	// Dataset names the benchmark (longmemeval, locomo, golden).
	Dataset string `json:"dataset"`
	// Mode is ModeReflect or ModeRecall.
	Mode string `json:"mode"`
	// Targets holds one report per target, in run order.
	Targets []TargetReport `json:"targets"`
}

// TargetReport is one target's results.
type TargetReport struct {
	// Target is the target's name.
	Target string `json:"target"`
	// Conversations is how many conversations (banks) were run.
	Conversations int `json:"conversations"`
	// Overall aggregates every question.
	Overall CategoryStats `json:"overall"`
	// Categories has one entry per question category, sorted by name.
	Categories []CategoryStats `json:"categories"`
	// Usage is token usage summed per slot (retain, reflect, background operations and the judge),
	// sorted by slot.
	Usage []Usage `json:"usage"`
	// Latency has p50/p95 per operation (retain, barrier, recall or reflect, judge), sorted by operation.
	Latency []LatencyStats `json:"latency"`
	// FailedOperations counts background operations that ended FAILED.
	FailedOperations int `json:"failed_operations"`
	// Errors lists conversation-level problems (retain, barrier, usage collection).
	Errors []string `json:"errors,omitempty"`
	// Questions holds every graded question, in dataset order.
	Questions []QuestionResult `json:"questions"`
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
	// Op is the operation: retain, barrier, recall, reflect or judge.
	Op string `json:"op"`
	// Count is the number of samples.
	Count int `json:"count"`
	// P50Ms is the median in milliseconds (nearest rank).
	P50Ms float64 `json:"p50_ms"`
	// P95Ms is the 95th percentile in milliseconds (nearest rank).
	P95Ms float64 `json:"p95_ms"`
}

// QuestionResult is one graded question.
type QuestionResult struct {
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
	// Response is the reflect answer, or the recalled memories joined by newlines.
	Response string `json:"response"`
	// Correct is the judge's verdict.
	Correct bool `json:"correct"`
	// NotApplicable is set when the question can't be graded in this mode; it is left out of accuracy.
	NotApplicable bool `json:"not_applicable,omitempty"`
	// Reason is the judge's explanation.
	Reason string `json:"reason,omitempty"`
	// Error is set when the question failed before grading.
	Error string `json:"error,omitempty"`
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

// recorder collects results from concurrently running conversations.
type recorder struct {
	mu        sync.Mutex
	results   [][]QuestionResult
	latencies map[string][]time.Duration
	usage     map[string]Usage
	failed    int
	errors    []string
}

func newRecorder(n int) *recorder {
	return &recorder{
		results:   make([][]QuestionResult, n),
		latencies: map[string][]time.Duration{},
		usage:     map[string]Usage{},
	}
}

func (r *recorder) observe(op string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latencies[op] = append(r.latencies[op], d)
}

func (r *recorder) addUsage(us []Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range us {
		acc := r.usage[u.Slot]
		acc.Slot = u.Slot
		acc.InputTokens += u.InputTokens
		acc.OutputTokens += u.OutputTokens
		r.usage[u.Slot] = acc
	}
}

func (r *recorder) addBackground(bg BackgroundStats) {
	r.addUsage(bg.Usage)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed += bg.Failed
}

func (r *recorder) addError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, msg)
}

func (r *recorder) setResults(i int, res []QuestionResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[i] = res
}

// failConversation marks every question of c as failed with err.
func (r *recorder) failConversation(i int, c Conversation, err error) {
	res := make([]QuestionResult, 0, len(c.Questions))
	for _, q := range c.Questions {
		res = append(res, QuestionResult{
			Conversation: c.ID, ID: q.ID, Category: q.Category, Query: q.Query, Gold: q.Answer,
			Error: err.Error(),
		})
	}
	r.setResults(i, res)
	r.addError(fmt.Sprintf("%s: %v", c.ID, err))
}

// report builds the target's report from everything recorded.
func (r *recorder) report(target string, conversations int) TargetReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	rep := TargetReport{
		Target: target, Conversations: conversations, FailedOperations: r.failed,
		Errors: append([]string(nil), r.errors...), Questions: []QuestionResult{},
	}
	sort.Strings(rep.Errors)
	for _, res := range r.results {
		rep.Questions = append(rep.Questions, res...)
	}
	rep.Overall, rep.Categories = categoryStats(rep.Questions)
	for _, u := range r.usage {
		rep.Usage = append(rep.Usage, u)
	}
	sort.Slice(rep.Usage, func(i, j int) bool { return rep.Usage[i].Slot < rep.Usage[j].Slot })
	for op, ds := range r.latencies {
		rep.Latency = append(rep.Latency, latencyStats(op, ds))
	}
	sort.Slice(rep.Latency, func(i, j int) bool { return rep.Latency[i].Op < rep.Latency[j].Op })
	return rep
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

// latencyStats computes nearest-rank p50 and p95 of ds.
func latencyStats(op string, ds []time.Duration) LatencyStats {
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return LatencyStats{
		Op: op, Count: len(sorted),
		P50Ms: ms(Percentile(sorted, 50)), P95Ms: ms(Percentile(sorted, 95)),
	}
}

// Percentile returns the nearest-rank p-th percentile of sorted (ascending); 0 when sorted is empty.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = max(1, min(rank, len(sorted)))
	return sorted[rank-1]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
