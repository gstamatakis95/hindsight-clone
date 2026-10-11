package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"memd/test/e2e/gatewayemu"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// memd run modes (SPEC "Process model").
const (
	// ModeAPI is the gRPC server.
	ModeAPI = "api"
	// ModeWorker is the job runner and maintenance tick.
	ModeWorker = "worker"
	// ModeAll is both in one process.
	ModeAll = "all"
)

// Environment the harness passes to memd (decision 1411). cmd/memd (WP-14b) reads these names; changing one
// means changing it here and there together.
const (
	// EnvDSN is the Postgres URL.
	EnvDSN = "MEMD_DSN"
	// EnvGRPCAddr is the api's listen address (host:port).
	EnvGRPCAddr = "MEMD_GRPC_ADDR"
	// EnvGatewayURL is the gateway base URL including /v1.
	EnvGatewayURL = "MEMD_GATEWAY_URL"
	// EnvGatewayAPIKey is the gateway bearer key.
	EnvGatewayAPIKey = "MEMD_GATEWAY_API_KEY"
	// EnvS3Endpoint is the S3 API base URL.
	EnvS3Endpoint = "MEMD_S3_ENDPOINT"
	// EnvS3Bucket is the bucket.
	EnvS3Bucket = "MEMD_S3_BUCKET"
	// EnvS3AccessKey is the static access key.
	EnvS3AccessKey = "MEMD_S3_ACCESS_KEY"
	// EnvS3SecretKey is the static secret key.
	EnvS3SecretKey = "MEMD_S3_SECRET_KEY" //nolint:gosec // an environment variable name, not a credential
	// EnvS3Region is the signing region.
	EnvS3Region = "MEMD_S3_REGION"
	// EnvS3PathStyle selects path-style addressing ("true" for MinIO).
	EnvS3PathStyle = "MEMD_S3_PATH_STYLE"
	// EnvModelPrefix + upper-case slot binds a slot's model (config.EnvModelPrefix).
	EnvModelPrefix = "MEMD_MODEL_"
)

// Slots is every bindable slot (domain.ModelSlot values); the harness binds each to gatewayemu.ModelFor(slot).
var Slots = []string{
	"extract", "time_parse", "consolidate", "dedup_check", "reflect", "structured_output", "mental_model_delta",
	"synthesize", "rerank",
}

// placeholderMarker is what the cmd/memd placeholder prints before exiting 1.
const placeholderMarker = "not implemented"

// Timeouts of memd processes.
const (
	buildTimeout = 5 * time.Minute
	probeTimeout = 10 * time.Second
	readyTimeout = 90 * time.Second
	stopTimeout  = 15 * time.Second
	// workerGrace is how long a worker must stay up to count as started (it serves no health endpoint).
	workerGrace = 2 * time.Second
)

var (
	buildOnce sync.Once
	buildBin  string
	buildErr  error
	buildSkip string
)

// BuildMemd builds ./cmd/memd once per test binary and returns its path. It skips the test, with the reason,
// when the binary can't be built or is still the placeholder that only prints "not implemented".
func BuildMemd(t testing.TB) string {
	t.Helper()
	root := RepoRoot(t)
	buildOnce.Do(func() { buildBin, buildSkip, buildErr = build(root) })
	if buildErr != nil {
		t.Fatalf("harness: %v", buildErr)
	}
	if buildSkip != "" {
		t.Skip(buildSkip)
	}
	return buildBin
}

// build compiles memd into a temporary directory and probes it. A non-empty skip says why e2e can't run memd.
func build(root string) (bin, skip string, err error) {
	dir, err := os.MkdirTemp("", "memd-e2e-bin-")
	if err != nil {
		return "", "", fmt.Errorf("temp dir: %w", err)
	}
	bin = filepath.Join(dir, "memd")
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/memd") //nolint:gosec // bin is our temp dir
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Sprintf("harness: memd can't be built yet (cmd/memd lands in WP-14b): %v\n%s", err, out), nil
	}
	pctx, pcancel := context.WithTimeout(context.Background(), probeTimeout)
	defer pcancel()
	// The placeholder exits at once whatever the arguments; a real memd prints usage or fails on config.
	out, _ := exec.CommandContext(pctx, bin, ModeAll).CombinedOutput()
	if bytes.Contains(out, []byte(placeholderMarker)) {
		return "", "harness: cmd/memd is still the placeholder (WP-14b wires it): " + strings.TrimSpace(string(out)), nil
	}
	return bin, "", nil
}

// StartEmulator starts the emulated gateway and closes it with the test (after memd processes started later).
func StartEmulator(t testing.TB, opts gatewayemu.Options) (*gatewayemu.Server, *httptest.Server) {
	t.Helper()
	emu, ts := gatewayemu.Start(opts)
	t.Cleanup(ts.Close)
	return emu, ts
}

// MemdEnv is the environment memd needs for this stack and a gateway at gatewayURL (base URL with /v1): DSN,
// S3, gateway and every slot bound to its emulator model.
func (s *Stack) MemdEnv(gatewayURL string) map[string]string {
	env := map[string]string{
		EnvDSN: s.DSN, EnvGatewayURL: gatewayURL, EnvGatewayAPIKey: "e2e",
		EnvS3Endpoint: s.S3Endpoint, EnvS3Bucket: S3Bucket, EnvS3AccessKey: S3AccessKey, EnvS3SecretKey: S3SecretKey,
		EnvS3Region: S3Region, EnvS3PathStyle: "true",
	}
	for _, slot := range Slots {
		env[EnvModelPrefix+strings.ToUpper(slot)] = gatewayemu.ModelFor(slot)
	}
	return env
}

// MemdOptions configures one memd process.
type MemdOptions struct {
	// Mode is ModeAPI, ModeWorker or ModeAll (default ModeAll).
	Mode string
	// GatewayURL is the gateway base URL including /v1, e.g. emulator URL + gatewayemu.BasePath.
	GatewayURL string
	// Env adds to or overrides MemdEnv.
	Env map[string]string
	// Args follow the mode on the command line.
	Args []string
}

// Proc is a running memd subprocess.
type Proc struct {
	// Mode is the run mode.
	Mode string
	// Addr is the api's gRPC address (api and all modes).
	Addr string

	t       testing.TB
	bin     string
	env     []string
	args    []string
	logPath string
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

// StartMemd starts memd from bin against the stack and waits until it is ready: the gRPC health check
// SERVING for api and all, still running after a short grace for worker. The process is killed with the
// test. It skips the test when memd exits early as the placeholder, and fails it on any other early exit.
func (s *Stack) StartMemd(t testing.TB, bin string, o MemdOptions) *Proc {
	t.Helper()
	if o.Mode == "" {
		o.Mode = ModeAll
	}
	env := s.MemdEnv(o.GatewayURL)
	p := &Proc{Mode: o.Mode, t: t, bin: bin, args: append([]string{o.Mode}, o.Args...)}
	if o.Mode != ModeWorker {
		ports, err := freePorts(1)
		if err != nil {
			t.Fatalf("harness: %v", err)
		}
		p.Addr = "127.0.0.1:" + strconv.Itoa(ports[0])
		env[EnvGRPCAddr] = p.Addr
	}
	maps.Copy(env, o.Env)
	p.env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(env)) {
		p.env = append(p.env, k+"="+env[k])
	}
	p.logPath = filepath.Join(t.TempDir(), "memd-"+o.Mode+".log")
	t.Cleanup(func() { _ = p.Kill() })
	p.start()
	return p
}

// start launches the process and waits until it is ready.
func (p *Proc) start() {
	p.t.Helper()
	logs, err := os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		p.t.Fatalf("harness: memd log: %v", err)
	}
	// The binary is the one BuildMemd built; the arguments are the test's own.
	cmd := exec.Command(p.bin, p.args...) //nolint:gosec // harness-built binary and args
	cmd.Env, cmd.Stdout, cmd.Stderr = p.env, logs, logs
	if err := cmd.Start(); err != nil {
		_ = logs.Close()
		p.t.Fatalf("harness: start memd %s: %v", p.Mode, err)
	}
	done := make(chan struct{})
	p.mu.Lock()
	p.cmd, p.done, p.waitErr = cmd, done, nil
	p.mu.Unlock()
	go func() {
		err := cmd.Wait()
		_ = logs.Close()
		p.mu.Lock()
		p.waitErr = err
		p.mu.Unlock()
		close(done)
	}()
	if err := p.waitReady(done); err != nil {
		p.earlyExit(err)
	}
}

// earlyExit skips the test when memd is the placeholder, and fails it otherwise.
func (p *Proc) earlyExit(err error) {
	p.t.Helper()
	logs := p.Logs()
	if strings.Contains(logs, placeholderMarker) {
		p.t.Skipf("harness: cmd/memd is still the placeholder (WP-14b wires it): %s", strings.TrimSpace(logs))
	}
	p.t.Fatalf("harness: memd %s not ready: %v\n--- memd log ---\n%s", p.Mode, err, logs)
}

// waitReady waits for the health check (api, all) or the worker grace period, failing if the process exits.
func (p *Proc) waitReady(done <-chan struct{}) error {
	if p.Mode == ModeWorker {
		select {
		case <-done:
			return fmt.Errorf("exited: %w", p.exitErr())
		case <-time.After(workerGrace):
			return nil
		}
	}
	conn, err := grpc.NewClient(p.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", p.Addr, err)
	}
	defer func() { _ = conn.Close() }()
	health := healthpb.NewHealthClient(conn)
	deadline := Deadline(p.t, readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return fmt.Errorf("exited: %w", p.exitErr())
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := health.Check(ctx, &healthpb.HealthCheckRequest{})
		cancel()
		if err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("health check not SERVING by %s", deadline.Format(time.TimeOnly))
}

// exitErr is the process's exit error (nil for exit 0, which is still an early exit).
func (p *Proc) exitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waitErr == nil {
		return errors.New("exit status 0")
	}
	return p.waitErr
}

// Kill sends SIGKILL (a crash: no graceful shutdown, claimed jobs stay claimed) and waits for the exit.
func (p *Proc) Kill() error { return p.signal(syscall.SIGKILL) }

// Stop sends SIGTERM and waits for a graceful exit, killing the process after a timeout.
func (p *Proc) Stop() error { return p.signal(syscall.SIGTERM) }

// signal delivers sig and waits for the process to exit (SIGKILL after stopTimeout).
func (p *Proc) signal(sig syscall.Signal) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	if err := cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal memd %s: %w", p.Mode, err)
	}
	select {
	case <-done:
		return nil
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()
		<-done
		return fmt.Errorf("memd %s ignored %v for %s and was killed", p.Mode, sig, stopTimeout)
	}
}

// Restart starts the process again with the same mode, address and environment (after Kill or Stop; a
// running process is killed first) and waits until it is ready.
func (p *Proc) Restart() {
	p.t.Helper()
	if err := p.Kill(); err != nil {
		p.t.Fatalf("harness: %v", err)
	}
	p.start()
}

// Exited reports whether the process has exited.
func (p *Proc) Exited() bool {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// Logs returns everything the process (and earlier runs of it) wrote to stdout and stderr.
func (p *Proc) Logs() string {
	b, err := os.ReadFile(p.logPath)
	if err != nil {
		return fmt.Sprintf("(no log: %v)", err)
	}
	return string(b)
}
