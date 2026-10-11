package harness

import (
	"context"
	"errors"
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
	// ModeRAG asks Recall, has the eval's answer model answer from the recalled memories, and grades that
	// answer: the methodology of Hindsight's published LoCoMo and LongMemEval numbers.
	ModeRAG = "rag"
)

// Modes lists the valid answer modes.
var Modes = []string{ModeReflect, ModeRecall, ModeRAG}

// Operations timed by the runner (the latency buckets of the report).
const (
	opRetain  = "retain"
	opBarrier = "barrier"
	opRecall  = "recall"
	opAnswer  = "answer"
	opReflect = "reflect"
	opJudge   = "judge"
)

// Config tunes a Runner. Zero fields take the defaults noted on each.
type Config struct {
	// Mode is ModeReflect (default), ModeRecall or ModeRAG.
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
	// CallTimeout bounds every single target, answer and judge call; 0 = no per-call bound (only ctx).
	CallTimeout time.Duration
	// Recall tunes the Recall calls of recall and rag modes.
	Recall RecallOptions
}

// Deps are a Runner's collaborators.
type Deps struct {
	// Target is the memory service under evaluation.
	Target Target
	// Judge grades the responses.
	Judge Judge
	// Answerer answers from recalled memories; required in rag mode only.
	Answerer Answerer
	// Journal records progress and results; the report is built from it.
	Journal *Journal
	// Clock times calls and paces the barrier; nil = SystemClock.
	Clock Clock
}

// Runner drives conversations against one target and grades the answers.
type Runner struct {
	d   Deps
	cfg Config
}

// NewRunner returns a runner over d with cfg's defaults applied. It fails on an unknown mode or a
// missing dependency.
func NewRunner(d Deps, cfg Config) (*Runner, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeReflect
	}
	switch {
	case cfg.Mode != ModeReflect && cfg.Mode != ModeRecall && cfg.Mode != ModeRAG:
		return nil, fmt.Errorf("harness: unknown mode %q (want %s)", cfg.Mode, strings.Join(Modes, ", "))
	case d.Target == nil || d.Judge == nil || d.Journal == nil:
		return nil, errors.New("harness: a runner needs a target, a judge and a journal")
	case cfg.Mode == ModeRAG && d.Answerer == nil:
		return nil, errors.New("harness: rag mode needs an answer model")
	}
	if d.Clock == nil {
		d.Clock = SystemClock{}
	}
	cfg.Parallel = max(cfg.Parallel, 1)
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.QuietPolls <= 0 {
		cfg.QuietPolls = 2
	}
	if cfg.SettleTimeout <= 0 {
		cfg.SettleTimeout = 15 * time.Minute
	}
	return &Runner{d: d, cfg: cfg}, nil
}

// Run retains every conversation into its own bank, waits for the barrier, asks every question and
// returns the target's report. Work the journal already holds is skipped, so a run resumed on the same
// journal and bank prefix picks up where it stopped. A conversation that fails to retain or settle marks
// its questions as errors and the run continues; Run itself fails on two conversations whose bank ids
// collide, a journal write error or a cancelled context, and still returns the report so far.
func (r *Runner) Run(ctx context.Context, convs []Conversation) (TargetReport, error) {
	if err := checkBankIDs(r.cfg.BankPrefix, convs); err != nil {
		return TargetReport{}, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sem := make(chan struct{}, r.cfg.Parallel)
	var wg sync.WaitGroup
	for _, c := range convs {
		sem <- struct{}{}
		wg.Add(1)
		go func(c Conversation) {
			defer func() { <-sem; wg.Done() }()
			if err := r.runConversation(ctx, c); err != nil {
				cancel(err)
			}
		}(c)
	}
	wg.Wait()
	rep := r.d.Journal.Report(r.d.Target.Name(), convs)
	if err := ctx.Err(); err != nil {
		return rep, fmt.Errorf("harness: run interrupted: %w", context.Cause(ctx))
	}
	return rep, nil
}

// runConversation runs what is left of one conversation. Only journal write errors are returned; a
// cancelled context ends it without recording the unfinished work.
func (r *Runner) runConversation(ctx context.Context, c Conversation) error {
	name := r.d.Target.Name()
	st := r.d.Journal.snapshot(name, c.ID)
	bank := BankID(r.cfg.BankPrefix, c.ID)
	if st.background && allAnswered(c, st.answered) {
		return nil
	}
	exists := st.settled || len(st.retained) > 0
	if !st.settled {
		n, err := r.retainAll(ctx, bank, c, st.retained)
		exists = exists || n > 0
		if err == nil {
			err = r.waitIdle(ctx, bank, c.ID)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err := r.failConversation(c, err, st.answered); err != nil {
				return err
			}
			return r.background(ctx, bank, c.ID, exists)
		}
	}
	for _, q := range c.Questions {
		if st.answered[q.ID] {
			continue
		}
		res := r.ask(ctx, bank, c.ID, q)
		if ctx.Err() != nil {
			return nil
		}
		if err := r.d.Journal.recordResult(res); err != nil {
			return err
		}
	}
	return r.background(ctx, bank, c.ID, true)
}

// allAnswered reports whether every question of c is answered without error.
func allAnswered(c Conversation, answered map[string]bool) bool {
	for _, q := range c.Questions {
		if !answered[q.ID] {
			return false
		}
	}
	return true
}

// background journals the bank's cumulative background usage and failed operations, when the bank
// exists.
func (r *Runner) background(ctx context.Context, bank, conv string, exists bool) error {
	if !exists || ctx.Err() != nil {
		return nil
	}
	cctx, cancel := r.call(ctx)
	defer cancel()
	step := Step{Kind: StepBackground, Target: r.d.Target.Name(), Conversation: conv}
	bg, err := r.d.Target.Background(cctx, bank)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		step.Kind, step.Stage, step.Error = StepFailed, stageBackground, fmt.Sprintf("background usage: %v", err)
	}
	step.Usage, step.FailedOperations, step.At = bg.Usage, bg.Failed, r.d.Clock.Now()
	return r.d.Journal.recordStep(step)
}

// failConversation journals err and marks c's unanswered questions as failed with it.
func (r *Runner) failConversation(c Conversation, err error, answered map[string]bool) error {
	now := r.d.Clock.Now()
	name := r.d.Target.Name()
	step := Step{
		Kind: StepFailed, Target: name, Conversation: c.ID, Stage: stageIngest, Error: err.Error(), At: now,
	}
	if err := r.d.Journal.recordStep(step); err != nil {
		return err
	}
	for _, q := range c.Questions {
		if answered[q.ID] {
			continue
		}
		res := newResult(name, c.ID, q)
		res.Error, res.At = err.Error(), now
		if err := r.d.Journal.recordResult(res); err != nil {
			return err
		}
	}
	return nil
}

// call bounds one target, answer or judge call by CallTimeout.
func (r *Runner) call(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.cfg.CallTimeout > 0 {
		return context.WithTimeout(ctx, r.cfg.CallTimeout)
	}
	return context.WithCancel(ctx)
}

// retainAll retains the sessions not retained yet, in order, one synchronous Retain each, journaling
// each one, and returns how many it retained.
func (r *Runner) retainAll(ctx context.Context, bank string, c Conversation, done map[string]bool) (int, error) {
	n := 0
	for _, s := range c.Sessions {
		if done[s.ID] {
			continue
		}
		start := r.d.Clock.Now()
		cctx, cancel := r.call(ctx)
		usage, err := r.d.Target.Retain(cctx, bank, s)
		cancel()
		if err != nil {
			return n, fmt.Errorf("retain session %s: %w", s.ID, err)
		}
		n++
		end := r.d.Clock.Now()
		step := Step{
			Kind: StepRetained, Target: r.d.Target.Name(), Conversation: c.ID, Session: s.ID,
			Ms: ms(end.Sub(start)), Usage: usage, At: end,
		}
		if err := r.d.Journal.recordStep(step); err != nil {
			return n, err
		}
	}
	return n, nil
}

// waitIdle is the eval barrier: it polls the bank's pending operations until QuietPolls consecutive
// polls see none, or SettleTimeout passes, and journals the settled bank.
func (r *Runner) waitIdle(ctx context.Context, bank, conv string) error {
	start := r.d.Clock.Now()
	deadline := start.Add(r.cfg.SettleTimeout)
	quiet := 0
	var last map[string]int64
	for {
		cctx, cancel := r.call(ctx)
		pending, err := r.d.Target.PendingOperations(cctx, bank)
		cancel()
		if err != nil {
			return fmt.Errorf("barrier: %w", err)
		}
		if len(pending) == 0 {
			quiet++
		} else {
			quiet, last = 0, pending
		}
		now := r.d.Clock.Now()
		if quiet >= r.cfg.QuietPolls {
			return r.d.Journal.recordStep(Step{
				Kind: StepSettled, Target: r.d.Target.Name(), Conversation: conv, Ms: ms(now.Sub(start)), At: now,
			})
		}
		if !now.Before(deadline) {
			return fmt.Errorf("barrier: bank %s not idle after %s; last pending %v", bank, r.cfg.SettleTimeout, last)
		}
		if err := r.d.Clock.Sleep(ctx, r.cfg.PollInterval); err != nil {
			return fmt.Errorf("barrier: %w", err)
		}
	}
}

func newResult(target, conv string, q Question) QuestionResult {
	return QuestionResult{
		Target: target, Conversation: conv, ID: q.ID, Category: q.Category, Query: q.Query, Gold: q.Answer,
		LatencyMs: map[string]float64{},
	}
}

// timed runs f as one bounded call and records its wall time under op.
func (r *Runner) timed(ctx context.Context, res *QuestionResult, op string, f func(context.Context) error) error {
	start := r.d.Clock.Now()
	cctx, cancel := r.call(ctx)
	defer cancel()
	err := f(cctx)
	res.LatencyMs[op] = ms(r.d.Clock.Now().Sub(start))
	return err
}

// wrapOp prefixes err with the operation that failed; nil stays nil.
func wrapOp(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

// ask puts one question to the target in the configured mode and grades the response.
func (r *Runner) ask(ctx context.Context, bank, conv string, q Question) QuestionResult {
	res := newResult(r.d.Target.Name(), conv, q)
	graded, err := r.respond(ctx, bank, q, &res)
	if err == nil && graded {
		err = r.timed(ctx, &res, opJudge, func(ctx context.Context) error {
			v, err := r.d.Judge.Grade(ctx, q, res.Response, r.cfg.Mode == ModeRecall)
			res.Usage = append(res.Usage, v.Usage...)
			res.Correct, res.NotApplicable, res.Reason = v.Correct && !v.NotApplicable, v.NotApplicable, v.Reason
			return wrapOp(opJudge, err)
		})
	}
	if err != nil {
		res.Error = err.Error()
	}
	res.At = r.d.Clock.Now()
	return res
}

// respond gets the response to grade into res. It returns false when the question is already decided:
// in rag mode an empty recall is wrong without an answer or a judge call.
func (r *Runner) respond(ctx context.Context, bank string, q Question, res *QuestionResult) (bool, error) {
	if r.cfg.Mode == ModeReflect {
		return true, r.timed(ctx, res, opReflect, func(ctx context.Context) error {
			text, usage, err := r.d.Target.Reflect(ctx, bank, q.Query, q.QueryTime)
			res.Response, res.Usage = text, append(res.Usage, usage...)
			return wrapOp(opReflect, err)
		})
	}
	var rec Recalled
	err := r.timed(ctx, res, opRecall, func(ctx context.Context) error {
		var err error
		rec, err = r.d.Target.Recall(ctx, bank, q.Query, q.QueryTime, r.cfg.Recall)
		return wrapOp(opRecall, err)
	})
	res.Memories = len(rec.Results)
	if err != nil || r.cfg.Mode == ModeRecall {
		res.Response = strings.Join(rec.Texts(), "\n")
		return true, err
	}
	if len(rec.Results) == 0 {
		res.Reason = "empty context: no memories recalled"
		return false, nil
	}
	return true, r.timed(ctx, res, opAnswer, func(ctx context.Context) error {
		a, err := r.d.Answerer.Answer(ctx, q, rec)
		res.Response, res.Reasoning, res.Usage = a.Text, a.Reasoning, append(res.Usage, a.Usage...)
		return wrapOp(opAnswer, err)
	})
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
