package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"memd/tools/txcheck/analyzer"
)

func main() {
	singlechecker.Main(analyzer.Analyzer)
}
