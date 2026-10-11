package main

import (
	"context"
	"fmt"
	"memd/internal/server"
	"net"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// pinger is the part of a pgx pool the Postgres readiness probe uses.
type pinger interface {
	// Ping checks the connection.
	Ping(ctx context.Context) error
}

// postgresProbe reports Postgres readiness with a ping.
type postgresProbe struct{ db pinger }

var _ server.ReadinessProbe = postgresProbe{}

// Ready pings Postgres.
func (p postgresProbe) Ready(ctx context.Context) error {
	if err := p.db.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	return nil
}

// gatewayProbe reports the gateway reachable when an HTTP request to its base URL gets any response. It
// makes no model call (health checks must not spend tokens or show up as model traffic) and caches the
// answer for gatewayProbeTTL, so frequent health polls don't hit the gateway each time.
type gatewayProbe struct {
	url string
	hc  *http.Client

	mu      sync.Mutex
	checked time.Time
	err     error
}

// gatewayProbeTTL is how long a gateway probe result is reused.
const gatewayProbeTTL = 10 * time.Second

var _ server.ReadinessProbe = (*gatewayProbe)(nil)

// Ready reports whether the gateway answered recently.
func (g *gatewayProbe) Ready(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.checked.IsZero() && time.Since(g.checked) < gatewayProbeTTL {
		return g.err
	}
	g.err = g.check(ctx)
	g.checked = time.Now()
	return g.err
}

// check sends one GET to the base URL; any HTTP status counts as reachable.
func (g *gatewayProbe) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("gateway unreachable: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

// serveHealth serves grpc.health.v1 over probes on lis until ctx is done, then stops gracefully. The
// worker uses it for MEMD_WORKER_HEALTH_ADDR; Check runs the probes, as on the api (server.NewHealthServer).
func serveHealth(ctx context.Context, lis net.Listener, probes []server.ReadinessProbe) error {
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, server.NewHealthServer(probes))
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	select {
	case err := <-served:
		return fmt.Errorf("memd: worker health: %w", err)
	case <-ctx.Done():
	}
	stopped := make(chan struct{})
	go func() { srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(apiShutdownGrace):
		srv.Stop()
	}
	return nil
}
