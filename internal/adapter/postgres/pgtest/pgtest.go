package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is the Postgres image; keep it in sync with docker-compose.yml (decision 003).
const Image = "paradedb/paradedb:0.26.1-pg17"

// RequireDockerEnv names the switch that turns "no Docker daemon" from a skip into a test failure.
const RequireDockerEnv = "MEMD_REQUIRE_DOCKER"

// MaxConns is the pool size: enough for the concurrent claim and seq tests plus readers.
const MaxConns = 24

// DB is one migrated Postgres container and a pool over it.
type DB struct {
	// DSN is the libpq URL of the memd database.
	DSN string
	// Pool is a shared pool (MaxConns connections) over DSN.
	Pool *pgxpool.Pool

	container testcontainers.Container
	tables    []string
}

var (
	shared     *DB
	sharedErr  error
	skipReason string
	resetMu    sync.Mutex
)

// Main starts the shared database, runs the tests and terminates the container. It returns m.Run's
// exit code.
func Main(m *testing.M) int {
	ctx := context.Background()
	if err := dockerHealthy(ctx); err != nil {
		if os.Getenv(RequireDockerEnv) != "" {
			// CI sets the switch so a missing daemon fails the run instead of silently skipping the suites.
			sharedErr = fmt.Errorf("docker unavailable and %s is set: %w", RequireDockerEnv, err)
			return m.Run()
		}
		skipReason = fmt.Sprintf("pgtest: docker unavailable: %v", err)
		return m.Run()
	}
	// One retry: a start can time out when many test binaries start containers at once.
	if shared, sharedErr = Start(ctx); sharedErr != nil {
		fmt.Fprintln(os.Stderr, "pgtest: start failed, retrying once:", sharedErr)
		shared, sharedErr = Start(ctx)
	}
	code := m.Run()
	if shared != nil {
		if err := shared.Close(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "pgtest: close:", err)
		}
	}
	return code
}

// Fresh returns the shared pool after truncating every table (Alembic's version table excepted). It skips
// the test without Docker and fails it when the database could not start.
func Fresh(t testing.TB) *pgxpool.Pool {
	t.Helper()
	d := Shared(t)
	if err := d.Reset(t.Context()); err != nil {
		t.Fatalf("pgtest: reset: %v", err)
	}
	return d.Pool
}

// Shared returns the shared database without resetting it.
func Shared(t testing.TB) *DB {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	if sharedErr != nil {
		t.Fatalf("pgtest: start: %v", sharedErr)
	}
	if shared == nil {
		t.Fatal("pgtest: call pgtest.Main from TestMain")
	}
	return shared
}

// Start runs a new container, migrates it with Alembic and opens a pool.
func Start(ctx context.Context) (*DB, error) {
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        Image,
			Env:          map[string]string{"POSTGRES_DB": "memd", "POSTGRES_USER": "memd", "POSTGRES_PASSWORD": "memd"},
			ExposedPorts: []string{"5432/tcp"},
			Tmpfs:        map[string]string{"/docker-entrypoint-initdb.d": "rw", "/var/lib/postgresql/data": "rw"},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(3 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if ctr != nil {
			_ = testcontainers.TerminateContainer(ctr)
		}
		return nil, fmt.Errorf("start %s: %w", Image, err)
	}
	d := &DB{container: ctr}
	if err := d.init(ctx); err != nil {
		_ = d.Close(ctx)
		return nil, err
	}
	return d, nil
}

// init migrates the database and opens the pool.
func (d *DB) init(ctx context.Context) error {
	host, err := d.container.Host(ctx)
	if err != nil {
		return fmt.Errorf("container host: %w", err)
	}
	port, err := d.container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return fmt.Errorf("container port: %w", err)
	}
	d.DSN = fmt.Sprintf("postgres://memd:memd@%s:%s/memd?sslmode=disable", host, port.Port())
	if err := migrate(ctx, d.DSN); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(d.DSN)
	if err != nil {
		return fmt.Errorf("pool config: %w", err)
	}
	cfg.MaxConns = MaxConns
	if d.Pool, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	return d.loadTables(ctx)
}

// loadTables records every public table except alembic_version, for Reset.
func (d *DB) loadTables(ctx context.Context) error {
	rows, err := d.Pool.Query(ctx,
		`SELECT quote_ident(tablename) FROM pg_tables WHERE schemaname = 'public' AND tablename <> 'alembic_version'
		 ORDER BY tablename`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("list tables: %w", err)
		}
		d.tables = append(d.tables, "public."+name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	if len(d.tables) == 0 {
		return errors.New("migrated database has no tables")
	}
	return nil
}

// Reset truncates every table. A leaked transaction holding a lock fails the reset after 10 s instead of
// hanging the run.
func (d *DB) Reset(ctx context.Context) error {
	resetMu.Lock()
	defer resetMu.Unlock()
	conn, err := d.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET lock_timeout = '10s'"); err != nil {
		return fmt.Errorf("lock_timeout: %w", err)
	}
	stmt := "TRUNCATE " + strings.Join(d.tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	_, err = conn.Exec(ctx, "RESET lock_timeout")
	if err != nil {
		return fmt.Errorf("reset lock_timeout: %w", err)
	}
	return nil
}

// Close closes the pool and terminates the container.
func (d *DB) Close(_ context.Context) error {
	if d.Pool != nil {
		d.Pool.Close()
	}
	if d.container == nil {
		return nil
	}
	if err := testcontainers.TerminateContainer(d.container); err != nil {
		return fmt.Errorf("terminate: %w", err)
	}
	return nil
}

// migrate runs `alembic upgrade head` from the repository root against dsn. MEMD_ALEMBIC overrides the
// alembic binary (default .venv/bin/alembic under the root).
func migrate(ctx context.Context, dsn string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	bin := os.Getenv("MEMD_ALEMBIC")
	if bin == "" {
		bin = filepath.Join(root, ".venv", "bin", "alembic")
	}
	// The path is the repo .venv or the developer's MEMD_ALEMBIC, never request input.
	if _, err := os.Stat(bin); err != nil { //nolint:gosec // test helper, see above

		return fmt.Errorf("alembic not found at %s (run `make tools`): %w", bin, err)
	}
	// Fixed arguments; bin is the repo alembic or MEMD_ALEMBIC.
	cmd := exec.CommandContext(ctx, bin, "-c", "migrations/alembic.ini", "upgrade", "head") //nolint:gosec // test helper
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MEMD_DSN="+dsn)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("alembic upgrade head: %w\n%s", err, out)
	}
	return nil
}

// repoRoot walks up from the working directory to the directory holding go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// dockerHealthy reports whether a Docker daemon is reachable.
func dockerHealthy(ctx context.Context) error {
	p, err := testcontainers.NewDockerProvider()
	if err != nil {
		return fmt.Errorf("docker provider: %w", err)
	}
	defer func() { _ = p.Close() }()
	if err := p.Health(ctx); err != nil {
		return fmt.Errorf("docker health: %w", err)
	}
	return nil
}
