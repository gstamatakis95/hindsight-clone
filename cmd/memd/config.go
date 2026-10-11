package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"memd/internal/config"
	"memd/internal/domain"
	"strconv"
	"strings"
	"time"
)

// Run modes (SPEC "Process model").
const (
	// modeAPI serves gRPC: Recall, Reflect, sync Retain and every admin RPC.
	modeAPI = "api"
	// modeWorker runs the job runner and the maintenance tick.
	modeWorker = "worker"
	// modeAll runs both in one process.
	modeAll = "all"
)

// Environment variables memd reads; each has a flag of the same name in lower case without the MEMD_
// prefix, with dashes for underscores (MEMD_GRPC_ADDR = -grpc-addr). A flag wins over the environment. The
// model bindings (MEMD_MODEL, MEMD_MODEL_<SLOT>) are read by config.ServerDefaults. The e2e harness relies
// on these names (decision 1411).
const (
	envDSN                 = "MEMD_DSN"
	envGRPCAddr            = "MEMD_GRPC_ADDR"
	envGatewayURL          = "MEMD_GATEWAY_URL"
	envGatewayAPIKey       = "MEMD_GATEWAY_API_KEY" //nolint:gosec // an environment variable name, not a credential
	envEmbedModel          = "MEMD_EMBED_MODEL"
	envGatewayConcurrency  = "MEMD_GATEWAY_MAX_CONCURRENT"
	envGatewayTimeout      = "MEMD_GATEWAY_TIMEOUT"
	envS3Endpoint          = "MEMD_S3_ENDPOINT"
	envS3Bucket            = "MEMD_S3_BUCKET"
	envS3AccessKey         = "MEMD_S3_ACCESS_KEY"
	envS3SecretKey         = "MEMD_S3_SECRET_KEY" //nolint:gosec // an environment variable name, not a credential
	envS3Region            = "MEMD_S3_REGION"
	envS3PathStyle         = "MEMD_S3_PATH_STYLE"
	envWorkerID            = "MEMD_WORKER_ID"
	envWorkerSlots         = "MEMD_WORKER_SLOTS"
	envPollInterval        = "MEMD_POLL_INTERVAL"
	envHeartbeatInterval   = "MEMD_HEARTBEAT_INTERVAL"
	envStaleAfter          = "MEMD_STALE_AFTER"
	envMaintenanceInterval = "MEMD_MAINTENANCE_INTERVAL"
	envConsolidationGrace  = "MEMD_CONSOLIDATION_RECONCILE_GRACE"
	envWorkerHealthAddr    = "MEMD_WORKER_HEALTH_ADDR"
	envRPCDeadline         = "MEMD_RPC_DEADLINE"
	envDBMaxConns          = "MEMD_DB_MAX_CONNS"
	envLogLevel            = "MEMD_LOG_LEVEL"
	envDatadogEnabled      = "MEMD_DATADOG_ENABLED"
	envDatadogProfiler     = "MEMD_DATADOG_PROFILER"
)

// Defaults of the settings the spec fixes or leaves to the deployment.
const (
	defaultGRPCAddr          = ":50051"
	defaultEmbedModel        = "nomic-embed-text-v1.5"
	defaultGatewayConcurrent = 32
	defaultGatewayTimeout    = 120 * time.Second
	defaultS3Region          = "us-east-1"
	defaultWorkerSlots       = 10
	defaultPollInterval      = 500 * time.Millisecond
	defaultHeartbeat         = 15 * time.Second
	defaultStaleAfter        = 2 * time.Minute
	defaultMaintenance       = 60 * time.Second
	defaultConsolidationWait = 10 * time.Minute
	defaultRPCDeadline       = 120 * time.Second
)

// Config is memd's process configuration: environment variables, overridden by flags.
type Config struct {
	// Mode is api, worker or all.
	Mode string
	// DSN is the Postgres URL.
	DSN string
	// GRPCAddr is the api's listen address.
	GRPCAddr string
	// GatewayURL is the AI gateway base URL including /v1.
	GatewayURL string
	// GatewayAPIKey is sent as a bearer token when set.
	GatewayAPIKey string
	// EmbedModel is the server-wide embedding model (768 dimensions).
	EmbedModel string
	// GatewayMaxConcurrent caps in-flight gateway calls in this process.
	GatewayMaxConcurrent int
	// SlotConcurrency caps in-flight gateway calls per slot (MEMD_GATEWAY_MAX_CONCURRENT_<SLOT>, e.g.
	// MEMD_GATEWAY_MAX_CONCURRENT_EXTRACT); slots without an entry are limited only by GatewayMaxConcurrent.
	SlotConcurrency map[domain.ModelSlot]int
	// GatewayTimeout bounds one gateway HTTP attempt when the binding sets none.
	GatewayTimeout time.Duration
	// S3Endpoint is the object store's S3 API URL; empty = AWS for S3Region.
	S3Endpoint string
	// S3Bucket holds the original document texts.
	S3Bucket string
	// S3AccessKey is the static access key. The S3 adapter signs with static credentials only (no AWS
	// default credential chain), so it is required, together with S3SecretKey (decision 2004).
	S3AccessKey string
	// S3SecretKey is the static secret key; required.
	S3SecretKey string
	// S3Region is the signing region.
	S3Region string
	// S3PathStyle addresses objects path-style (MinIO).
	S3PathStyle bool
	// WorkerID is the prefix of this worker's id on claimed rows; empty = the hostname. Every process appends
	// its own uuid (effectiveWorkerID), so two workers given the same MEMD_WORKER_ID never share an id and
	// heartbeat fencing keeps working (decision 2004).
	WorkerID string
	// WorkerSlots is how many jobs the worker runs at once.
	WorkerSlots int
	// PollInterval is the worker's claim period.
	PollInterval time.Duration
	// HeartbeatInterval is the worker's heartbeat period.
	HeartbeatInterval time.Duration
	// StaleAfter is the reclaim threshold; the runner's self-fence and the maintenance reclaim both use it.
	StaleAfter time.Duration
	// MaintenanceInterval is the maintenance tick period.
	MaintenanceInterval time.Duration
	// ConsolidationGrace is how old an unconsolidated unit must be before the maintenance tick consolidates
	// its bank (decision 2000).
	ConsolidationGrace time.Duration
	// WorkerHealthAddr, when set, is where the worker serves grpc.health.v1 (worker and all modes).
	WorkerHealthAddr string
	// RPCDeadline applies to calls without a deadline; 0 = none.
	RPCDeadline time.Duration
	// DBMaxConns caps the Postgres pool; 0 = sized from the worker slots.
	DBMaxConns int
	// LogLevel is debug, info, warn or error.
	LogLevel slog.Level
	// DatadogEnabled turns on Datadog tracing, log correlation and runtime metrics (internal/observability);
	// the agent address and the rest come from the standard DD_* variables. Off by default.
	DatadogEnabled bool
	// DatadogProfiler also starts the Datadog profiler; ignored unless DatadogEnabled.
	DatadogProfiler bool
	// Env is the environment lookup, kept for config.ServerDefaults (model bindings).
	Env func(string) string
}

// setting is one configuration knob with its environment variable and flag.
type setting struct {
	env  string
	help string
	def  string
	set  func(c *Config, v string) error
}

// settings lists every knob in the order of the usage text.
var settings = []setting{
	{envDSN, "Postgres URL (required)", "", str(func(c *Config) *string { return &c.DSN })},
	{
		envGRPCAddr, "gRPC listen address (api, all)", defaultGRPCAddr,
		str(func(c *Config) *string { return &c.GRPCAddr }),
	},
	{
		envGatewayURL, "AI gateway base URL including /v1 (required)", "",
		str(func(c *Config) *string { return &c.GatewayURL }),
	},
	{envGatewayAPIKey, "AI gateway bearer key", "", str(func(c *Config) *string { return &c.GatewayAPIKey })},
	{
		envEmbedModel, "embedding model (768 dimensions)", defaultEmbedModel,
		str(func(c *Config) *string { return &c.EmbedModel }),
	},
	{
		envGatewayConcurrency, "max in-flight gateway calls", strconv.Itoa(defaultGatewayConcurrent),
		integer(func(c *Config) *int { return &c.GatewayMaxConcurrent }),
	},
	{
		envGatewayTimeout, "gateway HTTP attempt timeout", defaultGatewayTimeout.String(),
		duration(func(c *Config) *time.Duration { return &c.GatewayTimeout }),
	},
	{envS3Endpoint, "S3 API URL (empty = AWS)", "", str(func(c *Config) *string { return &c.S3Endpoint })},
	{envS3Bucket, "bucket for original texts (required)", "", str(func(c *Config) *string { return &c.S3Bucket })},
	{envS3AccessKey, "S3 access key (required)", "", str(func(c *Config) *string { return &c.S3AccessKey })},
	{envS3SecretKey, "S3 secret key (required)", "", str(func(c *Config) *string { return &c.S3SecretKey })},
	{envS3Region, "S3 signing region", defaultS3Region, str(func(c *Config) *string { return &c.S3Region })},
	{
		envS3PathStyle, "path-style S3 addressing (MinIO)", "false",
		boolean(func(c *Config) *bool { return &c.S3PathStyle }),
	},
	{
		envWorkerID, "worker id prefix on claimed rows (empty = hostname; a per-process uuid is appended)", "",
		str(func(c *Config) *string { return &c.WorkerID }),
	},
	{
		envWorkerSlots, "jobs a worker runs at once", strconv.Itoa(defaultWorkerSlots),
		integer(func(c *Config) *int { return &c.WorkerSlots }),
	},
	{
		envPollInterval, "worker claim period", defaultPollInterval.String(),
		duration(func(c *Config) *time.Duration { return &c.PollInterval }),
	},
	{
		envHeartbeatInterval, "worker heartbeat period", defaultHeartbeat.String(),
		duration(func(c *Config) *time.Duration { return &c.HeartbeatInterval }),
	},
	{
		envStaleAfter, "heartbeat age after which a job is reclaimed", defaultStaleAfter.String(),
		duration(func(c *Config) *time.Duration { return &c.StaleAfter }),
	},
	{
		envMaintenanceInterval, "maintenance tick period", defaultMaintenance.String(),
		duration(func(c *Config) *time.Duration { return &c.MaintenanceInterval }),
	},
	{
		envConsolidationGrace, "age of an unconsolidated unit before the maintenance tick consolidates its bank",
		defaultConsolidationWait.String(), duration(func(c *Config) *time.Duration { return &c.ConsolidationGrace }),
	},
	{
		envWorkerHealthAddr, "worker grpc.health listen address (empty = none)", "",
		str(func(c *Config) *string { return &c.WorkerHealthAddr }),
	},
	{
		envRPCDeadline, "deadline of calls that carry none (0 = none)", defaultRPCDeadline.String(),
		duration(func(c *Config) *time.Duration { return &c.RPCDeadline }),
	},
	{
		envDBMaxConns, "Postgres pool size (0 = from the worker slots)", "0",
		integer(func(c *Config) *int { return &c.DBMaxConns }),
	},
	{envLogLevel, "log level: debug, info, warn, error", "info", logLevel},
	{
		envDatadogEnabled, "Datadog tracing, log correlation and runtime metrics (agent from DD_* variables)",
		"false", boolean(func(c *Config) *bool { return &c.DatadogEnabled }),
	},
	{
		envDatadogProfiler, "Datadog continuous profiler (only with Datadog enabled)", "false",
		boolean(func(c *Config) *bool { return &c.DatadogProfiler }),
	},
}

// str sets a string field.
func str(field func(*Config) *string) func(*Config, string) error {
	return func(c *Config, v string) error { *field(c) = v; return nil }
}

// integer parses an int field.
func integer(field func(*Config) *int) func(*Config, string) error {
	return func(c *Config, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("not an integer: %q", v)
		}
		*field(c) = n
		return nil
	}
}

// duration parses a time.Duration field ("90s", "2m").
func duration(field func(*Config) *time.Duration) func(*Config, string) error {
	return func(c *Config, v string) error {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("not a duration: %q", v)
		}
		*field(c) = d
		return nil
	}
}

// boolean parses a bool field.
func boolean(field func(*Config) *bool) func(*Config, string) error {
	return func(c *Config, v string) error {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("not a boolean: %q", v)
		}
		*field(c) = b
		return nil
	}
}

// logLevel parses the log level.
func logLevel(c *Config, v string) error {
	if err := c.LogLevel.UnmarshalText([]byte(strings.TrimSpace(v))); err != nil {
		return fmt.Errorf("not a log level: %q", v)
	}
	return nil
}

// flagName is the flag of an environment variable: MEMD_GRPC_ADDR -> grpc-addr.
func flagName(env string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(env, "MEMD_")), "_", "-")
}

// errUsage reports a command line that names no valid mode; run prints the usage for it.
var errUsage = errors.New("usage")

// usage writes the command's usage.
func usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "usage: memd <%s|%s|%s> [flags]\n\nSettings (flag, environment variable, default):\n",
		modeAPI, modeWorker, modeAll)
	for _, s := range settings {
		_, _ = fmt.Fprintf(w, "  -%s, %s (default %q)\n      %s\n", flagName(s.env), s.env, s.def, s.help)
	}
	_, _ = fmt.Fprintf(w, "  MEMD_MODEL, MEMD_MODEL_<SLOT>\n      gateway model of every LLM slot / of one slot "+
		"(EXTRACT and REFLECT must be bound)\n")
	_, _ = fmt.Fprintf(w, "  %s_<SLOT>\n      max in-flight gateway calls of one slot (EXTRACT, ..., RERANK, EMBED)\n",
		envGatewayConcurrency)
}

// LoadConfig reads the mode from args[0], then every setting from env and the flags after the mode (a flag
// wins). It does not validate; call Validate.
func LoadConfig(args []string, env func(string) string) (Config, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Config{}, errUsage
	}
	c := Config{Mode: args[0], Env: env}
	values := map[string]string{}
	for _, s := range settings {
		values[s.env] = s.def
		if v, ok := lookup(env, s.env); ok {
			values[s.env] = v
		}
	}
	fs := flag.NewFlagSet("memd "+c.Mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, s := range settings {
		fs.Func(flagName(s.env), s.help, func(v string) error { values[s.env] = v; return nil })
	}
	if err := fs.Parse(args[1:]); err != nil {
		return Config{}, fmt.Errorf("memd: %w", err)
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("memd: unexpected arguments %q", fs.Args())
	}
	for _, s := range settings {
		if err := s.set(&c, values[s.env]); err != nil {
			return Config{}, fmt.Errorf("memd: %s: %w", s.env, err)
		}
	}
	c.DSN = normalizeDSN(c.DSN)
	slots, err := slotConcurrency(env)
	if err != nil {
		return Config{}, err
	}
	c.SlotConcurrency = slots
	return c, nil
}

// normalizeDSN drops a SQLAlchemy driver suffix from the URL scheme ("postgresql+psycopg://" ->
// "postgresql://"), so the one MEMD_DSN the Makefile exports for Alembic also works for memd.
func normalizeDSN(dsn string) string {
	scheme, rest, ok := strings.Cut(dsn, "://")
	if !ok {
		return dsn
	}
	if base, _, driver := strings.Cut(scheme, "+"); driver {
		return base + "://" + rest
	}
	return dsn
}

// lookup returns env's value of key when set and non-empty.
func lookup(env func(string) string, key string) (string, bool) {
	v := env(key)
	return v, v != ""
}

// Validate checks the configuration before anything connects: a known mode, the required endpoints, and
// worker timings that keep the runner's self-fence ahead of the reclaim. The 768-dimension check against the
// gateway needs the network, so buildDeps runs it (checkEmbedding) right after the gateway client exists.
func (c Config) Validate() error {
	var errs []error
	switch c.Mode {
	case modeAPI, modeWorker, modeAll:
	default:
		return fmt.Errorf("%w: unknown mode %q", errUsage, c.Mode)
	}
	if c.DSN == "" {
		errs = append(errs, fmt.Errorf("%s is required", envDSN))
	}
	if c.GatewayURL == "" {
		errs = append(errs, fmt.Errorf("%s is required", envGatewayURL))
	}
	if c.S3Bucket == "" {
		errs = append(errs, fmt.Errorf("%s is required", envS3Bucket))
	}
	if c.S3AccessKey == "" || c.S3SecretKey == "" {
		errs = append(errs, fmt.Errorf("%s and %s are both required (static S3 credentials)", envS3AccessKey,
			envS3SecretKey))
	}
	errs = append(errs, c.validateSlots()...)
	if c.EmbedModel == "" {
		errs = append(errs, fmt.Errorf("%s must not be empty", envEmbedModel))
	}
	if c.Mode != modeWorker && c.GRPCAddr == "" {
		errs = append(errs, fmt.Errorf("%s is required in mode %s", envGRPCAddr, c.Mode))
	}
	errs = append(errs, c.validateWorker()...)
	if c.GatewayMaxConcurrent <= 0 || c.GatewayTimeout <= 0 || c.DBMaxConns < 0 || c.RPCDeadline < 0 {
		errs = append(errs, errors.New("gateway concurrency and timeout must be positive; pool size and "+
			"RPC deadline must not be negative"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("memd: invalid config: %w", err)
	}
	return nil
}

// gatewaySlots are the slots MEMD_GATEWAY_MAX_CONCURRENT_<SLOT> can cap.
var gatewaySlots = []domain.ModelSlot{
	domain.ModelSlotExtract, domain.ModelSlotTimeParse, domain.ModelSlotConsolidate, domain.ModelSlotDedupCheck,
	domain.ModelSlotReflect, domain.ModelSlotStructuredOutput, domain.ModelSlotMentalModelDelta,
	domain.ModelSlotSynthesize, domain.ModelSlotRerank, domain.ModelSlotEmbed,
}

// slotConcurrency reads MEMD_GATEWAY_MAX_CONCURRENT_<SLOT> for every gateway slot; unset slots get no entry.
func slotConcurrency(env func(string) string) (map[domain.ModelSlot]int, error) {
	out := map[domain.ModelSlot]int{}
	for _, slot := range gatewaySlots {
		key := envGatewayConcurrency + "_" + strings.ToUpper(string(slot))
		v, ok := lookup(env, key)
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("memd: %s: not an integer: %q", key, v)
		}
		out[slot] = n
	}
	return out, nil
}

// requiredSlots must have a model at the server level: retain (EXTRACT) and reflect (REFLECT) can't run
// without one, so memd refuses to start rather than fail the first call.
var requiredSlots = []domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotReflect}

// validateSlots checks the per-slot gateway caps are positive and that the required slots are bound in the
// environment (config.ServerDefaults reads MEMD_MODEL and MEMD_MODEL_<SLOT>).
func (c Config) validateSlots() []error {
	var errs []error
	for slot, n := range c.SlotConcurrency {
		if n <= 0 {
			errs = append(errs, fmt.Errorf("%s_%s must be positive", envGatewayConcurrency, strings.ToUpper(string(slot))))
		}
	}
	if c.Env == nil {
		return errs
	}
	server, err := config.ServerDefaults(c.Env)
	if err != nil {
		return append(errs, err)
	}
	for _, slot := range requiredSlots {
		if _, ok := server.Binding(slot); !ok {
			errs = append(errs, fmt.Errorf("no model bound to slot %s: set MEMD_MODEL or %s%s", slot,
				config.EnvModelPrefix, strings.ToUpper(string(slot))))
		}
	}
	return errs
}

// validateWorker checks the worker timings: positive, and a heartbeat well inside the reclaim threshold
// (the runner fences itself at 3/4 of StaleAfter, so a heartbeat must land before that).
func (c Config) validateWorker() []error {
	var errs []error
	if c.WorkerSlots <= 0 || c.PollInterval <= 0 || c.HeartbeatInterval <= 0 || c.StaleAfter <= 0 ||
		c.MaintenanceInterval <= 0 || c.ConsolidationGrace <= 0 {
		errs = append(errs, errors.New("worker slots, poll, heartbeat, stale-after, maintenance intervals and "+
			"the consolidation grace must be positive"))
	}
	if c.HeartbeatInterval > 0 && c.StaleAfter > 0 && c.HeartbeatInterval*4 >= c.StaleAfter*3 {
		errs = append(errs, fmt.Errorf("%s (%s) must be below 3/4 of %s (%s)",
			envHeartbeatInterval, c.HeartbeatInterval, envStaleAfter, c.StaleAfter))
	}
	return errs
}
