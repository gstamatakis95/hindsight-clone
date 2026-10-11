package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"
)

// main runs memd with the process arguments and environment; SIGINT and SIGTERM stop it gracefully.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stderr)
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

// run loads and validates the configuration, wires the dependencies and runs the mode until ctx is done.
func run(ctx context.Context, args []string, env func(string) string, logOut io.Writer) error {
	cfg, err := LoadConfig(args, env)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	d, closer, err := buildDeps(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closer()
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
