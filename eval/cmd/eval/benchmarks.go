package main

// The benchmarks the CLI can run: each package registers itself with eval/benchmark when imported. A new
// benchmark needs only its import here.
import (
	_ "memd/eval/golden"
	_ "memd/eval/locomo"
	_ "memd/eval/longmemeval"
)
