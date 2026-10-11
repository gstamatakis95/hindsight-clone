package gatewayemu

import (
	"encoding/json"
	"memd/internal/jsonschema"
	"memd/internal/prompts"
	"strings"
)

// Schema names of the structured prompts (the json_schema name the gateway client sends is the template name).
const (
	schemaTimeParse = "time_parse_system"
	extractPrefix   = "extract_"
	extractVerbatim = prompts.ExtractVerbatim
)

// structured answers a response_format json_schema call: a known prompt gets its scripted semantics, any
// other schema a generic instance. The output is validated against the request's schema; a mismatch is an
// emulator bug and fails the call.
func structured(req *chatRequest) (string, error) {
	name, schema := req.schemaName(), req.ResponseFormat.JSONSchema.Schema
	sch, err := jsonschema.Compile(schema)
	if err != nil {
		return "", emuErrorf("%s: %v", name, err)
	}
	user := req.firstUser()
	var v any
	switch {
	case strings.HasPrefix(name, extractPrefix):
		v, err = extraction(schema, chunkText(user), name == extractVerbatim)
	case name == prompts.ConsolidateSystem:
		v = consolidation(newFacts(user))
	case name == prompts.DedupSystem:
		v = map[string]any{"action": "keep", "text": "", "reason": "emulator: distinct observations are kept"}
	case name == schemaTimeParse:
		v = map[string]any{"start": nil, "end": nil}
	case name == prompts.MentalModelStructuredDeltaSystem:
		v = structuredDelta(sourceLines(user))
	case name == prompts.StructuredOutputSystem:
		v, err = generic(schema, answerDocument(user))
	default:
		v, err = generic(schema, user)
	}
	if err != nil {
		return "", emuErrorf("%s: %v", name, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", emuErrorf("%s: encode: %v", name, err)
	}
	if err := sch.Validate(out); err != nil {
		return "", emuErrorf("%s: emulator output does not match the request schema: %v", name, err)
	}
	return string(out), nil
}

// generic returns an instance of schema whose strings are text.
func generic(schema json.RawMessage, text string) (any, error) {
	g, err := newGenerator(schema, text)
	if err != nil {
		return nil, err
	}
	return g.value(g.root, 0), nil
}

// extraction returns one fact per sentence of chunk (one metadata entry for verbatim), each built from the
// schema's fact item so that mode-specific fields are present, then filled with fixed values.
func extraction(schema json.RawMessage, chunk string, verbatim bool) (any, error) {
	g, err := newGenerator(schema, "N/A")
	if err != nil {
		return nil, err
	}
	item := dig(g.root, "properties", "facts", "items")
	sentences := Sentences(chunk)
	if verbatim {
		sentences = []string{chunk}
	}
	facts := make([]any, 0, len(sentences))
	for _, sentence := range sentences {
		fact, ok := g.value(item, 0).(map[string]any)
		if !ok {
			return nil, emuErrorf("facts.items is not an object schema")
		}
		fillFact(fact, sentence)
		facts = append(facts, fact)
	}
	return map[string]any{"facts": facts}, nil
}

// fillFact sets the fields a fact item has: the sentence as what, N/A dimensions, a world conversation fact
// without dates, entities from capitalised words, no labels and no causes.
func fillFact(fact map[string]any, sentence string) {
	entities := []any{}
	for _, name := range entityNames(sentence) {
		entities = append(entities, map[string]any{"name": name, "kind": nil})
	}
	values := map[string]any{
		"what": sentence, "when": "N/A", "where": "N/A", "who": "N/A", "why": "N/A",
		"fact_kind": "conversation", "fact_type": "world", "occurred_start": nil, "occurred_end": nil,
		"time_expression": nil, "entities": entities, "labels": []any{}, "caused_by": []any{},
	}
	for k, v := range values {
		if _, ok := fact[k]; ok {
			fact[k] = v
		}
	}
}

// consolidation creates one observation per new fact, its text the fact's exact text, citing the fact's id.
func consolidation(facts []Item) map[string]any {
	creates := make([]any, 0, len(facts))
	for _, f := range facts {
		creates = append(creates, map[string]any{
			"text": f.Text, "source_fact_ids": []any{f.ID}, "reason": "emulator: one observation per fact",
		})
	}
	return map[string]any{"creates": creates, "updates": []any{}}
}

// structuredDelta adds one section quoting every new supporting fact (add_section needs no ids from the
// document, so it always applies); no facts, no ops.
func structuredDelta(items []Item) map[string]any {
	ops := []any{}
	if len(items) > 0 {
		blocks := make([]any, len(items))
		for i, it := range items {
			blocks[i] = it.Text
		}
		ops = append(ops, map[string]any{
			"kind": "add_section", "section_id": "", "block_id": "", "after_block_id": "",
			"heading": "New facts", "blocks_md": blocks,
		})
	}
	return map[string]any{"operations": ops}
}

// dig walks nested objects by key; a missing key yields nil.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
