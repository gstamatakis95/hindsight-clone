package analyzer_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"memd/tools/txcheck/analyzer"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), analyzer.Analyzer, "memd/a")
}
