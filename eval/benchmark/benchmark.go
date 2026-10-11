package benchmark

import (
	"fmt"
	"memd/eval/harness"
	"sort"
	"sync"
	"time"
)

// Benchmark is one dataset the eval can run. A new benchmark is a small package that implements it and
// calls Register from an init function; the CLI picks it up by name once the package is imported in
// eval/cmd/eval/benchmarks.go.
type Benchmark interface {
	// Name is the registry key and the -benchmark value, for example locomo.
	Name() string
	// Description is one line for `eval list`.
	Description() string
	// Dataset is the file to download (URL and sha256). The zero File means the benchmark brings its own
	// data, and Load gets "" unless the user passed -data.
	Dataset() File
	// Load reads the dataset at path (the downloaded file, the user's -data path, or "" for a benchmark
	// without a Dataset) and returns one conversation per bank. Each question's Category is its report
	// bucket and its Kind picks the judge prompt (and the rag answer prompt).
	Load(path string) ([]harness.Conversation, error)
	// Defaults are the run settings this benchmark needs to be scored the way its published numbers are;
	// a config file or flag still overrides them.
	Defaults() Defaults
}

// Gater is implemented by benchmarks whose run should fail (exit 1) on a bad result, so CI can gate on
// it.
type Gater interface {
	// Gate returns an error when the target's results fail the benchmark's bar.
	Gate(rep harness.TargetReport) error
}

// File is a downloadable dataset file.
type File struct {
	// Name is the file name in the cache directory.
	Name string
	// URL is where to download it from; pin it to a commit so the sha256 stays valid.
	URL string
	// SHA256 is the hex sha256 of the file; a download that does not match is rejected.
	SHA256 string
}

// Defaults are a benchmark's run settings. Zero fields leave the eval's global defaults.
type Defaults struct {
	// Mode is the answer mode: reflect, recall or rag.
	Mode string
	// Judge is the judge kind: llm or contains.
	Judge string
	// Recall tunes Recall in recall and rag modes.
	Recall harness.RecallOptions
	// PollInterval is the barrier's poll interval.
	PollInterval time.Duration
	// SettleTimeout bounds the barrier per conversation.
	SettleTimeout time.Duration
}

var (
	mu       sync.Mutex
	registry = map[string]Benchmark{}
)

// Register adds b to the registry. It panics on an empty or duplicate name, which is a programming error
// caught at start-up.
func Register(b Benchmark) {
	mu.Lock()
	defer mu.Unlock()
	name := b.Name()
	if name == "" {
		panic("benchmark: Register with an empty name")
	}
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("benchmark: %q registered twice", name))
	}
	registry[name] = b
}

// Get returns the benchmark registered as name.
func Get(name string) (Benchmark, bool) {
	mu.Lock()
	defer mu.Unlock()
	b, ok := registry[name]
	return b, ok
}

// All returns every registered benchmark, sorted by name.
func All() []Benchmark {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Benchmark, 0, len(registry))
	for _, b := range registry {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
