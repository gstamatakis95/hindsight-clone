package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// jsonLines decodes buf's JSON log records, one per line.
func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("not a JSON record: %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// present stands for any value in wantFields.
const present = "<present>"

// wantFields checks rec's fields against want: a value must match, present only needs the field, nil needs it
// absent.
func wantFields(t *testing.T, rec map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		got, ok := rec[k]
		if (v == nil && ok) || (v == present && !ok) || (v != nil && v != present && got != v) {
			t.Fatalf("record %v: field %q = %v, want %v", rec, k, got, v)
		}
	}
}

// TestJSONLogsUseDatadogAttributes checks the JSON format's reserved attribute names and, while tracing,
// the correlation attributes: trace and span ids on a record logged with a span, service, env and version on
// every record. Without tracing no dd.* attribute appears.
func TestJSONLogsUseDatadogAttributes(t *testing.T) {
	t.Setenv("DD_SERVICE", "")
	t.Setenv("DD_ENV", "staging")
	t.Setenv("DD_VERSION", "1.2.3")
	mt := mocktracer.Start()
	defer mt.Stop()
	var buf bytes.Buffer
	log := slog.New(New(nil).LogHandler(NewJSONHandler(&buf, nil)))
	span, ctx := tracer.StartSpanFromContext(context.Background(), "op")
	log.WarnContext(ctx, "with span", "op", "o1")
	span.Finish()
	ddLogger{log: log}.Log("datadog: tracer says hi")
	recs := jsonLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	wantFields(t, recs[0], map[string]any{
		LogKeyMessage: "with span", LogKeyStatus: "warn", LogKeyTimestamp: present, "op": "o1",
		ext.LogKeyTraceID: present, ext.LogKeySpanID: present, LogKeyService: DefaultService, LogKeyEnv: "staging",
		LogKeyVersion: "1.2.3", "msg": nil, "level": nil,
	})
	wantFields(t, recs[1], map[string]any{
		LogKeyLogger: "datadog", LogKeyStatus: "info", ext.LogKeyTraceID: nil, LogKeyService: DefaultService,
	})

	buf.Reset()
	var off *Telemetry
	slog.New(off.LogHandler(NewJSONHandler(&buf, nil))).InfoContext(ctx, "untraced")
	for k := range jsonLines(t, &buf)[0] {
		if strings.HasPrefix(k, "dd.") {
			t.Fatalf("untraced record has %s: %s", k, buf.String())
		}
	}
}
