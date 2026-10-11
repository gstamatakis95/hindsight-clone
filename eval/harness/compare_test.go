package harness_test

import (
	"bytes"
	"math"
	"memd/eval/harness"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	a := harness.Report{Benchmark: "locomo", Mode: "rag", Targets: []harness.TargetReport{{
		Target:  "memd",
		Overall: harness.CategoryStats{Category: "overall", Total: 4, Correct: 2, Accuracy: 0.5},
		Categories: []harness.CategoryStats{
			{Category: "1-multi-hop", Total: 2, Correct: 1, Accuracy: 0.5},
			{Category: "2-temporal", Total: 2, Correct: 1, Accuracy: 0.5},
		},
		Usage:   []harness.Usage{{Slot: "EXTRACT", InputTokens: 100, OutputTokens: 100}},
		Latency: []harness.LatencyStats{{Op: "recall", P50Ms: 100, P95Ms: 200}},
	}}}
	b := harness.Report{Benchmark: "locomo", Mode: "rag", Targets: []harness.TargetReport{
		{Target: "hindsight"},
		{
			Target:  "memd",
			Overall: harness.CategoryStats{Category: "overall", Total: 4, Correct: 3, Accuracy: 0.75},
			Categories: []harness.CategoryStats{
				{Category: "1-multi-hop", Total: 2, Correct: 2, Accuracy: 1},
				{Category: "4-single-hop", Total: 2, Correct: 1, Accuracy: 0.5},
			},
			Usage:   []harness.Usage{{Slot: "EXTRACT", InputTokens: 150, OutputTokens: 150}, {Slot: "JUDGE", InputTokens: 1}},
			Latency: []harness.LatencyStats{{Op: "recall", P50Ms: 50, P95Ms: 300}},
		},
	}}
	deltas := harness.Compare(a, b)
	if len(deltas) != 1 || deltas[0].Target != "memd" {
		t.Fatalf("deltas = %+v, want memd only (hindsight is in one run)", deltas)
	}
	cats := deltas[0].Categories
	wantPP := map[string]float64{"overall": 25, "1-multi-hop": 50, "2-temporal": 0, "4-single-hop": 0}
	if len(cats) != 4 {
		t.Fatalf("categories = %+v", cats)
	}
	for _, c := range cats {
		if math.Abs(c.DeltaPP-wantPP[c.Category]) > 1e-9 {
			t.Errorf("%s: delta %.2f pp, want %.2f", c.Category, c.DeltaPP, wantPP[c.Category])
		}
	}
	if cats[2].Category != "2-temporal" || cats[2].B != nil || cats[3].A != nil {
		t.Errorf("a category in one run only must have no stats on the other side: %+v %+v", cats[2], cats[3])
	}
	if len(deltas[0].Usage) != 2 || deltas[0].Usage[1].A != (harness.Usage{}) {
		t.Errorf("usage = %+v", deltas[0].Usage)
	}

	var out bytes.Buffer
	if err := harness.WriteComparison(&out, "runA", "runB", a, b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| overall | 50.0% (2/4) | 75.0% (3/4) | +25.0 |",
		"| 2-temporal | 50.0% (1/2) | - | - |",
		"| EXTRACT | 200 | 300 | +50.0% |",
		"| recall | 100.0 | 50.0 | -50.0% | 200.0 | 300.0 | +50.0% |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("comparison lacks %q:\n%s", want, out.String())
		}
	}
}
