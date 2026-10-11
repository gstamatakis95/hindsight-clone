package observability

import (
	"io"
	"log/slog"
	"strings"

	slogtrace "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// Datadog's reserved log attributes, which the agent maps without a pipeline: the record's date, status
// (level), message and logger name, and the unified service tags that link a log line to its service.
const (
	// LogKeyTimestamp is the record's time (RFC 3339 with nanoseconds).
	LogKeyTimestamp = "timestamp"
	// LogKeyStatus is the record's level: debug, info, warn or error.
	LogKeyStatus = "status"
	// LogKeyMessage is the record's message.
	LogKeyMessage = "message"
	// LogKeyLogger is the component that logged the record, when it is not memd itself (the tracer's own
	// messages carry "datadog").
	LogKeyLogger = "logger.name"
	// LogKeyService is DD_SERVICE (default DefaultService), added to every record while tracing.
	LogKeyService = "dd.service"
	// LogKeyEnv is DD_ENV, added to every record while tracing when set.
	LogKeyEnv = "dd.env"
	// LogKeyVersion is DD_VERSION, added to every record while tracing when set.
	LogKeyVersion = "dd.version"
)

// componentKey is the attribute memd's components name themselves with; the JSON handler writes it as
// LogKeyLogger.
const componentKey = "component"

// NewJSONHandler returns a JSON slog handler writing one object per line to w with Datadog's reserved
// attribute names: timestamp, status (lower-case level), message and logger.name (for a top-level
// "component" attribute). Every other attribute keeps its key, so dd.trace_id and dd.span_id (LogHandler)
// stay at the top level, where the agent correlates them with traces. It needs no Datadog client and works
// with Datadog off.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	o := slog.HandlerOptions{}
	if opts != nil {
		o = *opts
	}
	next := o.ReplaceAttr
	o.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 {
			a = datadogKey(a)
		}
		if next != nil {
			return next(groups, a)
		}
		return a
	}
	return slog.NewJSONHandler(w, &o)
}

// datadogKey renames a top-level attribute to its Datadog reserved name.
func datadogKey(a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		a.Key = LogKeyTimestamp
	case slog.LevelKey:
		a = slog.String(LogKeyStatus, strings.ToLower(a.Value.String()))
	case slog.MessageKey:
		a.Key = LogKeyMessage
	case componentKey:
		a.Key = LogKeyLogger
	}
	return a
}

// LogHandler correlates logs with traces: while tracing, every record logged with a context that carries a
// span (slog's *Context methods) gets dd.trace_id and dd.span_id, and every record gets dd.service, dd.env
// and dd.version (the last two when set). Without tracing (disabled, or metrics only) it returns h unchanged.
func (t *Telemetry) LogHandler(h slog.Handler) slog.Handler {
	if !t.traces() {
		return h
	}
	return slogtrace.WrapHandler(h.WithAttrs(t.logAttrs))
}

// logAttrs returns the unified service tags for log records: dd.service (DD_SERVICE, else DefaultService),
// and dd.env and dd.version when DD_ENV and DD_VERSION are set.
func logAttrs(env func(string) string) []slog.Attr {
	service := env(envService)
	if service == "" {
		service = DefaultService
	}
	attrs := []slog.Attr{slog.String(LogKeyService, service)}
	if v := env(envEnv); v != "" {
		attrs = append(attrs, slog.String(LogKeyEnv, v))
	}
	if v := env(envVersion); v != "" {
		attrs = append(attrs, slog.String(LogKeyVersion, v))
	}
	return attrs
}

// ddLogger routes the tracer's and profiler's own messages into slog.
type ddLogger struct {
	// log receives the messages.
	log *slog.Logger
}

var _ tracer.Logger = ddLogger{}

// Log writes msg at warn when the tracer marks it as an error, else at info.
func (l ddLogger) Log(msg string) {
	msg = strings.TrimSpace(msg)
	if strings.Contains(msg, "ERROR:") || strings.Contains(msg, "WARN:") {
		l.log.Warn(msg, componentKey, "datadog")
		return
	}
	l.log.Info(msg, componentKey, "datadog")
}
