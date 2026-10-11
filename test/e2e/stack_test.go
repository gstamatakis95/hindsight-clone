//go:build e2e

package e2e

import (
	"context"
	"memd/test/e2e/gatewayemu"
	"memd/test/e2e/harness"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestStackUpDown brings the compose stack up (migrated Postgres, MinIO with the bucket), tears it down and
// checks no container, volume or network of the project is left.
func TestStackUpDown(t *testing.T) {
	s := harness.StartStack(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, s.DSN)
	if err != nil {
		t.Fatalf("connect %s: %v", s.DSN, err)
	}
	var version string
	err = conn.QueryRow(ctx, "SELECT version_num FROM alembic_version").Scan(&version)
	_ = conn.Close(ctx)
	if err != nil || version == "" {
		t.Fatalf("database not migrated: version %q, %v", version, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.S3Endpoint+"/minio/health/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("minio health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("minio health: status %d", resp.StatusCode)
	}

	if err := s.Down(); err != nil {
		t.Fatal(err)
	}
	left, err := s.Leftovers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Fatalf("project %s left %v behind", s.Project, left)
	}
}

// TestMemdSmoke runs memd as separate api and worker processes against the stack and the emulator, checks the
// api serves, and kills and restarts the worker. It skips while cmd/memd is the placeholder (until WP-14b).
func TestMemdSmoke(t *testing.T) {
	bin := harness.BuildMemd(t)
	s := harness.StartStack(t)
	_, gw := harness.StartEmulator(t, gatewayemu.Options{})
	opts := harness.MemdOptions{GatewayURL: gw.URL + gatewayemu.BasePath}

	opts.Mode = harness.ModeAPI
	api := s.StartMemd(t, bin, opts)
	opts.Mode = harness.ModeWorker
	worker := s.StartMemd(t, bin, opts)

	c := harness.Dial(t, api.Addr, "e2e")
	if c.Tenant != "e2e" {
		t.Fatalf("tenant %q", c.Tenant)
	}
	if err := worker.Kill(); err != nil {
		t.Fatal(err)
	}
	if !worker.Exited() {
		t.Fatal("worker still running after Kill")
	}
	worker.Restart()
	if worker.Exited() {
		t.Fatalf("worker exited after restart:\n%s", worker.Logs())
	}
}
