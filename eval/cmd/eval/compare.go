package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"memd/eval/harness"
	"os"
	"path/filepath"
	"strings"
)

// compareCmd executes `eval compare <run A> <run B>`: each argument is a run directory or its
// report.json. It warns first when the runs differ in what makes their numbers comparable.
func compareCmd(args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: eval compare <run A> <run B>")
	}
	a, ma, err := readRun(args[0])
	if err != nil {
		return err
	}
	b, mb, err := readRun(args[1])
	if err != nil {
		return err
	}
	for _, w := range comparability(a, b, ma, mb) {
		_, _ = fmt.Fprintf(stdout, "> warning: %s\n", w)
	}
	if err := harness.WriteComparison(stdout, runLabel(args[0]), runLabel(args[1]), a, b); err != nil {
		return fmt.Errorf("compare: %w", err)
	}
	return nil
}

// readRun reads a run's report and, when present, its metadata.
func readRun(path string) (harness.Report, *runMeta, error) {
	dir, file := path, filepath.Join(path, reportJSON)
	if strings.HasSuffix(path, ".json") {
		dir, file = filepath.Dir(path), path
	}
	raw, err := os.ReadFile(filepath.Clean(file)) //nolint:gosec // reads the run the user names on the command line
	if err != nil {
		return harness.Report{}, nil, fmt.Errorf("compare: %w", err)
	}
	var rep harness.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return harness.Report{}, nil, fmt.Errorf("compare: decode %s: %w", file, err)
	}
	m, err := readMeta(dir)
	if err != nil {
		m = nil // a bare report.json has no metadata; the comparison still works
	}
	return rep, m, nil
}

// comparability lists what differs between the runs beyond the system under test.
func comparability(a, b harness.Report, ma, mb *runMeta) []string {
	var w []string
	add := func(what, x, y string) {
		if x != y {
			w = append(w, fmt.Sprintf("%s differs: %q vs %q", what, x, y))
		}
	}
	add("benchmark", a.Benchmark, b.Benchmark)
	add("mode", a.Mode, b.Mode)
	if ma == nil || mb == nil {
		return w
	}
	add("dataset sha256", ma.Dataset.SHA256, mb.Dataset.SHA256)
	add("judge model", ma.Judge.Model, mb.Judge.Model)
	var answerA, answerB string
	if ma.Answer != nil {
		answerA = ma.Answer.Model
	}
	if mb.Answer != nil {
		answerB = mb.Answer.Model
	}
	add("answer model", answerA, answerB)
	if !ma.Complete || !mb.Complete {
		w = append(w, "at least one run is incomplete (questions pending)")
	}
	return w
}

// runLabel names a run by its directory.
func runLabel(path string) string {
	if strings.HasSuffix(path, ".json") {
		path = filepath.Dir(path)
	}
	return filepath.Base(filepath.Clean(path))
}
