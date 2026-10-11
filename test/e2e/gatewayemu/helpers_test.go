package gatewayemu_test

import (
	"encoding/json"
	"memd/internal/adapter/gateway"
	"memd/internal/domain"
	"memd/internal/jsonschema"
	"memd/internal/port"
	"memd/internal/prompts"
	"memd/test/e2e/gatewayemu"
	"testing"
)

// newEmu starts an emulator and a gateway client pointed at it, both closed with the test.
func newEmu(t *testing.T) (*gatewayemu.Server, *gateway.Client) {
	t.Helper()
	emu, ts := gatewayemu.Start(gatewayemu.Options{})
	t.Cleanup(ts.Close)
	client := gateway.NewClient(gateway.Config{
		BaseURL: ts.URL + gatewayemu.BasePath, RetryBackoff: -1, MaxRetries: 2,
	}, ts.Client())
	return emu, client
}

// binding is the slot's emulator model binding, as the harness configures it.
func binding(slot domain.ModelSlot) domain.ModelBinding {
	return domain.ModelBinding{Model: gatewayemu.ModelFor(string(slot))}
}

// render renders a prompt template or fails the test.
func render(t *testing.T, name string, data any) string {
	t.Helper()
	out, err := prompts.Render(name, data)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

// mustValidate fails the test unless doc matches schema, checked with internal/jsonschema.
func mustValidate(t *testing.T, what string, schema, doc json.RawMessage) {
	t.Helper()
	sch, err := jsonschema.Compile(schema)
	if err != nil {
		t.Fatalf("%s: compile schema: %v", what, err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Fatalf("%s: %v\n%s", what, err, doc)
	}
}

// structured makes a Structured call through the gateway client and returns the raw output.
func structured(t *testing.T, c *gateway.Client, req port.StructuredRequest) json.RawMessage {
	t.Helper()
	var out json.RawMessage
	if _, err := c.Structured(t.Context(), req, &out); err != nil {
		t.Fatalf("structured %s: %v", req.Name, err)
	}
	return out
}

// reflectTools returns the reflect tool list from the prompts assets.
func reflectTools(t *testing.T) []port.ToolSpec {
	t.Helper()
	raw, err := prompts.JSON(prompts.ReflectTools)
	if err != nil {
		t.Fatal(err)
	}
	var tools []port.ToolSpec
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	return tools
}

// sampleFacts are three facts with ids, two dated.
func sampleFacts() []prompts.SourceFact {
	at := mustTime("2024-03-01T10:00:00Z")
	return []prompts.SourceFact{
		{ID: "0b6a1f3e-1111-4c1e-9a55-000000000001", Type: "world", Text: "Alice moved to Paris in 2023.", MentionedAt: &at},
		{
			ID: "0b6a1f3e-1111-4c1e-9a55-000000000002", Type: "experience", Text: "Bob plays chess (badly).",
			OccurredStart: &at, OccurredEnd: &at,
		},
		{ID: "0b6a1f3e-1111-4c1e-9a55-000000000003", Text: "The team ships on Fridays."},
	}
}
