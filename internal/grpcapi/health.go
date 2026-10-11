package grpcapi

import (
	"context"
	"fmt"
	"net"
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
