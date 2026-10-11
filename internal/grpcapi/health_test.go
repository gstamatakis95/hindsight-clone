package grpcapi_test

import (
	"context"
	"errors"
	"memd/internal/grpcapi"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// failingProbe is a readiness probe that fails while fail is set.
type failingProbe struct{ fail atomic.Bool }

// Ready fails while fail is set.
func (p *failingProbe) Ready(context.Context) error {
	if p.fail.Load() {
		return errors.New("postgres down")
	}
	return nil
}

// checkFlips checks that Check over conn (sent without a tenant header) reports SERVING, then NOT_SERVING once
// probe fails.
func checkFlips(t *testing.T, conn *grpc.ClientConn, probe *failingProbe) {
	t.Helper()
	hc := healthpb.NewHealthClient(conn)
	check := func() healthpb.HealthCheckResponse_ServingStatus {
		t.Helper()
		resp, err := hc.Check(t.Context(), &healthpb.HealthCheckRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return resp.GetStatus()
	}
	if s := check(); s != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("healthy: %v", s)
	}
	probe.fail.Store(true)
	if s := check(); s != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("probe failing: %v", s)
	}
}

// TestHealthWithoutTenant checks the API server answers health checks without a tenant header and reports the
// probes.
func TestHealthWithoutTenant(t *testing.T) {
	probe := &failingProbe{}
	srv := grpcapi.New(grpcapi.Handlers{}, grpcapi.Options{Probes: []grpcapi.ReadinessProbe{probe}})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	checkFlips(t, conn, probe)
}

// TestServeHealth checks the standalone health server (the worker's) answers Check from the probes and stops when
// its context ends.
func TestServeHealth(t *testing.T) {
	probe := &failingProbe{}
	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- grpcapi.ServeHealth(ctx, lis, []grpcapi.ReadinessProbe{probe}, time.Second) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	checkFlips(t, conn, probe)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeHealth: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ServeHealth did not stop")
	}
}
