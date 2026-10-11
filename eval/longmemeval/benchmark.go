package longmemeval

import (
	"memd/eval/benchmark"
	"memd/eval/harness"
)

func init() {
	benchmark.Register(Benchmark{
		name: "longmemeval",
		desc: "LongMemEval S (cleaned): 500 questions, each over its own ~115k-token chat history",
		file: benchmark.File{
			Name:   "longmemeval_s_cleaned.json",
			URL:    hfBase + "longmemeval_s_cleaned.json",
			SHA256: "d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442",
		},
	})
	benchmark.Register(Benchmark{
		name: "longmemeval-oracle",
		desc: "LongMemEval oracle: the 500 questions over only their evidence sessions (a quick check)",
		file: benchmark.File{
			Name:   "longmemeval_oracle.json",
			URL:    hfBase + "longmemeval_oracle.json",
			SHA256: "821a2034d219ab45846873dd14c14f12cfe7776e73527a483f9dac095d38620c",
		},
	})
}

// hfBase is the cleaned LongMemEval release on Hugging Face, pinned to a revision.
const hfBase = "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned/resolve/" +
	"98d7416c24c778c2fee6e6f3006e7a073259d48f/"

// Benchmark is one LongMemEval variant as a registered benchmark. It runs in reflect mode by default and
// grades with LongMemEval's own judge prompts, picked by question type.
type Benchmark struct {
	name, desc string
	file       benchmark.File
}

var _ benchmark.Benchmark = Benchmark{}

// Name returns the variant's name.
func (b Benchmark) Name() string { return b.name }

// Description describes the variant for `eval list`.
func (b Benchmark) Description() string { return b.desc }

// Dataset is the variant's file, pinned to a revision of the cleaned release.
func (b Benchmark) Dataset() benchmark.File { return b.file }

// Load reads a LongMemEval JSON file.
func (Benchmark) Load(path string) ([]harness.Conversation, error) { return Load(path) }

// Defaults are reflect mode and the LLM judge.
func (Benchmark) Defaults() benchmark.Defaults {
	return benchmark.Defaults{Mode: harness.ModeReflect, Judge: "llm"}
}
