package server

import (
	"context"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

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

// NewHealthServer returns the grpc.health.v1 server over probes, for processes that serve health without
// the memd API (the worker's MEMD_WORKER_HEALTH_ADDR). Its semantics are the API server's: see healthServer.
func NewHealthServer(probes []ReadinessProbe) healthpb.HealthServer {
	return newHealthServer(probes)
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
