package server

import (
	"context"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// healthServer is grpc.health.v1 with readiness: Check runs every probe (Postgres, gateway) and reports
// NOT_SERVING when one fails. Watch and List come from the standard server, which always reports SERVING.
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
