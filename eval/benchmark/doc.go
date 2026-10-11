// Package benchmark is the extension point of memd's eval: the [Benchmark] interface a dataset
// implements, the registry the CLI selects benchmarks from by name, and the download cache that fetches
// a benchmark's dataset once and checks its sha256.
//
// # Adding a benchmark
//
// Write a small package that implements [Benchmark] and registers it:
//
//	func init() { benchmark.Register(mybench{}) }
//
//	func (mybench) Name() string        { return "mybench" }
//	func (mybench) Description() string { return "My dataset (one line for `eval list`)." }
//	func (mybench) Dataset() benchmark.File {
//		return benchmark.File{Name: "data.json", URL: "https://.../<commit>/data.json", SHA256: "..."}
//	}
//	func (mybench) Load(path string) ([]harness.Conversation, error) { ... }
//	func (mybench) Defaults() benchmark.Defaults { return benchmark.Defaults{Mode: harness.ModeRAG} }
//
// then add one blank import to eval/cmd/eval/benchmarks.go. Load turns the dataset into
// harness.Conversation values: one per bank, with the sessions to retain and the questions to ask. Each
// question carries its report Category and a Kind that picks the judge prompt (and, in rag mode, the
// answer prompt); a new prompt is a template in eval/judge/templates. Defaults hold the settings the
// benchmark's published numbers were produced with, so a plain run is comparable; a config file or flag
// overrides them. A benchmark that should fail the run on a bad result also implements [Gater].
//
// # Data
//
// [Fetch] downloads a benchmark's [File] into <data dir>/<benchmark>/<file name> (default
// [DefaultDataDir], ~/.cache/memd-eval), streaming it through sha256 into a temporary file that is renamed
// into place only when the hash matches, and reuses a cached copy whose hash matches. [HashPath] hashes a
// file or a directory, for the run metadata.
package benchmark
