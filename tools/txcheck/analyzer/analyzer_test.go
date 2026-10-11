package analyzer_test

import (
	"memd/tools/txcheck/analyzer"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), analyzer.Analyzer, "memd/a")
}
