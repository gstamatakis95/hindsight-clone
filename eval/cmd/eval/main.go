package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"memd/eval/golden"
	"memd/eval/harness"
	"memd/eval/judge"
	"memd/eval/locomo"
	"memd/eval/longmemeval"
	"memd/eval/stub"
	"memd/eval/target"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// options are the parsed command-line flags.
type options struct {
	dataset, data, mode, judgeKind         string
	addr, tenant, hindsightURL             string
	hindsightKey                           string
	useStub, skipMemd                      bool
	judgeURL, judgeModel, judgeKey         string
	bankPrefix, outJSON, outMD, categories string
	parallel, quietPolls                   int
	maxConvs, maxQuestions                 int
	poll, settle                           time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

// parseFlags parses args; dataset-dependent defaults apply to flags the caller did not set.
func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.StringVar(&o.dataset, "dataset", "", "longmemeval | locomo | golden (required)")
	fs.StringVar(&o.data, "data", "", "dataset file, or the fixture directory for golden (default eval/golden)")
	fs.StringVar(&o.mode, "mode", harness.ModeReflect, "reflect | recall (golden default: recall)")
	fs.StringVar(&o.judgeKind, "judge", "llm", "llm | contains (golden default: contains)")
	fs.StringVar(&o.addr, "addr", "localhost:50051", "memd gRPC address")
	fs.StringVar(&o.tenant, "tenant", "eval", "tenant sent as x-tenant-id")
	fs.BoolVar(&o.useStub, "stub", false, "run memd's side against an in-process stub server instead of -addr")
	fs.BoolVar(&o.skipMemd, "skip-memd", false, "do not run against memd (use with -hindsight-url)")
	fs.StringVar(&o.hindsightURL, "hindsight-url", "", "also run against stock Hindsight's HTTP API at this URL")
	fs.StringVar(&o.hindsightKey, "hindsight-api-key", os.Getenv("MEMD_EVAL_HINDSIGHT_API_KEY"), "Hindsight API key")
	fs.StringVar(&o.judgeURL, "judge-url", os.Getenv("MEMD_EVAL_JUDGE_URL"), "OpenAI-compatible gateway base URL")
	fs.StringVar(&o.judgeModel, "judge-model", os.Getenv("MEMD_EVAL_JUDGE_MODEL"), "judge model name")
	fs.StringVar(&o.bankPrefix, "bank-prefix", "", "bank id prefix (default eval-<unix time>-)")
	fs.StringVar(&o.categories, "category", "", "comma-separated categories to keep")
	fs.IntVar(&o.maxConvs, "limit", 0, "max conversations (0 = all)")
	fs.IntVar(&o.maxQuestions, "questions", 0, "max questions per conversation (0 = all)")
	fs.IntVar(&o.parallel, "parallel", 4, "conversations run concurrently")
	fs.DurationVar(&o.poll, "poll", time.Second, "barrier poll interval")
	fs.IntVar(&o.quietPolls, "quiet-polls", 2, "consecutive idle polls that end the barrier")
	fs.DurationVar(&o.settle, "settle-timeout", 15*time.Minute, "barrier timeout per conversation")
	fs.StringVar(&o.outJSON, "out-json", "eval-report.json", "JSON report path (empty = none)")
	fs.StringVar(&o.outMD, "out-md", "eval-report.md", "Markdown report path (empty = none)")
	if err := fs.Parse(args); err != nil {
		return o, fmt.Errorf("flags: %w", err)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if o.dataset == "golden" {
		if !set["mode"] {
			o.mode = harness.ModeRecall
		}
		if !set["judge"] {
			o.judgeKind = "contains"
		}
		if o.data == "" {
			o.data = filepath.Join("eval", "golden")
		}
	}
	o.judgeKey = os.Getenv("MEMD_EVAL_JUDGE_API_KEY")
	if o.bankPrefix == "" {
		o.bankPrefix = fmt.Sprintf("eval-%d-", time.Now().Unix())
	}
	return o, nil
}

// run executes one eval and writes the reports.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	convs, err := load(o.dataset, o.data)
	if err != nil {
		return err
	}
	convs = harness.Filter{
		Categories: splitList(o.categories), MaxConversations: o.maxConvs, MaxQuestions: o.maxQuestions,
	}.Apply(convs)
	j, err := newJudge(o)
	if err != nil {
		return err
	}
	targets, closeAll, err := newTargets(o)
	if err != nil {
		return err
	}
	defer closeAll()
	rep := harness.Report{Dataset: o.dataset, Mode: o.mode}
	cfg := harness.Config{
		Mode: o.mode, BankPrefix: o.bankPrefix, Parallel: o.parallel,
		PollInterval: o.poll, QuietPolls: o.quietPolls, SettleTimeout: o.settle,
	}
	for _, t := range targets {
		tr, err := harness.NewRunner(t, j, harness.SystemClock{}, cfg).Run(ctx, convs)
		rep.Targets = append(rep.Targets, tr)
		if err != nil {
			return errors.Join(err, writeReports(rep, o, stdout))
		}
	}
	if err := writeReports(rep, o, stdout); err != nil {
		return err
	}
	return goldenGate(o.dataset, rep)
}

// load reads the dataset.
func load(dataset, path string) ([]harness.Conversation, error) {
	if path == "" {
		return nil, fmt.Errorf("-data is required for %q", dataset)
	}
	var (
		convs []harness.Conversation
		err   error
	)
	switch dataset {
	case "longmemeval":
		convs, err = longmemeval.Load(path)
	case "locomo":
		convs, err = locomo.Load(path)
	case "golden":
		convs, err = golden.Load(path)
	default:
		return nil, fmt.Errorf("unknown -dataset %q (want longmemeval, locomo or golden)", dataset)
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", dataset, err)
	}
	return convs, nil
}

// newJudge builds the judge selected by -judge.
func newJudge(o options) (harness.Judge, error) {
	switch o.judgeKind {
	case "contains":
		return judge.Contains{}, nil
	case "llm":
		if o.judgeURL == "" || o.judgeModel == "" {
			return nil, errors.New("-judge llm needs -judge-url and -judge-model (or MEMD_EVAL_JUDGE_URL/_MODEL)")
		}
		return judge.NewLLM(o.judgeURL, o.judgeModel, o.judgeKey, nil), nil
	default:
		return nil, fmt.Errorf("unknown -judge %q (want llm or contains)", o.judgeKind)
	}
}

// newTargets connects to memd (or starts the stub) and Hindsight as requested.
func newTargets(o options) ([]harness.Target, func(), error) {
	var (
		targets []harness.Target
		closers []func()
	)
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	if !o.skipMemd {
		addr := o.addr
		if o.useStub {
			stubAddr, stopStub, err := stub.Start(stub.Options{PendingPolls: 2})
			if err != nil {
				return nil, closeAll, fmt.Errorf("start stub: %w", err)
			}
			addr = stubAddr
			closers = append(closers, stopStub)
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("dial memd %s: %w", addr, err)
		}
		closers = append(closers, func() { _ = conn.Close() })
		targets = append(targets, target.NewMemd(conn, o.tenant))
	}
	if o.hindsightURL != "" {
		targets = append(targets, target.NewHindsight(o.hindsightURL, o.hindsightKey, nil))
	}
	if len(targets) == 0 {
		closeAll()
		return nil, func() {}, errors.New("no target: -skip-memd needs -hindsight-url")
	}
	return targets, closeAll, nil
}

// writeReports writes the JSON and Markdown reports and prints the Markdown.
func writeReports(rep harness.Report, o options, stdout io.Writer) error {
	if err := rep.WriteMarkdown(stdout); err != nil {
		return fmt.Errorf("print report: %w", err)
	}
	if o.outJSON != "" {
		if err := writeFile(o.outJSON, rep.WriteJSON); err != nil {
			return err
		}
	}
	if o.outMD != "" {
		if err := writeFile(o.outMD, rep.WriteMarkdown); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close report: %w", err)
	}
	return nil
}

// goldenGate fails a golden run when any check failed, so CI can gate on it.
func goldenGate(dataset string, rep harness.Report) error {
	if dataset != "golden" {
		return nil
	}
	for _, t := range rep.Targets {
		if t.Overall.Correct != t.Overall.Total || t.Overall.Total == 0 {
			return fmt.Errorf("golden: %s passed %d of %d checks", t.Target, t.Overall.Correct, t.Overall.Total)
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
