package grpcapi

import (
	"context"
	"fmt"
	"memd/internal/clock"
	"net"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// ReadinessProbe is one readiness check behind grpc.health.v1.
type ReadinessProbe interface {
	// Ready returns nil when the dependency answers.
	Ready(ctx context.Context) error
}

// healthServer is grpc.health.v1 with readiness: Check runs every probe (Postgres, gateway) and reports
// NOT_SERVING when one fails. Service names are ignored by Check.
//
// Watch and List are the standard grpc-go health server's and run no probe: memd never calls
// SetServingStatus, so Watch on "" streams SERVING once and then nothing (it is a liveness signal, not
// readiness), Watch on any other name streams SERVICE_UNKNOWN, and List reports {"": SERVING}. Readiness
// checks (k8s grpc probes, grpc_health_probe) call Check, which is the only probe-backed method.
type healthServer struct {
	*health.Server
	// probes are the readiness checks.
	probes []ReadinessProbe
}

var _ healthpb.HealthServer = (*healthServer)(nil)

// newHealthServer returns a health server over probes.
func newHealthServer(probes []ReadinessProbe) *healthServer {
	return &healthServer{Server: health.NewServer(), probes: probes}
}

// Check runs the probes and reports SERVING only when all of them pass. Service names are ignored: every
// memd service shares the same dependencies.
func (h *healthServer) Check(
	ctx context.Context, _ *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
	for _, p := range h.probes {
		if p == nil {
			continue
		}
		if err := p.Ready(ctx); err != nil {
			return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}, nil
		}
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

// ServeHealth serves grpc.health.v1 over probes on lis until ctx is done, then stops gracefully, forcing the
// stop after grace. Processes that serve health without the memd API (the worker's health address) use it;
// Check behaves as on the API server.
func ServeHealth(ctx context.Context, lis net.Listener, probes []ReadinessProbe, grace time.Duration) error {
	srv := grpc.NewServer()
	healthpb.RegisterHealthServer(srv, newHealthServer(probes))
	served := make(chan error, 1)
	go func() { served <- srv.Serve(lis) }()
	select {
	case err := <-served:
		return fmt.Errorf("health: serve: %w", err)
	case <-ctx.Done():
	}
	stopped := make(chan struct{})
	go func() { srv.GracefulStop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(grace):
		srv.Stop()
	}
	return nil
}

// PostgresProbe reports Postgres readiness with a ping.
type PostgresProbe struct {
	// Ping checks one pooled connection (the pgx pool's Ping).
	Ping func(ctx context.Context) error
}

var _ ReadinessProbe = PostgresProbe{}

// Ready pings Postgres.
func (p PostgresProbe) Ready(ctx context.Context) error {
	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	return nil
}

// gatewayProbeTTL is how long a gateway probe result is reused.
const gatewayProbeTTL = 10 * time.Second

// GatewayProbe reports the gateway reachable when an HTTP request to its base URL gets any response. It makes no
// model call (health checks must not spend tokens or show up as model traffic) and caches the answer for 10 s, so
// frequent health polls don't hit the gateway each time.
type GatewayProbe struct {
	url   string
	hc    *http.Client
	clock clock.Clock

	mu      sync.Mutex
	checked time.Time
	err     error
}

var _ ReadinessProbe = (*GatewayProbe)(nil)

// NewGatewayProbe returns a probe of the gateway at url, sent with hc; clk dates the cached answer.
func NewGatewayProbe(url string, hc *http.Client, clk clock.Clock) *GatewayProbe {
	return &GatewayProbe{url: url, hc: hc, clock: clk}
}

// Ready reports whether the gateway answered recently.
func (g *GatewayProbe) Ready(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.checked.IsZero() && g.clock.Now().Sub(g.checked) < gatewayProbeTTL {
		return g.err
	}
	g.err = g.check(ctx)
	g.checked = g.clock.Now()
	return g.err
}

// check sends one GET to the base URL; any HTTP status counts as reachable.
func (g *GatewayProbe) check(ctx context.Context) error {
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
