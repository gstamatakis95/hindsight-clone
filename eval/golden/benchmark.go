package golden

import (
	"fmt"
	"memd/eval/benchmark"
	"memd/eval/harness"
	"time"
)

func init() { benchmark.Register(Benchmark{}) }

// Benchmark is the golden retain -> recall fixtures as a registered benchmark: recall mode, the substring
// judge (no model in the loop), a short barrier so CI fails fast, and a gate that fails the run unless
// every check passed.
type Benchmark struct{}

var (
	_ benchmark.Benchmark = Benchmark{}
	_ benchmark.Gater     = Benchmark{}
)

// Name returns "golden".
func (Benchmark) Name() string { return "golden" }

// Description describes the benchmark for `eval list`.
func (Benchmark) Description() string {
	return "Golden fixtures: retained facts must be recalled (substring judge, no model; CI gate)"
}

// Dataset is empty: the fixtures are built into the binary; -data points at another fixture directory.
func (Benchmark) Dataset() benchmark.File { return benchmark.File{} }

// Load reads the fixtures in dir, or the built-in ones when dir is "".
func (Benchmark) Load(dir string) ([]harness.Conversation, error) { return Load(dir) }

// Defaults are recall mode, the substring judge, a 200 ms poll and a 90 s barrier.
func (Benchmark) Defaults() benchmark.Defaults {
	return benchmark.Defaults{
		Mode: harness.ModeRecall, Judge: "contains", PollInterval: 200 * time.Millisecond, SettleTimeout: 90 * time.Second,
	}
}

// Gate fails unless every check ran and passed.
func (Benchmark) Gate(rep harness.TargetReport) error {
	if rep.Overall.Total == 0 || rep.Overall.Correct != rep.Overall.Total || rep.Pending > 0 {
		return fmt.Errorf("golden: %s passed %d of %d checks", rep.Target, rep.Overall.Correct, rep.Overall.Total)
	}
	return nil
}
