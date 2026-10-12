package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// Run modes (docs/design/architecture.md#process-model).
const (
	// modeAPI serves gRPC: Recall, Reflect, sync Retain and every admin RPC.
	modeAPI = "api"
	// modeWorker runs the job runner and the maintenance tick.
	modeWorker = "worker"
	// modeIngest runs the queue consumer (internal/ingestapi) over the source MEMD_INGEST_PROVIDER selects.
	modeIngest = "ingest"
	// modeAll runs the api and the worker in one process, plus the queue consumer when a provider is set.
	modeAll = "all"
)

// Environment variables memd reads: deployment, infrastructure and secrets only (behaviour lives in the server
// config file, MEMD_CONFIG). Each has a flag of the same name in lower case without the MEMD_ prefix, with dashes
// for underscores (MEMD_GRPC_ADDR = -grpc-addr). A flag wins over the environment. The e2e harness relies on these
// names.
const (
	envDSN              = "MEMD_DSN"
	envConfig           = "MEMD_CONFIG"
	envGRPCAddr         = "MEMD_GRPC_ADDR"
	envGatewayURL       = "MEMD_GATEWAY_URL"
	envGatewayAPIKey    = "MEMD_GATEWAY_API_KEY" //nolint:gosec // an environment variable name, not a credential
	envBlobProvider     = "MEMD_BLOB_PROVIDER"
	envS3Endpoint       = "MEMD_S3_ENDPOINT"
	envS3Bucket         = "MEMD_S3_BUCKET"
	envS3AccessKey      = "MEMD_S3_ACCESS_KEY"
	envS3SecretKey      = "MEMD_S3_SECRET_KEY" //nolint:gosec // an environment variable name, not a credential
	envS3Region         = "MEMD_S3_REGION"
	envS3PathStyle      = "MEMD_S3_PATH_STYLE"
	envWorkerID         = "MEMD_WORKER_ID"
	envWorkerSlots      = "MEMD_WORKER_SLOTS"
	envWorkerHealthAddr = "MEMD_WORKER_HEALTH_ADDR"
	envDBMaxConns       = "MEMD_DB_MAX_CONNS"
	envLogLevel         = "MEMD_LOG_LEVEL"
	envLogFormat        = "MEMD_LOG_FORMAT"
	envDatadogEnabled   = "MEMD_DATADOG_ENABLED"
	envDatadogProfiler  = "MEMD_DATADOG_PROFILER"
	envDatadogMetrics   = "MEMD_DATADOG_METRICS"
	envIngestProvider   = "MEMD_INGEST_PROVIDER"
	envIngestTenant     = "MEMD_INGEST_TENANT"
	envIngestOrg        = "MEMD_INGEST_ORG"
	envIngestHealthAddr = "MEMD_INGEST_HEALTH_ADDR"
	envKafkaBrokers     = "MEMD_INGEST_KAFKA_BROKERS"
	envKafkaTopic       = "MEMD_INGEST_KAFKA_TOPIC"
	envKafkaGroup       = "MEMD_INGEST_KAFKA_GROUP"
	envKafkaDLQTopic    = "MEMD_INGEST_KAFKA_DLQ_TOPIC"
	envKafkaClientID    = "MEMD_INGEST_KAFKA_CLIENT_ID"
	envKafkaTLS         = "MEMD_INGEST_KAFKA_TLS"
	envKafkaTLSCA       = "MEMD_INGEST_KAFKA_TLS_CA_FILE"
	envKafkaTLSCert     = "MEMD_INGEST_KAFKA_TLS_CERT_FILE"
	envKafkaTLSKey      = "MEMD_INGEST_KAFKA_TLS_KEY_FILE"
	envKafkaSASLMech    = "MEMD_INGEST_KAFKA_SASL_MECHANISM"
	envKafkaSASLUser    = "MEMD_INGEST_KAFKA_SASL_USERNAME"
	//nolint:gosec // an environment variable name, not a credential
	envKafkaSASLPass = "MEMD_INGEST_KAFKA_SASL_PASSWORD"
)

// Ingest providers (MEMD_INGEST_PROVIDER).
const (
	// ingestProviderNone runs no queue consumer: memd all skips it and memd ingest refuses to start.
	ingestProviderNone = "none"
	// ingestProviderKafka consumes a Kafka topic with a consumer group (internal/ingest/kafka).
	ingestProviderKafka = "kafka"
)

// Log formats (MEMD_LOG_FORMAT). Unset, the format is json with Datadog enabled and text otherwise.
const (
	// logFormatText is slog's key=value text, one record per line.
	logFormatText = "text"
	// logFormatJSON is one JSON object per line with Datadog's reserved attribute names
	// (observability.NewJSONHandler).
	logFormatJSON = "json"
)

// Defaults of the deployment settings.
const (
	defaultGRPCAddr    = ":50051"
	defaultS3Region    = "us-east-1"
	blobProviderS3     = "s3"
	defaultWorkerSlots = 10
	defaultKafkaGroup  = "memd-ingest"
)

// Config is memd's process configuration: environment variables, overridden by flags, plus the server config
// file they point to.
type Config struct {
	// Mode is api, worker or all.
	Mode string
	// ConfigPath is the server config file (MEMD_CONFIG); empty = ./memd.yaml, which may be absent.
	ConfigPath string
	// File is the server config file's content, with the section defaults applied.
	File ServerFile
	// DSN is the Postgres URL.
	DSN string
	// GRPCAddr is the api's listen address.
	GRPCAddr string
	// GatewayURL is the AI gateway base URL including /v1.
	GatewayURL string
	// GatewayAPIKey is sent as a bearer token when set.
	GatewayAPIKey string
	// BlobProvider selects the blob.Store adapter; "s3" (S3 or any S3-compatible store) is the only one.
	BlobProvider string
	// S3Endpoint is the object store's S3 API URL; empty = AWS for S3Region.
	S3Endpoint string
	// S3Bucket holds the original document texts.
	S3Bucket string
	// S3AccessKey is the static access key. The S3 adapter signs with static credentials only (no AWS
	// default credential chain), so it is required, together with S3SecretKey.
	S3AccessKey string
	// S3SecretKey is the static secret key; required.
	S3SecretKey string
	// S3Region is the signing region.
	S3Region string
	// S3PathStyle addresses objects path-style (MinIO).
	S3PathStyle bool
	// WorkerID is the prefix of this worker's id on claimed rows; empty = the hostname. Every process appends
	// its own uuid (effectiveWorkerID), so two workers given the same MEMD_WORKER_ID never share an id and
	// heartbeat fencing keeps working.
	WorkerID string
	// WorkerSlots is how many jobs the worker runs at once.
	WorkerSlots int
	// WorkerHealthAddr, when set, is where the worker serves grpc.health.v1 (worker and all modes).
	WorkerHealthAddr string
	// DBMaxConns caps the Postgres pool; 0 = sized from the worker slots.
	DBMaxConns int
	// LogLevel is debug, info, warn or error.
	LogLevel slog.Level
	// LogFormat is text or json; empty = json when DatadogEnabled, else text (see Config.logFormat).
	LogFormat string
	// DatadogEnabled turns on Datadog tracing, log correlation, runtime metrics and memd's own metrics
	// (internal/observability); the agent address and the rest come from the standard DD_* variables. Off by
	// default.
	DatadogEnabled bool
	// DatadogProfiler also starts the Datadog profiler; ignored unless DatadogEnabled.
	DatadogProfiler bool
	// DatadogMetrics sends memd's own metrics to DogStatsD (default true); ignored unless DatadogEnabled.
	DatadogMetrics bool
	// Ingest is the queue consumer's deployment: the provider, its connection and the tenant pinning.
	Ingest IngestEnv
}

// IngestEnv is the queue consumer's deployment settings (its behaviour is the server config file's
// ingest.consumer section).
type IngestEnv struct {
	// Provider is none (no consumer) or kafka.
	Provider string
	// Tenant pins the source to one tenant (empty = every envelope names its tenant).
	Tenant string
	// Org pins the source to one org (empty = every envelope names its org).
	Org string
	// HealthAddr, when set, is where memd ingest serves grpc.health.v1.
	HealthAddr string
	// Kafka is the kafka provider's connection.
	Kafka KafkaEnv
}

// KafkaEnv is the kafka provider's connection and credentials.
type KafkaEnv struct {
	// Brokers are the seed brokers, comma-separated host:port; required.
	Brokers string
	// Topic is the topic to consume; required.
	Topic string
	// Group is the consumer group every memd ingest process of the deployment shares.
	Group string
	// DLQTopic receives the dead letters; required, and it must exist.
	DLQTopic string
	// ClientID names the client to the brokers (empty = memd).
	ClientID string
	// TLS dials the brokers over TLS (implied by any of the TLS files).
	TLS bool
	// TLSCAFile is a PEM file of the CAs to trust instead of the system pool.
	TLSCAFile string
	// TLSCertFile is the PEM client certificate, for mutual TLS (with TLSKeyFile).
	TLSCertFile string
	// TLSKeyFile is the PEM key of the client certificate.
	TLSKeyFile string
	// SASLMechanism is PLAIN, SCRAM-SHA-256 or SCRAM-SHA-512; empty = no SASL.
	SASLMechanism string
	// SASLUsername is the SASL user.
	SASLUsername string
	// SASLPassword is the SASL password.
	SASLPassword string
}

// logFormat is the effective log format: LogFormat when set, else json with Datadog enabled (the agent then
// parses each line without a pipeline) and text otherwise.
func (c Config) logFormat() string {
	switch {
	case c.LogFormat != "":
		return c.LogFormat
	case c.DatadogEnabled:
		return logFormatJSON
	default:
		return logFormatText
	}
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
		envConfig, "server config file: models, behaviour defaults, operator settings (unset = ./memd.yaml if present)", "",
		str(func(c *Config) *string { return &c.ConfigPath }),
	},
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
		envBlobProvider, "blob storage for original texts (s3)", blobProviderS3,
		str(func(c *Config) *string { return &c.BlobProvider }),
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
		envWorkerHealthAddr, "worker grpc.health listen address (empty = none)", "",
		str(func(c *Config) *string { return &c.WorkerHealthAddr }),
	},
	{
		envDBMaxConns, "Postgres pool size (0 = from the worker slots)", "0",
		integer(func(c *Config) *int { return &c.DBMaxConns }),
	},
	{envLogLevel, "log level: debug, info, warn, error", "info", logLevel},
	{envLogFormat, "log format: text or json (empty = json with Datadog enabled, else text)", "", logFormat},
	{
		envDatadogEnabled, "Datadog tracing, metrics and log correlation (agent from DD_* variables)",
		"false", boolean(func(c *Config) *bool { return &c.DatadogEnabled }),
	},
	{
		envDatadogProfiler, "Datadog continuous profiler (only with Datadog enabled)", "false",
		boolean(func(c *Config) *bool { return &c.DatadogProfiler }),
	},
	{
		envDatadogMetrics, "memd's own metrics over DogStatsD (only with Datadog enabled)", "true",
		boolean(func(c *Config) *bool { return &c.DatadogMetrics }),
	},
	{
		envIngestProvider, "queue consumer source: none or kafka (ingest; all runs the consumer when set)",
		ingestProviderNone, str(func(c *Config) *string { return &c.Ingest.Provider }),
	},
	{
		envIngestTenant, "pin the queue to one tenant (empty = every envelope names its tenant)", "",
		str(func(c *Config) *string { return &c.Ingest.Tenant }),
	},
	{
		envIngestOrg, "pin the queue to one org (empty = every envelope names its org)", "",
		str(func(c *Config) *string { return &c.Ingest.Org }),
	},
	{
		envIngestHealthAddr, "queue consumer grpc.health listen address (empty = none)", "",
		str(func(c *Config) *string { return &c.Ingest.HealthAddr }),
	},
	{
		envKafkaBrokers, "Kafka seed brokers, comma-separated host:port", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.Brokers }),
	},
	{envKafkaTopic, "Kafka topic to consume", "", kafkaStr(func(k *KafkaEnv) *string { return &k.Topic })},
	{
		envKafkaGroup, "Kafka consumer group, shared by every memd ingest process", defaultKafkaGroup,
		kafkaStr(func(k *KafkaEnv) *string { return &k.Group }),
	},
	{
		envKafkaDLQTopic, "Kafka dead-letter topic (must exist)", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.DLQTopic }),
	},
	{
		envKafkaClientID, "Kafka client id (empty = memd)", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.ClientID }),
	},
	{
		envKafkaTLS, "dial the Kafka brokers over TLS (implied by a TLS file)", "false",
		boolean(func(c *Config) *bool { return &c.Ingest.Kafka.TLS }),
	},
	{
		envKafkaTLSCA, "Kafka TLS CA bundle, PEM (empty = the system pool)", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.TLSCAFile }),
	},
	{
		envKafkaTLSCert, "Kafka TLS client certificate, PEM", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.TLSCertFile }),
	},
	{
		envKafkaTLSKey, "Kafka TLS client key, PEM", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.TLSKeyFile }),
	},
	{
		envKafkaSASLMech, "Kafka SASL mechanism: PLAIN, SCRAM-SHA-256 or SCRAM-SHA-512 (empty = none)", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.SASLMechanism }),
	},
	{
		envKafkaSASLUser, "Kafka SASL user", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.SASLUsername }),
	},
	{
		envKafkaSASLPass, "Kafka SASL password", "",
		kafkaStr(func(k *KafkaEnv) *string { return &k.SASLPassword }),
	},
}

// kafkaStr sets a string field of the Kafka settings.
func kafkaStr(field func(*KafkaEnv) *string) func(*Config, string) error {
	return str(func(c *Config) *string { return field(&c.Ingest.Kafka) })
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

// logFormat parses the log format; empty leaves the choice to Config.logFormat.
func logFormat(c *Config, v string) error {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case "", logFormatText, logFormatJSON:
		c.LogFormat = v
		return nil
	default:
		return fmt.Errorf("not a log format (text, json): %q", v)
	}
}

// flagName is the flag of an environment variable: MEMD_GRPC_ADDR -> grpc-addr.
func flagName(env string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(env, "MEMD_")), "_", "-")
}

// errUsage reports a command line that names no valid mode; run prints the usage for it.
var errUsage = errors.New("usage")

// usage writes the command's usage.
func usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "usage: memd <%s|%s|%s|%s> [flags]\n\nSettings (flag, environment variable, default):\n",
		modeAPI, modeWorker, modeIngest, modeAll)
	for _, s := range settings {
		_, _ = fmt.Fprintf(w, "  -%s, %s (default %q)\n      %s\n", flagName(s.env), s.env, s.def, s.help)
	}
	_, _ = fmt.Fprintf(w, "\nModels and every behaviour setting live in the server config file (%s, default %s; "+
		"see memd.example.yaml).\n", envConfig, defaultConfigPath)
}

// LoadConfig reads the mode from args[0], then every setting from env and the flags after the mode (a flag
// wins), then the server config file they name. It does not validate; call Validate.
func LoadConfig(args []string, env func(string) string) (Config, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Config{}, errUsage
	}
	c := Config{Mode: args[0]}
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
	path, required := c.ConfigPath, true
	if path == "" {
		path, required = defaultConfigPath, false
	}
	f, err := loadServerFile(path, required)
	if err != nil {
		return Config{}, err
	}
	c.File = f
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

// Validate checks the configuration before anything connects: a known mode, the required endpoints, and the
// server config file (ServerFile.validate: the required model slots, the defaults block, positive operator
// settings). The 768-dimension check against the gateway needs the network, so buildDeps runs it
// (checkEmbedding) right after the gateway client exists.
func (c Config) Validate() error {
	var errs []error
	switch c.Mode {
	case modeAPI, modeWorker, modeIngest, modeAll:
	default:
		return fmt.Errorf("%w: unknown mode %q", errUsage, c.Mode)
	}
	if c.DSN == "" {
		errs = append(errs, fmt.Errorf("%s is required", envDSN))
	}
	if c.GatewayURL == "" && c.Mode != modeIngest {
		errs = append(errs, fmt.Errorf("%s is required", envGatewayURL))
	}
	errs = append(errs, c.validateBlob()...)
	errs = append(errs, c.validateIngest()...)
	if (c.Mode == modeAPI || c.Mode == modeAll) && c.GRPCAddr == "" {
		errs = append(errs, fmt.Errorf("%s is required in mode %s", envGRPCAddr, c.Mode))
	}
	if c.WorkerSlots <= 0 || c.DBMaxConns < 0 {
		errs = append(errs, fmt.Errorf("%s must be positive and %s must not be negative", envWorkerSlots,
			envDBMaxConns))
	}
	if _, err := c.File.validate(); err != nil {
		errs = append(errs, fmt.Errorf("config file: %w", err))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("memd: invalid config: %w", err)
	}
	return nil
}

// runsConsumer reports whether the mode runs the queue consumer: ingest always, all when a provider is set.
func (c Config) runsConsumer() bool {
	return c.Mode == modeIngest || (c.Mode == modeAll && c.Ingest.Provider != ingestProviderNone)
}

// validateIngest checks the provider and, when the mode runs the consumer, its settings: memd ingest needs a
// provider, and kafka needs brokers, a topic, a group and a dead-letter topic. The pinned tenant and org are
// checked with every envelope's ids.
func (c Config) validateIngest() []error {
	switch c.Ingest.Provider {
	case ingestProviderNone, ingestProviderKafka:
	default:
		return []error{fmt.Errorf("%s: unknown ingest provider %q (supported: %s, %s)", envIngestProvider,
			c.Ingest.Provider, ingestProviderNone, ingestProviderKafka)}
	}
	if c.Mode == modeIngest && c.Ingest.Provider == ingestProviderNone {
		return []error{fmt.Errorf("%s must name a provider in mode %s (supported: %s)", envIngestProvider, modeIngest,
			ingestProviderKafka)}
	}
	if !c.runsConsumer() {
		return nil
	}
	k := c.Ingest.Kafka
	var errs []error
	for _, s := range []struct{ env, v string }{
		{envKafkaBrokers, k.Brokers},
		{envKafkaTopic, k.Topic},
		{envKafkaGroup, k.Group},
		{envKafkaDLQTopic, k.DLQTopic},
	} {
		if strings.TrimSpace(s.v) == "" {
			errs = append(errs, fmt.Errorf("%s is required with %s=%s", s.env, envIngestProvider, ingestProviderKafka))
		}
	}
	if (k.TLSCertFile == "") != (k.TLSKeyFile == "") {
		errs = append(errs, fmt.Errorf("%s and %s go together", envKafkaTLSCert, envKafkaTLSKey))
	}
	return errs
}

// validateBlob checks the blob provider and its settings: s3 needs a bucket and static credentials.
func (c Config) validateBlob() []error {
	if c.BlobProvider != blobProviderS3 {
		return []error{fmt.Errorf("%s: unknown blob provider %q (supported: %s)", envBlobProvider, c.BlobProvider,
			blobProviderS3)}
	}
	var errs []error
	if c.S3Bucket == "" {
		errs = append(errs, fmt.Errorf("%s is required", envS3Bucket))
	}
	if c.S3AccessKey == "" || c.S3SecretKey == "" {
		errs = append(errs, fmt.Errorf("%s and %s are both required (static S3 credentials)", envS3AccessKey,
			envS3SecretKey))
	}
	return errs
}

// effectiveWorkerID is this process's worker id: "<configured or hostname>-<uuid>". The uuid is fresh per
// process, so two workers started with the same MEMD_WORKER_ID never share an id and the heartbeat fencing
// keeps them apart. Without a configured id or a hostname the prefix is "worker".
func effectiveWorkerID(configured string, hostname func() (string, error), newID func() string) string {
	base := strings.TrimSpace(configured)
	if base == "" {
		if h, err := hostname(); err == nil {
			base = strings.TrimSpace(h)
		}
	}
	if base == "" {
		base = "worker"
	}
	return base + "-" + newID()
}
