package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"memd/internal/observability"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// main runs memd with the process arguments and environment; SIGINT and SIGTERM stop it gracefully.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	switch {
	case errors.Is(err, errUsage):
		usage(os.Stderr)
		os.Exit(2)
	case err != nil:
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run loads and validates the configuration, wires the dependencies and runs the mode until ctx is done. Logs
// go to stderr, or to stdout with Datadog enabled (see logHandler).
func run(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) error {
	cfg, err := LoadConfig(args, env)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	base := logHandler(cfg, stdout, stderr)
	// Datadog is optional and off by default; disabled, tel is nil and installs nothing. It starts before
	// the adapters so their instrumentation sees the running tracer, and its stop (deferred first, so run
	// last) flushes after the pool closes.
	tel, stopTel := observability.Start(observability.Config{
		Enabled: cfg.DatadogEnabled, Profiler: cfg.DatadogProfiler, Metrics: cfg.DatadogMetrics,
		Logger: slog.New(base), Env: env,
	})
	defer stopTel()
	log := slog.New(tel.LogHandler(base))
	slog.SetDefault(log)
	d, closer, err := buildDeps(ctx, cfg, log, tel)
	if err != nil {
		return err
	}
	defer closer()
	// The gauge sampler (Datadog metrics only) stops first: before the pool closes and the client flushes.
	defer tel.StartSampler(ctx, time.Duration(cfg.File.Observability.MetricsInterval), d.samples)()
	switch cfg.Mode {
	case modeAPI:
		return runAPI(ctx, d)
	case modeWorker:
		return runWorker(ctx, d)
	default:
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error { return runAPI(gctx, d) })
		g.Go(func() error { return runWorker(gctx, d) })
		return g.Wait() //nolint:wrapcheck // both modes wrap their own errors
	}
}

// logHandler is the process's slog handler at cfg.LogLevel in cfg's log format. Without Datadog it writes to
// stderr, as always; with Datadog enabled it writes to stdout, the stream the Datadog agent collects without
// treating lines as errors (it may give stderr lines an error status when they carry none of their own).
func logHandler(cfg Config, stdout, stderr io.Writer) slog.Handler {
	w := stderr
	if cfg.DatadogEnabled {
		w = stdout
	}
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.logFormat() == logFormatJSON {
		return observability.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}
