package reflect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"slices"
)

// ProjectRequest is one structured-output projection.
type ProjectRequest struct {
	// Bank owns the document (config resolution and usage accounting).
	Bank domain.BankRef
	// Schema is the JSON Schema to project onto.
	Schema json.RawMessage
	// Document is the finished answer or mental-model content.
	Document string
	// Overrides are request-level config overrides (the STRUCTURED_OUTPUT binding); nil = none.
	Overrides *domain.BankConfig
}

// Project extracts a JSON object matching req.Schema from a finished document: it resolves the bank's effective
// config (with the request overrides) for the STRUCTURED_OUTPUT binding and makes one Structured call.
func (r *Agent) Project(ctx context.Context, req ProjectRequest) (json.RawMessage, error) {
	var overrides []domain.BankConfig
	if req.Overrides != nil {
		overrides = append(overrides, *req.Overrides)
	}
	cfg, err := r.d.Config.Effective(ctx, req.Bank, overrides...)
	if err != nil {
		return nil, fmt.Errorf("reflect: project: %w", err)
	}
	out, _, err := project(ctx, r.d.LLM, r.d.Router, cfg, req.Schema, req.Document)
	return out, err
}

// project makes the STRUCTURED_OUTPUT call for document under cfg and returns the JSON with the call's usage
// (spent tokens are reported even when the call fails).
func project(
	ctx context.Context, llm ai.LLM, router *ai.Router, cfg domain.BankConfig,
	schema json.RawMessage, document string,
) (json.RawMessage, domain.Usage, error) {
	binding, err := router.Resolve(cfg, domain.ModelSlotStructuredOutput)
	if err != nil {
		return nil, domain.Usage{}, err
	}
	data, err := structuredData(schema, document)
	if err != nil {
		return nil, domain.Usage{}, err
	}
	system, err := prompts.Render(prompts.StructuredOutputSystem, &data)
	if err != nil {
		return nil, domain.Usage{}, fmt.Errorf("reflect: %w", err)
	}
	user, err := prompts.Render(prompts.StructuredOutputUser, &data)
	if err != nil {
		return nil, domain.Usage{}, fmt.Errorf("reflect: %w", err)
	}
	var out json.RawMessage
	usage, err := llm.Structured(ctx, ai.StructuredRequest{
		Slot: domain.ModelSlotStructuredOutput, Binding: binding, Name: prompts.StructuredOutputSystem,
		System: system, User: user, Schema: schema,
	}, &out)
	if err != nil {
		return nil, usage, err
	}
	return out, usage, nil
}

// structuredData fills the structured-output prompt data: the document, the schema pretty-printed and its
// top-level properties in schema order.
func structuredData(schema json.RawMessage, document string) (prompts.StructuredOutputData, error) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, schema, "", "  "); err != nil {
		return prompts.StructuredOutputData{}, fmt.Errorf("%w: response schema: %w", domain.ErrInvalidArgument, err)
	}
	fields, err := schemaFields(schema)
	if err != nil {
		return prompts.StructuredOutputData{}, err
	}
	return prompts.StructuredOutputData{Document: document, Fields: fields, Schema: pretty.String()}, nil
}

// schemaFields lists a schema's top-level properties in the order they appear.
func schemaFields(schema json.RawMessage) ([]prompts.SchemaField, error) {
	var top struct {
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if err := json.Unmarshal(schema, &top); err != nil {
		return nil, fmt.Errorf("%w: response schema: %w", domain.ErrInvalidArgument, err)
	}
	if len(top.Properties) == 0 {
		return nil, nil
	}
	names, err := objectKeys(top.Properties)
	if err != nil {
		return nil, err
	}
	var props map[string]struct {
		Type        any    `json:"type"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(top.Properties, &props); err != nil {
		return nil, fmt.Errorf("%w: response schema properties: %w", domain.ErrInvalidArgument, err)
	}
	fields := make([]prompts.SchemaField, 0, len(names))
	for _, n := range names {
		typ, _ := props[n].Type.(string)
		fields = append(fields, prompts.SchemaField{
			Name: n, Type: typ, Required: slices.Contains(top.Required, n), Description: props[n].Description,
		})
	}
	return fields, nil
}

// objectKeys returns the keys of a JSON object in document order.
func objectKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%w: response schema properties is not an object", domain.ErrInvalidArgument)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: response schema: %w", domain.ErrInvalidArgument, err)
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, fmt.Errorf("%w: response schema: %w", domain.ErrInvalidArgument, err)
		}
	}
	return keys, nil
}
