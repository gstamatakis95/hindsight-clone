package pgtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"memd/internal/adapter/postgres"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is the Postgres image; keep it in sync with docker-compose.yml.
const Image = "paradedb/paradedb:0.26.1-pg17"

// RequireDockerEnv names the switch that turns "no Docker daemon" from a skip into a test failure.
const RequireDockerEnv = "MEMD_REQUIRE_DOCKER"

// DatabaseURLEnv names the variable that points the tests at an existing server instead of a container.
const DatabaseURLEnv = "MEMD_TEST_DATABASE_URL"

// MaxConns is the per-database pool size: enough for the concurrent claim and seq tests plus readers.
// Connections open lazily, so an idle test holds one or two.
const MaxConns = 24

// templatePrefix and testPrefix name the template databases and the per-test clones.
const (
	templatePrefix = "memd_tpl_"
	testPrefix     = "memd_t_"
)

// errMigrate marks a failed migration (missing alembic, a broken revision), which a retry can't fix.
var errMigrate = errors.New("migrate the template")

// migrateLockKey is the advisory lock that serializes template creation across test processes.
const migrateLockKey = 0x6d656d64 // "memd"

// DB is one test's database: a clone of the migrated template, dropped when the test ends.
type DB struct {
	// Name is the database name.
	Name string
	// DSN is the libpq URL of the database.
	DSN string
	// Pool is a pool (MaxConns connections) over DSN, closed when the test ends.
	Pool *pgxpool.Pool
}

// Store returns a production Store over the database's pool.
func (d *DB) Store() *postgres.Store { return postgres.New(d.Pool) }

// server is the Postgres server the process clones databases on, set up once.
type server struct {
	admin    *pgxpool.Pool // over the server's maintenance database
	base     *url.URL      // DSN of the maintenance database; clones swap the path
	template string
}

var (
	setupOnce sync.Once
	srv       *server
	setupErr  error
	skipMsg   string

	perTestMu sync.Mutex
	perTest   = map[testing.TB]*DB{}
)

// NewStore returns a production Store over a new database (see New). It skips the test without Docker
// (unless MEMD_REQUIRE_DOCKER is set) and fails it when the server or the migration fails.
func NewStore(t testing.TB) *postgres.Store {
	t.Helper()
	return New(t).Store()
}

// Pool returns the pool of the newest database created for t (by NewStore, New or an earlier Pool call),
// creating one if there is none. Use it for raw SQL setup and assertions next to a Store; a test that
// creates several databases keeps each DB from New instead.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	perTestMu.Lock()
	d, ok := perTest[t]
	perTestMu.Unlock()
	if ok {
		return d.Pool
	}
	return New(t).Pool
}

// New clones a new database from the migrated template for t and drops it at cleanup. Every call returns
// a new, empty (schema only) database, so tests using it may run in parallel; subtests get their own.
func New(t testing.TB) *DB {
	t.Helper()
	s := shared(t)
	name := testPrefix + randomHex(8)
	if err := s.clone(t.Context(), name); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	t.Cleanup(func() { s.drop(context.WithoutCancel(t.Context()), t, name) })
	dsn := s.dsn(name)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgtest: pool config: %v", err)
	}
	cfg.MaxConns = MaxConns
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("pgtest: pool: %v", err)
	}
	d := &DB{Name: name, DSN: dsn, Pool: pool}
	perTestMu.Lock()
	perTest[t] = d
	perTestMu.Unlock()
	t.Cleanup(func() { // runs before the drop
		perTestMu.Lock()
		if perTest[t] == d {
			delete(perTest, t)
		}
		perTestMu.Unlock()
		pool.Close()
	})
	return d
}

// drop drops database name, failing t on error. It runs in cleanup, after t.Context is cancelled, so ctx
// must not carry that cancellation.
func (s *server) drop(ctx context.Context, t testing.TB, name string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := s.admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quoteIdent(name)+" WITH (FORCE)"); err != nil {
		t.Errorf("pgtest: drop database %s: %v", name, err)
	}
}

// shared returns the process's server, setting it up on first use. It skips or fails t when the server is
// unavailable.
func shared(t testing.TB) *server {
	t.Helper()
	setupOnce.Do(func() { srv, skipMsg, setupErr = setup(context.Background()) })
	if skipMsg != "" {
		t.Skip(skipMsg)
	}
	if setupErr != nil {
		t.Fatalf("pgtest: %v", setupErr)
	}
	return srv
}

// setup finds the server (MEMD_TEST_DATABASE_URL, DATABASE_URL or the shared container) and makes sure the
// migrated template exists. A non-empty skip means the tests should skip.
func setup(ctx context.Context) (s *server, skip string, err error) {
	root, err := repoRoot()
	if err != nil {
		return nil, "", err
	}
	tmpl, err := templateName(root)
	if err != nil {
		return nil, "", err
	}
	if dsn := externalDSN(); dsn != "" {
		s, err := connect(ctx, dsn, tmpl)
		if err != nil {
			return nil, "", err
		}
		return s, "", s.ensureTemplate(ctx, root)
	}
	// testcontainers logs every container step to stderr, which would land in whichever test set the server up;
	// failures still surface as errors.
	tclog.SetDefault(tclog.NewNoopLogger())
	if err := dockerHealthy(ctx); err != nil {
		if os.Getenv(RequireDockerEnv) != "" {
			return nil, "", fmt.Errorf("docker unavailable and %s is set: %w", RequireDockerEnv, err)
		}
		return nil, fmt.Sprintf("pgtest: docker unavailable (%v); start Docker or set %s", err, DatabaseURLEnv), nil
	}
	// A container being reaped at the end of an earlier binary's session, or a slow concurrent start, can fail
	// one attempt; the next one creates or finds a healthy container.
	for attempt := 1; ; attempt++ {
		s, err = startShared(ctx, root, tmpl)
		if err == nil || attempt == 3 || errors.Is(err, errMigrate) {
			return s, "", err
		}
		fmt.Fprintf(os.Stderr, "pgtest: attempt %d failed, retrying: %v\n", attempt, err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
}

// externalDSN returns the configured server URL, if any, as a libpq URL.
func externalDSN() string {
	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	// Accept the SQLAlchemy form Alembic uses.
	return strings.Replace(dsn, "postgresql+psycopg://", "postgresql://", 1)
}

// startShared finds or starts the container shared by every test binary of this `go test` run and prepares
// the template on it.
func startShared(ctx context.Context, root, tmpl string) (*server, error) {
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: containerRequest(),
		Started:          true,
		Reuse:            true,
	})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", Image, err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("container host: %w", err)
	}
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return nil, fmt.Errorf("container port: %w", err)
	}
	s, err := connect(ctx, fmt.Sprintf("postgres://memd:memd@%s:%s/postgres?sslmode=disable", host, port.Port()), tmpl)
	if err != nil {
		return nil, err
	}
	if err := s.ensureTemplate(ctx, root); err != nil {
		s.admin.Close()
		return nil, err
	}
	return s, nil
}

// containerRequest describes the shared container. Its name carries the testcontainers session id, which
// every test binary started by one `go test` invocation shares, so they find the same container; Ryuk removes
// it after the last of them exits.
func containerRequest() testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		Name:         "memd-pgtest-" + testcontainers.SessionID()[:16],
		Image:        Image,
		Env:          map[string]string{"POSTGRES_DB": "memd", "POSTGRES_USER": "memd", "POSTGRES_PASSWORD": "memd"},
		ExposedPorts: []string{"5432/tcp"},
		// The data is throwaway: durability off, and enough connections for every package's parallel tests.
		Cmd: []string{
			"postgres", "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off",
			"-c", "max_connections=1000", "-c", "max_worker_processes=16", "-c", "max_parallel_workers=16",
		},
		// tmpfs hides the image's bootstrap script (Alembic, not the image, creates the schema) and holds
		// the data directory.
		Tmpfs: map[string]string{"/docker-entrypoint-initdb.d": "rw", "/var/lib/postgresql/data": "rw"},
		// Parallel index builds (the HNSW acceptance fixture) need more than Docker's default 64 MB /dev/shm.
		HostConfigModifier: func(hc *container.HostConfig) { hc.ShmSize = 1 << 30 },
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		).WithDeadline(3 * time.Minute),
	}
}

// connect opens the admin pool over dsn.
func connect(ctx context.Context, dsn, tmpl string) (*server, error) {
	base, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("admin pool config: %w", err)
	}
	cfg.MaxConns = 8
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("admin pool: %w", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		return nil, fmt.Errorf("connect %s: %w", base.Redacted(), err)
	}
	return &server{admin: admin, base: base, template: tmpl}, nil
}

// dsn returns the URL of database name on the server.
func (s *server) dsn(name string) string {
	u := *s.base
	u.Path = "/" + name
	return u.String()
}

// ensureTemplate creates and migrates the template database unless it already exists. An advisory lock
// serializes concurrent test processes; the template is marked IS_TEMPLATE only once fully migrated, so an
// interrupted migration is redone.
func (s *server) ensureTemplate(ctx context.Context, root string) error {
	conn, err := s.admin.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrateLockKey)
	}()
	var ready bool
	err = conn.QueryRow(ctx, "SELECT datistemplate FROM pg_database WHERE datname = $1", s.template).Scan(&ready)
	if err == nil && ready {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("look up template: %w", err)
	}
	q := quoteIdent(s.template)
	if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+q+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop stale template: %w", err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+q+" TEMPLATE template0"); err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	if err := migrate(ctx, root, s.dsn(s.template)); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "ALTER DATABASE "+q+" WITH IS_TEMPLATE true"); err != nil {
		return fmt.Errorf("mark template: %w", err)
	}
	return nil
}

// clone creates database name from the template. Postgres refuses to copy a template while a session is
// still connected to it (the migration's connections close asynchronously), so that error is retried.
func (s *server) clone(ctx context.Context, name string) error {
	stmt := "CREATE DATABASE " + quoteIdent(name) + " TEMPLATE " + quoteIdent(s.template)
	for attempt := 1; ; attempt++ {
		_, err := s.admin.Exec(ctx, stmt)
		var pgErr *pgconn.PgError
		if err == nil {
			return nil
		}
		if attempt == 10 || !errors.As(err, &pgErr) || pgErr.Code != "55006" {
			return fmt.Errorf("%s: %w", stmt, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", stmt, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// templateName names the template after a hash of the migrations, so a schema change gets a new template
// (and a long-lived server keeps working across branches).
func templateName(root string) (string, error) {
	files, err := filepath.Glob(filepath.Join(root, "migrations", "versions", "*.py"))
	if err != nil {
		return "", fmt.Errorf("list migrations: %w", err)
	}
	files = append(files, filepath.Join(root, "migrations", "env.py"))
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		if err := hashFile(h, root, f); err != nil {
			return "", err
		}
	}
	return templatePrefix + hex.EncodeToString(h.Sum(nil))[:16], nil
}

// hashFile writes f's path relative to root and its content to h.
func hashFile(h io.Writer, root, f string) error {
	rel, err := filepath.Rel(root, f)
	if err != nil {
		return fmt.Errorf("hash migrations: %w", err)
	}
	b, err := os.ReadFile(f) //nolint:gosec // a migration file of this repository
	if err != nil {
		return fmt.Errorf("hash migrations: %w", err)
	}
	_, _ = fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
	_, _ = h.Write(b)
	return nil
}

// migrate runs `alembic upgrade head` from the repository root against dsn. MEMD_ALEMBIC overrides the
// alembic binary (default .venv/bin/alembic under the root).
func migrate(ctx context.Context, root, dsn string) error {
	bin := os.Getenv("MEMD_ALEMBIC")
	if bin == "" {
		bin = filepath.Join(root, ".venv", "bin", "alembic")
	}
	// The path is the repo .venv or the developer's MEMD_ALEMBIC, never request input.
	if _, err := os.Stat(bin); err != nil { //nolint:gosec // test helper, see above
		return fmt.Errorf("%w: alembic not found at %s (run `make tools`): %w", errMigrate, bin, err)
	}
	// Fixed arguments; bin is the repo alembic or MEMD_ALEMBIC.
	cmd := exec.CommandContext(ctx, bin, "-c", "migrations/alembic.ini", "upgrade", "head") //nolint:gosec // test helper
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "MEMD_DSN="+dsn)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: alembic upgrade head: %w\n%s", errMigrate, err, out)
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

// quoteIdent quotes a database name.
func quoteIdent(s string) string { return pgx.Identifier{s}.Sanitize() }

// randomHex returns n random bytes in hex.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b)
}
