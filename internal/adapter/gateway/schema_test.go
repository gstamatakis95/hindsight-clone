package gateway

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"strings"
	"testing"
)

func TestValidateSchema(t *testing.T) {
	const facts = `{
		"type": "object",
		"required": ["facts"],
		"additionalProperties": false,
		"properties": {
			"facts": {"type": "array", "maxItems": 3, "items": {"$ref": "#/$defs/fact"}},
			"note": {"type": ["string", "null"]}
		},
		"$defs": {
			"fact": {
				"type": "object",
				"required": ["text", "kind"],
				"properties": {
					"text": {"type": "string", "minLength": 1},
					"kind": {"enum": ["world", "experience"]},
					"score": {"type": "number", "minimum": 0, "maximum": 1},
					"count": {"type": "integer"},
					"when": {"anyOf": [{"type": "string"}, {"type": "null"}]},
					"tag": {"oneOf": [{"type": "string", "maxLength": 3}, {"type": "string", "minLength": 2}]},
					"v": {"const": 2}
				}
			}
		}
	}`
	for _, tc := range []struct {
		name, doc, wantErr string
	}{
		{"valid", `{"facts":[{"text":"a","kind":"world","score":0.5,"count":3,"when":null,"v":2.0}],"note":null}`, ""},
		{"integer as float", `{"facts":[{"text":"a","kind":"world","count":3.0}]}`, ""},
		{"not json", `{"facts": [`, "not valid JSON"},
		{"trailing data", `{"facts":[]} {}`, "trailing data"},
		{"missing required", `{}`, `$: missing required property "facts"`},
		{"nested required via ref", `{"facts":[{"text":"a"}]}`, `$.facts[0]: missing required property "kind"`},
		{"wrong type", `{"facts":[{"text":5,"kind":"world"}]}`, "$.facts[0].text: want type string, got number"},
		{"type list", `{"facts":[],"note":3}`, "$.note: want type string or null"},
		{"additional property", `{"facts":[],"extra":1}`, "$.extra: no value is allowed"},
		{"enum", `{"facts":[{"text":"a","kind":"opinion"}]}`, "$.facts[0].kind: value is not one of"},
		{"const", `{"facts":[{"text":"a","kind":"world","v":3}]}`, "$.facts[0].v: value does not equal"},
		{"min length", `{"facts":[{"text":"","kind":"world"}]}`, "$.facts[0].text: string shorter"},
		{"max items", `{"facts":[{"text":"a","kind":"world"},{"text":"a","kind":"world"},` +
			`{"text":"a","kind":"world"},{"text":"a","kind":"world"}]}`, "$.facts: want at most 3 items"},
		{"maximum", `{"facts":[{"text":"a","kind":"world","score":1.5}]}`, "above the maximum"},
		{"integer", `{"facts":[{"text":"a","kind":"world","count":1.5}]}`, "want type integer"},
		{"anyOf", `{"facts":[{"text":"a","kind":"world","when":1}]}`, "matches none of the anyOf"},
		{"oneOf both", `{"facts":[{"text":"a","kind":"world","tag":"ab"}]}`, "matches 2 of the oneOf"},
		{"oneOf one", `{"facts":[{"text":"a","kind":"world","tag":"abcd"}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := compileSchema(json.RawMessage(facts))
			if err != nil {
				t.Fatalf("compileSchema: %v", err)
			}
			err = s.validate([]byte(tc.doc))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("validate: %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("validate: %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompileSchemaRejectsBadSchema(t *testing.T) {
	for _, raw := range []string{"", "  ", "{", `{"type":"object"} x`} {
		if _, err := compileSchema(json.RawMessage(raw)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("compileSchema(%q) = %v, want ErrInvalidArgument", raw, err)
		}
	}
}

func TestSchemaRefLoopBounded(t *testing.T) {
	s, err := compileSchema(json.RawMessage(`{"$ref":"#"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validate([]byte(`{}`)); err == nil || !strings.Contains(err.Error(), "nests too deeply") {
		t.Errorf("validate = %v, want a depth error", err)
	}
}
