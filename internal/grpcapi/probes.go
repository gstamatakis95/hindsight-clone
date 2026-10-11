package grpcapi

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

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
	url string
	hc  *http.Client

	mu      sync.Mutex
	checked time.Time
	err     error
}

var _ ReadinessProbe = (*GatewayProbe)(nil)

// NewGatewayProbe returns a probe of the gateway at url, sent with hc.
func NewGatewayProbe(url string, hc *http.Client) *GatewayProbe {
	return &GatewayProbe{url: url, hc: hc}
}

// Ready reports whether the gateway answered recently.
func (g *GatewayProbe) Ready(ctx context.Context) error {
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
