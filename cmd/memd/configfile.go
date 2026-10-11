package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/grpcapi"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	pb "memd/gen/memory/v1"
)

// The server config file (MEMD_CONFIG, default ./memd.yaml): every behaviour and operator setting, where the
// environment holds only deployment, infrastructure and secrets. Its defaults block is the server level of the
// config hierarchy (server < tenant < bank < request), written as the API's BankConfig message (proto field
// names, enum value names); the other sections are operator settings that are not per bank. memd.example.yaml
// documents every key.

// defaultConfigPath is the server config file read when MEMD_CONFIG is unset; it may be absent.
const defaultConfigPath = "./memd.yaml"

// Operator defaults of the file's sections.
const (
	defaultGatewayConcurrent = 32
	defaultGatewayTimeout    = 120 * time.Second
	defaultPollInterval      = 500 * time.Millisecond
	defaultHeartbeat         = 15 * time.Second
	defaultStaleAfter        = 2 * time.Minute
	defaultMaintenance       = 60 * time.Second
	defaultRPCDeadline       = 120 * time.Second
	defaultOperationKeep     = 7 * 24 * time.Hour
	defaultEventKeep         = 30 * 24 * time.Hour
	defaultMetricsInterval   = 15 * time.Second
)

// ServerFile is the server config file.
type ServerFile struct {
	// Defaults is the server level of the config hierarchy, merged over the built-in defaults by
	// config.ServerDefaults. Its models block binds the models: models.default for every chat step,
	// models.<slot> for one step, models.rerank for the cross-encoder and models.embed for embeddings.
	Defaults domain.BankConfig `json:"-"`
	// Gateway caps and times out calls to the AI gateway.
	Gateway GatewayFile `json:"gateway"`
	// Worker sets the job runner's and maintenance tick's timings.
	Worker WorkerFile `json:"worker"`
	// Retention sets how long finished operations and lifecycle events are kept.
	Retention RetentionFile `json:"retention"`
	// API sets the gRPC server's behaviour.
	API APIFile `json:"api"`
	// Observability tunes the optional Datadog metrics (MEMD_DATADOG_ENABLED switches them on).
	Observability ObservabilityFile `json:"observability"`
}

// ObservabilityFile is the observability section of the server config file.
type ObservabilityFile struct {
	// MetricsInterval is how often the Postgres pool and job queue gauges are sampled (default 15s).
	MetricsInterval Duration `json:"metrics_interval"`
}

// GatewayFile is the gateway section of the server config file.
type GatewayFile struct {
	// MaxConcurrent caps in-flight gateway calls in this process (default 32).
	MaxConcurrent int `json:"max_concurrent"`
	// SlotConcurrency caps in-flight gateway calls per slot (extract, ..., rerank, embed); slots without an
	// entry are limited only by MaxConcurrent.
	SlotConcurrency map[domain.ModelSlot]int `json:"slot_concurrency"`
	// Timeout bounds one gateway HTTP attempt when the binding sets none (default 120s).
	Timeout Duration `json:"timeout"`
}

// WorkerFile is the worker section of the server config file.
type WorkerFile struct {
	// PollInterval is the claim period (default 500ms).
	PollInterval Duration `json:"poll_interval"`
	// HeartbeatInterval is the heartbeat period (default 15s); it must be below 3/4 of StaleAfter.
	HeartbeatInterval Duration `json:"heartbeat_interval"`
	// StaleAfter is the heartbeat age after which a job is reclaimed (default 2m).
	StaleAfter Duration `json:"stale_after"`
	// MaintenanceInterval is the maintenance tick period (default 60s).
	MaintenanceInterval Duration `json:"maintenance_interval"`
}

// RetentionFile is the retention section of the server config file.
type RetentionFile struct {
	// Operations keeps finished (completed, failed, cancelled) operations this long (default 168h).
	Operations Duration `json:"operations"`
	// Events keeps lifecycle events this long (default 720h).
	Events Duration `json:"events"`
}

// APIFile is the api section of the server config file.
type APIFile struct {
	// RPCDeadline applies to calls that carry no deadline (default 120s; "0s" = none).
	RPCDeadline *Duration `json:"rpc_deadline"`
}

// Duration is a time.Duration written as a Go duration string ("90s", "2m", "168h").
type Duration time.Duration

// UnmarshalJSON parses a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("want a duration string such as \"90s\", got %s", b)
	}
	v, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("not a duration: %q", s)
	}
	*d = Duration(v)
	return nil
}

// loadServerFile reads and decodes the server config file at path. A missing file is the empty file when
// required is false (the default path); otherwise it is an error, as is an unknown key or a mistyped value.
// It applies the section defaults but does not validate (see ServerFile.validate).
func loadServerFile(path string, required bool) (ServerFile, error) {
	var f ServerFile
	raw, err := os.ReadFile(path) //nolint:gosec // the operator chooses the config file
	switch {
	case errors.Is(err, fs.ErrNotExist) && !required:
		raw = nil
	case err != nil:
		return ServerFile{}, fmt.Errorf("memd: config file: %w", err)
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := decodeYAML(raw, &f); err != nil {
			return ServerFile{}, fmt.Errorf("memd: config file %s: %w", path, err)
		}
	}
	f.withDefaults()
	return f, nil
}

// decodeYAML decodes the file into f, rejecting unknown keys: the operator sections through their JSON tags, the
// defaults block as a BankConfig message (protojson), converted to the domain config.
func decodeYAML(raw []byte, f *ServerFile) error {
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("not valid YAML: %w", err)
	}
	if doc == nil {
		return nil
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("not a mapping of settings: %w", err)
	}
	var file struct {
		*ServerFile
		Defaults json.RawMessage `json:"defaults"`
	}
	file.ServerFile = f
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return err //nolint:wrapcheck // the caller adds the file name
	}
	if len(file.Defaults) == 0 || string(file.Defaults) == "null" {
		return nil
	}
	var msg pb.BankConfig
	if err := protojson.Unmarshal(file.Defaults, &msg); err != nil {
		return fmt.Errorf("defaults: %w", err)
	}
	if f.Defaults, err = grpcapi.BankConfigFromProto(&msg); err != nil {
		return fmt.Errorf("defaults: %w", err)
	}
	return nil
}

// withDefaults fills unset section values.
func (f *ServerFile) withDefaults() {
	fill := func(d *Duration, def time.Duration) {
		if *d == 0 {
			*d = Duration(def)
		}
	}
	if f.Gateway.MaxConcurrent == 0 {
		f.Gateway.MaxConcurrent = defaultGatewayConcurrent
	}
	fill(&f.Gateway.Timeout, defaultGatewayTimeout)
	fill(&f.Worker.PollInterval, defaultPollInterval)
	fill(&f.Worker.HeartbeatInterval, defaultHeartbeat)
	fill(&f.Worker.StaleAfter, defaultStaleAfter)
	fill(&f.Worker.MaintenanceInterval, defaultMaintenance)
	fill(&f.Retention.Operations, defaultOperationKeep)
	fill(&f.Retention.Events, defaultEventKeep)
	fill(&f.Observability.MetricsInterval, defaultMetricsInterval)
	if f.API.RPCDeadline == nil {
		d := Duration(defaultRPCDeadline)
		f.API.RPCDeadline = &d
	}
}

// requiredSlots must have a model at the server level: retain (EXTRACT) and reflect (REFLECT) can't run
// without one, so memd refuses to start rather than fail the first call.
var requiredSlots = []domain.ModelSlot{domain.ModelSlotExtract, domain.ModelSlotReflect}

// gatewaySlots are the slots gateway.slot_concurrency can cap.
var gatewaySlots = append(append([]domain.ModelSlot{}, domain.LLMSlots...), domain.ModelSlotRerank,
	domain.ModelSlotEmbed)

// validate checks the file and returns the server level of the config hierarchy (config.ServerDefaults over
// its defaults block): the required slots bound, positive caps and timings, and a heartbeat well inside the
// reclaim threshold (the runner fences itself at 3/4 of stale_after).
func (f ServerFile) validate() (domain.BankConfig, error) {
	var errs []error
	server, err := config.ServerDefaults(f.Defaults)
	if err != nil {
		errs = append(errs, fmt.Errorf("defaults: %w", err))
	}
	for _, slot := range requiredSlots {
		if _, ok := server.Binding(slot); err == nil && !ok {
			errs = append(errs, fmt.Errorf("defaults.models: no model for %s: set defaults.models.default "+
				"or defaults.models.%s", slot, slot))
		}
	}
	if err := errors.Join(append(errs, f.validateOperator()...)...); err != nil {
		return domain.BankConfig{}, err
	}
	return server, nil
}

// validateOperator checks the operator sections: positive caps, timings and retentions, and a heartbeat well inside
// the reclaim threshold (the runner fences itself at 3/4 of stale_after).
func (f ServerFile) validateOperator() []error {
	var errs []error
	for slot, n := range f.Gateway.SlotConcurrency {
		if !knownSlot(slot) || n <= 0 {
			errs = append(errs, fmt.Errorf("gateway.slot_concurrency.%s: want a positive cap for a known slot", slot))
		}
	}
	w := f.Worker
	for _, d := range []Duration{
		f.Gateway.Timeout, w.PollInterval, w.HeartbeatInterval, w.StaleAfter, w.MaintenanceInterval,
		f.Retention.Operations, f.Retention.Events, f.Observability.MetricsInterval,
	} {
		if d <= 0 {
			errs = append(errs, errors.New("gateway.timeout, the worker timings, the retentions and "+
				"observability.metrics_interval must be positive"))
			break
		}
	}
	if f.Gateway.MaxConcurrent <= 0 || *f.API.RPCDeadline < 0 {
		errs = append(errs, errors.New("gateway.max_concurrent must be positive and api.rpc_deadline not negative"))
	}
	if w.HeartbeatInterval > 0 && w.StaleAfter > 0 && w.HeartbeatInterval*4 >= w.StaleAfter*3 {
		errs = append(errs, fmt.Errorf("worker.heartbeat_interval (%s) must be below 3/4 of worker.stale_after (%s)",
			time.Duration(w.HeartbeatInterval), time.Duration(w.StaleAfter)))
	}
	return errs
}

// knownSlot reports whether slot is a gateway slot.
func knownSlot(slot domain.ModelSlot) bool {
	for _, s := range gatewaySlots {
		if s == slot {
			return true
		}
	}
	return false
}
