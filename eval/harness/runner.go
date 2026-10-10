package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Answer modes: how a question is put to the target.
const (
	// ModeReflect asks Reflect and grades its answer.
	ModeReflect = "reflect"
	// ModeRecall asks Recall and grades whether the recalled memories contain the answer.
	ModeRecall = "recall"
)

// Latency buckets recorded by the runner.
const (
	opRetain  = "retain"
	opBarrier = "barrier"
	opJudge   = "judge"
)

// Config tunes a Runner. Zero fields take the defaults noted on each.
type Config struct {
	// Mode is ModeReflect (default) or ModeRecall.
	Mode string
	// BankPrefix is prepended to each conversation id to form its bank id.
	BankPrefix string
	// Parallel is how many conversations run at once; default 1.
	Parallel int
	// PollInterval is the wait between barrier polls; default 1 s.
	PollInterval time.Duration
	// QuietPolls is how many consecutive idle polls end the barrier; default 2, since one idle poll can
	// fall between one job completing and the next being enqueued.
	QuietPolls int
	// SettleTimeout bounds the barrier per conversation; default 15 min.
	SettleTimeout time.Duration
	// CallTimeout bounds every single target and judge call; 0 = no per-call bound (only ctx).
	CallTimeout time.Duration
}

// Runner drives conversations against one target and grades the answers.
type Runner struct {
	target Target
	judge  Judge
	clock  Clock
	cfg    Config
}

// NewRunner returns a runner over target, grading with judge and timing with clock.
func NewRunner(target Target, judge Judge, clock Clock, cfg Config) *Runner {
	if cfg.Mode == "" {
		cfg.Mode = ModeReflect
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.QuietPolls <= 0 {
		cfg.QuietPolls = 2
	}
	if cfg.SettleTimeout <= 0 {
		cfg.SettleTimeout = 15 * time.Minute
	}
	return &Runner{target: target, judge: judge, clock: clock, cfg: cfg}
}

// Run retains every conversation into its own bank, waits for the barrier, asks every question and
// returns the target's report. A conversation that fails to retain or settle marks its questions as
// errors and the run continues; Run itself fails only on an unknown mode, two conversations whose bank
// ids collide, or a cancelled context.
func (r *Runner) Run(ctx context.Context, convs []Conversation) (TargetReport, error) {
	if r.cfg.Mode != ModeReflect && r.cfg.Mode != ModeRecall {
		return TargetReport{}, fmt.Errorf("harness: unknown mode %q", r.cfg.Mode)
	}
	if err := checkBankIDs(r.cfg.BankPrefix, convs); err != nil {
		return TargetReport{}, err
	}
	rec := newRecorder(len(convs))
	sem := make(chan struct{}, r.cfg.Parallel)
	var wg sync.WaitGroup
	for i := range convs {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer func() { <-sem; wg.Done() }()
			r.runConversation(ctx, i, convs[i], rec)
		}(i)
	}
	wg.Wait()
	rep := rec.report(r.target.Name(), len(convs))
	if err := ctx.Err(); err != nil {
		return rep, fmt.Errorf("harness: run interrupted: %w", context.Cause(ctx))
	}
	return rep, nil
}

// runConversation runs one conversation end to end, recording into slot i of rec. Background usage and
// failed operations are collected whenever the bank exists (at least one Retain succeeded), even when a
// later retain or the barrier failed.
func (r *Runner) runConversation(ctx context.Context, i int, c Conversation, rec *recorder) {
	bank := BankID(r.cfg.BankPrefix, c.ID)
	retained, err := r.retainAll(ctx, bank, c.Sessions, rec)
	if err == nil {
		err = r.waitIdle(ctx, bank, rec)
	}
	if err != nil {
		rec.failConversation(i, c, err)
	} else {
		results := make([]QuestionResult, 0, len(c.Questions))
		for _, q := range c.Questions {
			results = append(results, r.ask(ctx, bank, c.ID, q, rec))
		}
		rec.setResults(i, results)
	}
	if retained == 0 {
		return
	}
	cctx, cancel := r.call(ctx)
	defer cancel()
	bg, err := r.target.Background(cctx, bank)
	if err != nil {
		rec.addError(fmt.Sprintf("%s: background usage: %v", c.ID, err))
		return
	}
	rec.addBackground(bg)
}

// call bounds one target or judge call by CallTimeout.
func (r *Runner) call(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.cfg.CallTimeout > 0 {
		return context.WithTimeout(ctx, r.cfg.CallTimeout)
	}
	return context.WithCancel(ctx)
}

// retainAll retains the sessions in order, one synchronous Retain each, and returns how many succeeded.
func (r *Runner) retainAll(ctx context.Context, bank string, sessions []Session, rec *recorder) (int, error) {
	for n, s := range sessions {
		start := r.clock.Now()
		cctx, cancel := r.call(ctx)
		usage, err := r.target.Retain(cctx, bank, s)
		cancel()
		rec.observe(opRetain, r.clock.Now().Sub(start))
		if err != nil {
			return n, fmt.Errorf("retain session %s: %w", s.ID, err)
		}
		rec.addUsage(usage)
	}
	return len(sessions), nil
}

// waitIdle is the eval barrier: it polls the bank's pending operations until QuietPolls consecutive
// polls see none, or SettleTimeout passes.
func (r *Runner) waitIdle(ctx context.Context, bank string, rec *recorder) error {
	start := r.clock.Now()
	deadline := start.Add(r.cfg.SettleTimeout)
	quiet := 0
	var last map[string]int64
	for {
		cctx, cancel := r.call(ctx)
		pending, err := r.target.PendingOperations(cctx, bank)
		cancel()
		if err != nil {
			return fmt.Errorf("barrier: %w", err)
		}
		if len(pending) == 0 {
			quiet++
		} else {
			quiet, last = 0, pending
		}
		if quiet >= r.cfg.QuietPolls {
			rec.observe(opBarrier, r.clock.Now().Sub(start))
			return nil
		}
		if !r.clock.Now().Before(deadline) {
			return fmt.Errorf("barrier: bank %s not idle after %s; last pending %v", bank, r.cfg.SettleTimeout, last)
		}
		if err := r.clock.Sleep(ctx, r.cfg.PollInterval); err != nil {
			return fmt.Errorf("barrier: %w", err)
		}
	}
}

// ask puts one question to the target and grades the response.
func (r *Runner) ask(ctx context.Context, bank, conv string, q Question, rec *recorder) QuestionResult {
	res := QuestionResult{
		Conversation: conv, ID: q.ID, Category: q.Category, Query: q.Query, Gold: q.Answer,
	}
	start := r.clock.Now()
	cctx, cancel := r.call(ctx)
	var err error
	if r.cfg.Mode == ModeRecall {
		var texts []string
		texts, err = r.target.Recall(cctx, bank, q.Query, q.QueryTime)
		res.Response = strings.Join(texts, "\n")
	} else {
		var usage []Usage
		res.Response, usage, err = r.target.Reflect(cctx, bank, q.Query, q.QueryTime)
		rec.addUsage(usage)
	}
	cancel()
	rec.observe(r.cfg.Mode, r.clock.Now().Sub(start))
	if err != nil {
		res.Error = fmt.Sprintf("%s: %v", r.cfg.Mode, err)
		return res
	}
	start = r.clock.Now()
	cctx, cancel = r.call(ctx)
	v, err := r.judge.Grade(cctx, q, res.Response, r.cfg.Mode == ModeRecall)
	cancel()
	rec.observe(opJudge, r.clock.Now().Sub(start))
	if err != nil {
		res.Error = fmt.Sprintf("judge: %v", err)
		return res
	}
	rec.addUsage(v.Usage)
	res.Correct, res.NotApplicable, res.Reason = v.Correct && !v.NotApplicable, v.NotApplicable, v.Reason
	return res
}

// checkBankIDs fails when two conversations sanitise to the same bank id, which would mix their memories.
func checkBankIDs(prefix string, convs []Conversation) error {
	seen := make(map[string]string, len(convs))
	for _, c := range convs {
		id := BankID(prefix, c.ID)
		if prev, dup := seen[id]; dup {
			return fmt.Errorf("harness: conversations %q and %q share bank id %q", prev, c.ID, id)
		}
		seen[id] = c.ID
	}
	return nil
}

// BankID derives a bank id from a prefix and a conversation id, keeping only characters that are safe
// in URLs and bank ids ([A-Za-z0-9._-]; anything else becomes '-').
func BankID(prefix, conv string) string {
	var b strings.Builder
	for _, c := range prefix + conv {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
