package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Environment switches read by the harness.
const (
	// RequireDockerEnv turns "no Docker daemon" from a skip into a failure (same switch as pgtest).
	RequireDockerEnv = "MEMD_REQUIRE_DOCKER"
	// RunEnv names the run; projects are memd-e2e-<run>-<random>, so `make e2e` can sweep a run's leftovers.
	RunEnv = "MEMD_E2E_RUN"
	// AlembicEnv overrides the alembic binary (default <repo>/.venv/bin/alembic, built by `make tools`).
	AlembicEnv = "MEMD_ALEMBIC"
)

// MinIO credentials and bucket from docker-compose.yml.
const (
	// S3Bucket is the bucket minio-init creates.
	S3Bucket = "memd"
	// S3AccessKey is MINIO_ROOT_USER.
	S3AccessKey = "memd"
	// S3SecretKey is MINIO_ROOT_PASSWORD.
	S3SecretKey = "memd-secret"
	// S3Region is the signing region memd uses against MinIO.
	S3Region = "us-east-1"
)

// ProjectPrefix starts every compose project name the harness creates.
const ProjectPrefix = "memd-e2e-"

// overlayFile is the compose overlay (tmpfs data, no named volumes), relative to the repository root.
const overlayFile = "test/e2e/harness/compose.e2e.yml"

// Timeouts of the stack's steps.
const (
	upTimeout      = 5 * time.Minute
	downTimeout    = 2 * time.Minute
	dockerProbeTTL = 15 * time.Second
)

// Stack is one docker-compose project: Postgres (migrated) and MinIO with the memd bucket.
type Stack struct {
	// Project is the compose project name.
	Project string
	// DSN is the libpq URL of the memd database.
	DSN string
	// S3Endpoint is the MinIO S3 API base URL.
	S3Endpoint string
	// PGPort, S3Port and ConsolePort are the host ports.
	PGPort, S3Port, ConsolePort int

	root     string
	downOnce sync.Once
	downErr  error
}

// StartStack brings up a fresh compose project and migrates it. Teardown (down -v) is registered before the
// project starts, so a failed start still removes what it created. Without Docker it skips the test.
func StartStack(t testing.TB) *Stack {
	t.Helper()
	RequireDocker(t)
	root := RepoRoot(t)
	ports, err := freePorts(3)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	s := &Stack{
		Project: projectName(), root: root, PGPort: ports[0], S3Port: ports[1], ConsolePort: ports[2],
	}
	s.DSN = fmt.Sprintf("postgres://memd:memd@127.0.0.1:%d/memd?sslmode=disable", s.PGPort)
	s.S3Endpoint = fmt.Sprintf("http://127.0.0.1:%d", s.S3Port)
	t.Cleanup(func() {
		if err := s.Down(); err != nil {
			t.Errorf("harness: %v", err)
		}
	})
	ctx, cancel := context.WithDeadline(context.Background(), Deadline(t, upTimeout))
	defer cancel()
	if out, err := s.compose(ctx, "up", "-d", "--wait", "postgres", "minio"); err != nil {
		t.Fatalf("harness: compose up %s: %v\n%s", s.Project, err, out)
	}
	if out, err := s.compose(ctx, "run", "--rm", "minio-init"); err != nil {
		t.Fatalf("harness: create bucket: %v\n%s", err, out)
	}
	if err := migrate(ctx, root, s.DSN); err != nil {
		t.Fatalf("harness: %v", err)
	}
	return s
}

// Down removes the project's containers, networks and volumes. It is idempotent.
func (s *Stack) Down() error {
	s.downOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), downTimeout)
		defer cancel()
		out, err := s.compose(ctx, "down", "-v", "--remove-orphans", "--timeout", "5")
		if err != nil {
			s.downErr = fmt.Errorf("compose down %s: %w\n%s", s.Project, err, out)
		}
	})
	return s.downErr
}

// Leftovers lists the containers, volumes and networks still labelled with the project (empty after Down).
func (s *Stack) Leftovers(ctx context.Context) ([]string, error) {
	label := "label=com.docker.compose.project=" + s.Project
	var left []string
	for _, kind := range [][]string{
		{"ps", "-aq", "--filter", label},
		{"volume", "ls", "-q", "--filter", label},
		{"network", "ls", "-q", "--filter", label},
	} {
		out, err := docker(ctx, kind...)
		if err != nil {
			return nil, fmt.Errorf("docker %s: %w\n%s", strings.Join(kind, " "), err, out)
		}
		for _, id := range strings.Fields(string(out)) {
			left = append(left, kind[0]+" "+id)
		}
	}
	return left, nil
}

// compose runs docker compose on the project with the root file and the e2e overlay.
func (s *Stack) compose(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{
		"compose", "-f", filepath.Join(s.root, "docker-compose.yml"), "-f", filepath.Join(s.root, overlayFile),
		"-p", s.Project,
	}, args...)
	// Test helper: the arguments are the harness's own compose files and project name.
	cmd := exec.CommandContext(ctx, "docker", full...) //nolint:gosec // fixed binary, harness-built args
	cmd.Env = append(os.Environ(),
		"MEMD_PG_PORT="+strconv.Itoa(s.PGPort), "MEMD_MINIO_PORT="+strconv.Itoa(s.S3Port),
		"MEMD_MINIO_CONSOLE_PORT="+strconv.Itoa(s.ConsolePort),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker compose %s: %w", args[0], err)
	}
	return out, nil
}

// docker runs the docker CLI.
func docker(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput() //nolint:gosec // harness-built args
	if err != nil {
		return out, fmt.Errorf("docker %s: %w", args[0], err)
	}
	return out, nil
}

// deadlineMargin is kept between a step's deadline and the test binary's -timeout, so that a slow step fails
// the test and its cleanups (compose down) still run, instead of the timeout panic skipping them.
const deadlineMargin = 30 * time.Second

// Deadline returns now+limit, or earlier when the test's own deadline (go test -timeout) minus a margin comes
// first. Use it to bound harness steps and waits.
func Deadline(t testing.TB, limit time.Duration) time.Time {
	d := time.Now().Add(limit)
	if td, ok := t.(interface{ Deadline() (time.Time, bool) }); ok {
		if end, ok := td.Deadline(); ok && end.Add(-deadlineMargin).Before(d) {
			d = end.Add(-deadlineMargin)
		}
	}
	return d
}

// RequireDocker skips the test when no Docker daemon answers, or fails it when RequireDockerEnv is set.
func RequireDocker(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerProbeTTL)
	defer cancel()
	out, err := docker(ctx, "info", "--format", "{{.ServerVersion}}")
	if err == nil {
		return
	}
	msg := fmt.Sprintf("harness: docker unavailable: %v: %s", err, bytes.TrimSpace(out))
	if os.Getenv(RequireDockerEnv) != "" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// nameUnsafe matches what compose project names may not contain.
var nameUnsafe = regexp.MustCompile(`[^a-z0-9_-]`)

// projectName returns memd-e2e-<run>-<random>.
func projectName() string {
	run := nameUnsafe.ReplaceAllString(strings.ToLower(os.Getenv(RunEnv)), "")
	if run == "" {
		run = "local"
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return ProjectPrefix + run + "-" + hex.EncodeToString(b)
}

// freePorts returns n distinct loopback ports that were free a moment ago.
func freePorts(n int) ([]int, error) {
	var (
		ports     []int
		listeners []net.Listener
	)
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("free port: %w", err)
		}
		listeners = append(listeners, l)
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			return nil, errors.New("free port: not a TCP address")
		}
		ports = append(ports, addr.Port)
	}
	return ports, nil
}

// migrate runs `alembic upgrade head` from the repository root against dsn (as pgtest does).
func migrate(ctx context.Context, root, dsn string) error {
	bin := os.Getenv(AlembicEnv)
	if bin == "" {
		bin = filepath.Join(root, ".venv", "bin", "alembic")
	}
	if _, err := os.Stat(bin); err != nil { //nolint:gosec // the repo venv or the developer's MEMD_ALEMBIC
		return fmt.Errorf("alembic not found at %s (run `make tools`): %w", bin, err)
	}
	cmd := exec.CommandContext(ctx, bin, "-c", "migrations/alembic.ini", "upgrade", "head") //nolint:gosec // see above
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MEMD_DSN="+dsn)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("alembic upgrade head: %w\n%s", err, out)
	}
	return nil
}

// RepoRoot returns the directory holding go.mod above the working directory.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("harness: getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("harness: go.mod not found above the working directory")
		}
		dir = parent
	}
}
