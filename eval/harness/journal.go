package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Journal file names inside a run directory.
const (
	// ProgressFile holds one Step per line: retained sessions, settled banks, background reads, failures.
	ProgressFile = "progress.jsonl"
	// ResultsFile holds one QuestionResult per line, in completion order; a later line for the same
	// question supersedes an earlier one.
	ResultsFile = "results.jsonl"
)

// Step kinds recorded in the progress file.
const (
	// StepRetained records one session retained into its conversation's bank.
	StepRetained = "retained"
	// StepSettled records a conversation's bank passing the barrier.
	StepSettled = "settled"
	// StepBackground records a read of the bank's cumulative background usage; the latest read wins.
	StepBackground = "background"
	// StepFailed records a conversation-level failure (Stage ingest or background).
	StepFailed = "failed"
)

// Failure stages of a StepFailed.
const (
	stageIngest     = "ingest"
	stageBackground = "background"
)

// Step is one line of the progress file.
type Step struct {
	// Kind is one of the Step* constants.
	Kind string `json:"kind"`
	// Target is the target's name.
	Target string `json:"target"`
	// Conversation is the conversation id.
	Conversation string `json:"conversation"`
	// Session is the retained session's id (StepRetained).
	Session string `json:"session,omitempty"`
	// Stage is what failed (StepFailed): ingest or background.
	Stage string `json:"stage,omitempty"`
	// Ms is the wall time of the retain call or the barrier.
	Ms float64 `json:"ms,omitempty"`
	// Usage is the step's token usage: the retain call's, or the bank's cumulative background usage.
	Usage []Usage `json:"usage,omitempty"`
	// FailedOperations is the bank's failed background operation count (StepBackground).
	FailedOperations int `json:"failed_operations,omitempty"`
	// Error is the failure (StepFailed).
	Error string `json:"error,omitempty"`
	// At is when the step was recorded.
	At time.Time `json:"at"`
}

// convState is what the journal knows about one (target, conversation).
type convState struct {
	retained   map[string]bool
	settled    bool
	background bool
	answered   map[string]bool
}

// Journal records a run's progress in two append-only JSON-lines files in the run directory. It is the
// source of the report and of resume: a resumed run skips sessions already retained, banks already
// settled and questions already answered without error, reusing the same banks. Safe for concurrent use.
type Journal struct {
	mu       sync.Mutex
	progress *os.File
	results  *os.File
	steps    []Step
	answers  []QuestionResult
	state    map[[2]string]*convState
}

// OpenJournal opens (creating if needed) the journal in dir and loads what it already holds. A line cut
// short by a crash is dropped.
func OpenJournal(dir string) (*Journal, error) {
	j := &Journal{state: map[[2]string]*convState{}}
	var err error
	if j.progress, err = openLines(filepath.Join(dir, ProgressFile), func(b []byte) error {
		var s Step
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		j.applyStep(s)
		return nil
	}); err != nil {
		return nil, err
	}
	if j.results, err = openLines(filepath.Join(dir, ResultsFile), func(b []byte) error {
		var q QuestionResult
		if err := json.Unmarshal(b, &q); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		j.applyResult(q)
		return nil
	}); err != nil {
		_ = j.progress.Close()
		return nil, err
	}
	return j, nil
}

// openLines reads every complete line of path through apply, truncates a trailing partial line, and
// returns the file opened for appending.
func openLines(path string, apply func([]byte) error) (*os.File, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: open %s: %w", path, err)
	}
	good, err := readLines(f, path, apply)
	if err == nil {
		err = f.Truncate(good)
	}
	if err == nil {
		_, err = f.Seek(good, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal: %s: %w", path, err)
	}
	return f, nil
}

// readLines applies every newline-terminated line of r and returns the offset just past the last one.
func readLines(r io.Reader, path string, apply func([]byte) error) (int64, error) {
	br := bufio.NewReader(r)
	var good int64
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return good, nil // a non-empty line here has no newline: cut short, dropped
		}
		if err != nil {
			return 0, fmt.Errorf("read: %w", err)
		}
		if b := bytes.TrimSpace(line); len(b) > 0 {
			if err := apply(b); err != nil {
				return 0, fmt.Errorf("%s line %d: %w", path, n, err)
			}
		}
		good += int64(len(line))
	}
}

// Close closes the journal files.
func (j *Journal) Close() error {
	return errors.Join(j.progress.Close(), j.results.Close())
}

// recordStep appends s to the progress file.
func (j *Journal) recordStep(s Step) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := writeLine(j.progress, s); err != nil {
		return err
	}
	j.applyStep(s)
	return nil
}

// recordResult appends q to the results file.
func (j *Journal) recordResult(q QuestionResult) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := writeLine(j.results, q); err != nil {
		return err
	}
	j.applyResult(q)
	return nil
}

// writeLine writes v as one JSON line with a single write.
func writeLine(f *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("journal: encode: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("journal: write %s: %w", f.Name(), err)
	}
	return nil
}

func (j *Journal) conv(target, conv string) *convState {
	k := [2]string{target, conv}
	st, ok := j.state[k]
	if !ok {
		st = &convState{retained: map[string]bool{}, answered: map[string]bool{}}
		j.state[k] = st
	}
	return st
}

func (j *Journal) applyStep(s Step) {
	j.steps = append(j.steps, s)
	st := j.conv(s.Target, s.Conversation)
	switch s.Kind {
	case StepRetained:
		st.retained[s.Session] = true
	case StepSettled:
		st.settled = true
	case StepBackground:
		st.background = true
	}
}

func (j *Journal) applyResult(q QuestionResult) {
	j.answers = append(j.answers, q)
	j.conv(q.Target, q.Conversation).answered[q.ID] = q.Error == ""
}

// snapshot returns a copy of what the journal knows about (target, conv).
func (j *Journal) snapshot(target, conv string) convState {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := j.conv(target, conv)
	cp := convState{
		retained: make(map[string]bool, len(st.retained)), settled: st.settled, background: st.background,
		answered: make(map[string]bool, len(st.answered)),
	}
	for k, v := range st.retained {
		cp.retained[k] = v
	}
	for k, v := range st.answered {
		cp.answered[k] = v
	}
	return cp
}
