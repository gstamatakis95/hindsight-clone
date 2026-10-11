package gatewayemu

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// maxGenDepth bounds recursion through nested or self-referencing schemas.
const maxGenDepth = 32

// generator builds a value matching a JSON Schema, over the keywords internal/jsonschema checks: type, enum,
// const, properties, required, additionalProperties, items, minItems, maxItems, minLength, maxLength,
// minimum, maximum, anyOf, oneOf, allOf and local $ref. Strings take text.
type generator struct {
	// root is the whole schema, the base of $ref pointers.
	root any
	// text is the value strings take (cut or padded to their length bounds).
	text string
}

// newGenerator parses schema for generation.
func newGenerator(schema json.RawMessage, text string) (*generator, error) {
	var root any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &generator{root: root, text: text}, nil
}

// value returns a value matching node.
func (g *generator) value(node any, depth int) any {
	n, ok := node.(map[string]any)
	if !ok || depth > maxGenDepth {
		return nil
	}
	if ref, ok := n["$ref"].(string); ok {
		return g.value(g.resolve(ref), depth+1)
	}
	if c, ok := n["const"]; ok {
		return c
	}
	if enum, ok := n["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	if all, ok := n["allOf"].([]any); ok && len(all) > 0 {
		return g.value(mergeAll(n, all), depth+1)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if subs, ok := n[key].([]any); ok && len(subs) > 0 {
			return g.value(subs[0], depth+1)
		}
	}
	return g.typed(n, pickType(n), depth)
}

// typed returns a value of type typ for node.
func (g *generator) typed(n map[string]any, typ string, depth int) any {
	switch typ {
	case "object":
		return g.object(n, depth)
	case "array":
		return g.array(n, depth)
	case "integer":
		return math.Round(bounded(n, 1))
	case "number":
		return bounded(n, 1)
	case "boolean":
		return true
	case "null":
		return nil
	default:
		return fitString(n, g.text)
	}
}

// object returns an object with every declared property and every required one.
func (g *generator) object(n map[string]any, depth int) map[string]any {
	props, _ := n["properties"].(map[string]any)
	out := map[string]any{}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out[name] = g.value(props[name], depth+1)
	}
	if req, ok := n["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, done := out[name]; !done {
					out[name] = g.value(n["additionalProperties"], depth+1)
				}
			}
		}
	}
	return out
}

// array returns max(minItems, 1) items, capped by maxItems.
func (g *generator) array(n map[string]any, depth int) []any {
	count := 1
	if lo, ok := n["minItems"].(float64); ok && int(lo) > count {
		count = int(lo)
	}
	if hi, ok := n["maxItems"].(float64); ok && int(hi) < count {
		count = int(hi)
	}
	items, ok := n["items"].(map[string]any)
	if !ok {
		return []any{}
	}
	out := make([]any, count)
	for i := range out {
		out[i] = g.value(items, depth+1)
	}
	return out
}

// resolve follows a local $ref; an unresolvable one yields nil.
func (g *generator) resolve(ref string) any {
	ptr, _ := strings.CutPrefix(ref, "#")
	cur := g.root
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[tok]
	}
	return cur
}

// mergeAll merges node and its allOf branches into one node (properties and required unioned, other
// keywords first-wins), dropping allOf.
func mergeAll(n map[string]any, all []any) map[string]any {
	out := map[string]any{}
	props := map[string]any{}
	var req []any
	for _, sub := range append([]any{n}, all...) {
		m, ok := sub.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range m {
			switch k {
			case "allOf":
			case "properties":
				if p, ok := v.(map[string]any); ok {
					for name, s := range p {
						props[name] = s
					}
				}
			case "required":
				if r, ok := v.([]any); ok {
					req = append(req, r...)
				}
			default:
				if _, set := out[k]; !set {
					out[k] = v
				}
			}
		}
	}
	if len(props) > 0 {
		out["properties"] = props
	}
	if len(req) > 0 {
		out["required"] = req
	}
	return out
}

// pickType returns the type to generate: the first non-null type listed, "null" when only null is allowed, or
// one inferred from the keywords present.
func pickType(n map[string]any) string {
	switch t := n["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
		if len(t) > 0 {
			return "null"
		}
	}
	switch {
	case n["properties"] != nil, n["required"] != nil:
		return "object"
	case n["items"] != nil:
		return "array"
	default:
		return "string"
	}
}

// bounded returns def clamped to the node's minimum and maximum.
func bounded(n map[string]any, def float64) float64 {
	if lo, ok := n["minimum"].(float64); ok && def < lo {
		def = lo
	}
	if hi, ok := n["maximum"].(float64); ok && def > hi {
		def = hi
	}
	return def
}

// fitString cuts or pads text to the node's minLength and maxLength (in characters).
func fitString(n map[string]any, text string) string {
	if hi, ok := n["maxLength"].(float64); ok && utf8.RuneCountInString(text) > int(hi) {
		text = string([]rune(text)[:int(hi)])
	}
	if lo, ok := n["minLength"].(float64); ok {
		for utf8.RuneCountInString(text) < int(lo) {
			text += "x"
		}
	}
	return text
}
