package harness

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// maxListedErrors caps the problems listed per target in the Markdown report; the JSON has them all.
const maxListedErrors = 20

// WriteMarkdown writes the report as Markdown, with one column per target so runs against memd and
// stock Hindsight read side by side.
func (r Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval report: %s (%s)\n\n", r.Dataset, r.Mode)
	r.accuracyTable(&b)
	r.usageTable(&b)
	r.latencyTable(&b)
	r.problems(&b)
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("harness: write markdown report: %w", err)
	}
	return nil
}

// header writes a table header: first, then one or more columns per target.
func (r Report) header(b *strings.Builder, first string, perTarget ...string) {
	cols := []string{first}
	for _, t := range r.Targets {
		for _, c := range perTarget {
			cols = append(cols, strings.TrimSpace(t.Target+" "+c))
		}
	}
	fmt.Fprintf(b, "| %s |\n|%s\n", strings.Join(cols, " | "), strings.Repeat("---|", len(cols)))
}

func (r Report) accuracyTable(b *strings.Builder) {
	b.WriteString("## Accuracy by category\n\n")
	r.header(b, "Category", "")
	cell := func(c CategoryStats) string {
		s := fmt.Sprintf("%.1f%% (%d/%d)", 100*c.Accuracy, c.Correct, c.Total)
		if c.Errors > 0 {
			s += fmt.Sprintf(", %d errors", c.Errors)
		}
		if c.NotApplicable > 0 {
			s += fmt.Sprintf(", %d n/a", c.NotApplicable)
		}
		return s
	}
	row := []string{"**overall**"}
	for _, t := range r.Targets {
		row = append(row, "**"+cell(t.Overall)+"**")
	}
	writeRow(b, row)
	for _, name := range r.keys(func(t TargetReport) []string {
		names := make([]string, 0, len(t.Categories))
		for _, c := range t.Categories {
			names = append(names, c.Category)
		}
		return names
	}) {
		row = []string{name}
		for _, t := range r.Targets {
			row = append(row, cellOr(t.Categories, name, func(c CategoryStats) string { return c.Category }, cell))
		}
		writeRow(b, row)
	}
	b.WriteString("\n")
}

func (r Report) usageTable(b *strings.Builder) {
	b.WriteString("## Token usage per slot (input / output)\n\n")
	r.header(b, "Slot", "")
	for _, slot := range r.keys(func(t TargetReport) []string {
		slots := make([]string, 0, len(t.Usage))
		for _, u := range t.Usage {
			slots = append(slots, u.Slot)
		}
		return slots
	}) {
		row := []string{slot}
		for _, t := range r.Targets {
			row = append(row, cellOr(t.Usage, slot, func(u Usage) string { return u.Slot }, func(u Usage) string {
				return fmt.Sprintf("%d / %d", u.InputTokens, u.OutputTokens)
			}))
		}
		writeRow(b, row)
	}
	b.WriteString("\n")
}

func (r Report) latencyTable(b *strings.Builder) {
	b.WriteString("## Latency (ms)\n\n")
	r.header(b, "Operation", "p50", "p95", "n")
	for _, op := range r.keys(func(t TargetReport) []string {
		ops := make([]string, 0, len(t.Latency))
		for _, l := range t.Latency {
			ops = append(ops, l.Op)
		}
		return ops
	}) {
		row := []string{op}
		for _, t := range r.Targets {
			l, ok := find(t.Latency, op, func(l LatencyStats) string { return l.Op })
			if !ok {
				row = append(row, "-", "-", "-")
				continue
			}
			row = append(row, fmt.Sprintf("%.1f", l.P50Ms), fmt.Sprintf("%.1f", l.P95Ms), fmt.Sprint(l.Count))
		}
		writeRow(b, row)
	}
	b.WriteString("\n")
}

func (r Report) problems(b *strings.Builder) {
	b.WriteString("## Problems\n\n")
	for _, t := range r.Targets {
		fmt.Fprintf(b, "- %s: %d conversations, %d failed background operations, %d conversation errors\n",
			t.Target, t.Conversations, t.FailedOperations, len(t.Errors))
		for i, e := range t.Errors {
			if i == maxListedErrors {
				fmt.Fprintf(b, "  - ... %d more in the JSON report\n", len(t.Errors)-i)
				break
			}
			fmt.Fprintf(b, "  - %s\n", e)
		}
	}
}

// keys returns the sorted union of names across targets.
func (r Report) keys(names func(TargetReport) []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range r.Targets {
		for _, n := range names(t) {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

func find[T any](items []T, key string, keyOf func(T) string) (T, bool) {
	for _, it := range items {
		if keyOf(it) == key {
			return it, true
		}
	}
	var zero T
	return zero, false
}

func cellOr[T any](items []T, key string, keyOf func(T) string, render func(T) string) string {
	if it, ok := find(items, key, keyOf); ok {
		return render(it)
	}
	return "-"
}

func writeRow(b *strings.Builder, cells []string) {
	for i, c := range cells {
		cells[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	fmt.Fprintf(b, "| %s |\n", strings.Join(cells, " | "))
}
