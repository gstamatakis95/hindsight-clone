package main

import (
	"context"
	"fmt"
	"io"
	"memd/eval/benchmark"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
)

const usage = `usage:
  eval [run] [-config file.yaml] [-benchmark name] [flags]   run a benchmark (eval run -h lists the flags)
  eval run -resume <run dir> [flags]                        finish an interrupted run, reusing its banks
  eval compare <run A> <run B>                              per-category accuracy, token and latency deltas
  eval list                                                 the registered benchmarks
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand; without one, the arguments are run's flags.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		return runCmd(ctx, args, stdout, stderr)
	case "compare":
		return compareCmd(args, stdout)
	case "list":
		return listCmd(stdout)
	case "help":
		_, _ = io.WriteString(stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", cmd, usage)
	}
}

// listCmd prints the registered benchmarks.
func listCmd(stdout io.Writer) error {
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "BENCHMARK\tDEFAULT MODE\tDESCRIPTION")
	for _, b := range benchmark.All() {
		mode := b.Defaults().Mode
		if mode == "" {
			mode = defaultConfig().Mode
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", b.Name(), mode, b.Description())
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	return nil
}
