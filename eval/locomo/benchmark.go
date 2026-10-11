package locomo

import (
	"memd/eval/benchmark"
	"memd/eval/harness"
)

func init() { benchmark.Register(Benchmark{}) }

// Benchmark is LoCoMo (locomo10.json) as a registered benchmark, scored the way Hindsight's published
// LoCoMo numbers are: rag mode with the LoCoMo answer and judge prompts, and a high-budget recall with
// source chunks attached.
type Benchmark struct{}

var _ benchmark.Benchmark = Benchmark{}

// Name returns "locomo".
func (Benchmark) Name() string { return "locomo" }

// Description describes the benchmark for `eval list`.
func (Benchmark) Description() string {
	return "LoCoMo: 10 long multi-session conversations, 1540 questions (adversarial excluded)"
}

// Dataset is locomo10.json, pinned to the snap-research/locomo commit that last changed it.
func (Benchmark) Dataset() benchmark.File {
	return benchmark.File{
		Name: "locomo10.json",
		URL: "https://raw.githubusercontent.com/snap-research/locomo/" +
			"cbfbc1dba6bc53d00625212a0f22d55ffee7c1fc/data/locomo10.json",
		SHA256: "79fa87e90f04081343b8c8debecb80a9a6842b76a7aa537dc9fdf651ea698ff4",
	}
}

// Load reads locomo10.json.
func (Benchmark) Load(path string) ([]harness.Conversation, error) { return Load(path) }

// Defaults are Hindsight's benchmark settings: rag mode, the LLM judge, and recall with budget high,
// 32768 result tokens and 16384 chunk tokens.
func (Benchmark) Defaults() benchmark.Defaults {
	return benchmark.Defaults{
		Mode:   harness.ModeRAG,
		Judge:  "llm",
		Recall: harness.RecallOptions{Budget: "high", MaxTokens: 32768, ChunkTokens: 16384},
	}
}
