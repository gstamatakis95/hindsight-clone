package harness

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// TargetDelta compares one target's results across two reports (A, the baseline, and B).
type TargetDelta struct {
	// Target is the target's name.
	Target string
	// Categories has the overall row first, then every category of either run, sorted by name.
	Categories []CategoryDelta
	// Usage has every slot of either run, sorted by slot.
	Usage []UsageDelta
	// Latency has every operation of either run, sorted by operation.
	Latency []LatencyDelta
}

// CategoryDelta is one category's accuracy in both runs.
type CategoryDelta struct {
	// Category is the bucket name.
	Category string
	// A and B are the category's stats; nil when the run has no such category.
	A, B *CategoryStats
	// DeltaPP is B's accuracy minus A's, in percentage points; 0 unless both runs have the category.
	DeltaPP float64
}

// UsageDelta is one slot's token usage in both runs (zero when a run has no such slot).
type UsageDelta struct {
	// Slot is the model slot.
	Slot string
	// A and B are the slot's usage.
	A, B Usage
}

// LatencyDelta is one operation's latency in both runs (zero when a run has no such operation).
type LatencyDelta struct {
	// Op is the operation.
	Op string
	// A and B are the operation's latency.
	A, B LatencyStats
}

// Compare pairs the targets the two reports share by name and compares them.
func Compare(a, b Report) []TargetDelta {
	var out []TargetDelta
	for _, ta := range a.Targets {
		for _, tb := range b.Targets {
			if ta.Target == tb.Target {
				out = append(out, compareTarget(ta, tb))
			}
		}
	}
	return out
}

func compareTarget(a, b TargetReport) TargetDelta {
	d := TargetDelta{Target: a.Target}
	oa, ob := a.Overall, b.Overall
	d.Categories = append(d.Categories, categoryDelta("overall", &oa, &ob))
	ca, cb := byKey(a.Categories, catKey), byKey(b.Categories, catKey)
	for _, name := range unionKeys(ca, cb) {
		d.Categories = append(d.Categories, categoryDelta(name, ptr(ca, name), ptr(cb, name)))
	}
	slotKey := func(u Usage) string { return u.Slot }
	ua, ub := byKey(a.Usage, slotKey), byKey(b.Usage, slotKey)
	for _, slot := range unionKeys(ua, ub) {
		d.Usage = append(d.Usage, UsageDelta{Slot: slot, A: ua[slot], B: ub[slot]})
	}
	opKey := func(l LatencyStats) string { return l.Op }
	la, lb := byKey(a.Latency, opKey), byKey(b.Latency, opKey)
	for _, op := range unionKeys(la, lb) {
		d.Latency = append(d.Latency, LatencyDelta{Op: op, A: la[op], B: lb[op]})
	}
	return d
}

func catKey(c CategoryStats) string { return c.Category }

func categoryDelta(name string, a, b *CategoryStats) CategoryDelta {
	d := CategoryDelta{Category: name, A: a, B: b}
	if a != nil && b != nil {
		d.DeltaPP = 100 * (b.Accuracy - a.Accuracy)
	}
	return d
}

func byKey[T any](items []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(items))
	for _, it := range items {
		m[key(it)] = it
	}
	return m
}

func ptr[T any](m map[string]T, k string) *T {
	if v, ok := m[k]; ok {
		return &v
	}
	return nil
}

func unionKeys[T any](a, b map[string]T) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]T{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// WriteComparison writes Compare(a, b) as Markdown tables: accuracy with the delta in percentage points,
// total tokens per slot with the relative change, and p50/p95 latency with the relative change.
func WriteComparison(w io.Writer, labelA, labelB string, a, b Report) error {
	var s strings.Builder
	fmt.Fprintf(&s, "# Comparison: %s vs %s\n\nA = %s (%s, %s)\n\nB = %s (%s, %s)\n\n",
		labelA, labelB, labelA, a.Benchmark, a.Mode, labelB, b.Benchmark, b.Mode)
	deltas := Compare(a, b)
	if len(deltas) == 0 {
		s.WriteString("The runs share no target.\n")
	}
	for _, d := range deltas {
		fmt.Fprintf(&s, "## %s: accuracy\n\n| Category | A | B | Δ (pp) |\n|---|---|---|---|\n", d.Target)
		for _, c := range d.Categories {
			delta := "-"
			if c.A != nil && c.B != nil {
				delta = fmt.Sprintf("%+.1f", c.DeltaPP)
			}
			writeRow(&s, []string{c.Category, accCell(c.A), accCell(c.B), delta})
		}
		fmt.Fprintf(&s, "\n## %s: tokens (input + output)\n\n| Slot | A | B | Δ |\n|---|---|---|---|\n", d.Target)
		for _, u := range d.Usage {
			ta, tb := u.A.InputTokens+u.A.OutputTokens, u.B.InputTokens+u.B.OutputTokens
			writeRow(&s, []string{u.Slot, fmt.Sprint(ta), fmt.Sprint(tb), relChange(float64(ta), float64(tb))})
		}
		fmt.Fprintf(&s, "\n## %s: latency (ms)\n\n| Operation | p50 A | p50 B | Δ p50 | p95 A | p95 B | Δ p95 |\n"+
			"|---|---|---|---|---|---|---|\n", d.Target)
		for _, l := range d.Latency {
			writeRow(&s, []string{
				l.Op, fmt.Sprintf("%.1f", l.A.P50Ms), fmt.Sprintf("%.1f", l.B.P50Ms), relChange(l.A.P50Ms, l.B.P50Ms),
				fmt.Sprintf("%.1f", l.A.P95Ms), fmt.Sprintf("%.1f", l.B.P95Ms), relChange(l.A.P95Ms, l.B.P95Ms),
			})
		}
		s.WriteString("\n")
	}
	if _, err := io.WriteString(w, s.String()); err != nil {
		return fmt.Errorf("harness: write comparison: %w", err)
	}
	return nil
}

func accCell(c *CategoryStats) string {
	if c == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*c.Accuracy, c.Correct, c.Total)
}

// relChange renders the change from a to b as a percentage of a; "-" when a is zero.
func relChange(a, b float64) string {
	if a == 0 {
		return "-"
	}
	return fmt.Sprintf("%+.1f%%", 100*(b-a)/a)
}
