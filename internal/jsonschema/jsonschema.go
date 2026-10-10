package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"memd/internal/domain"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Schema is a parsed JSON Schema. It checks the subset of keywords structured output uses: type (a name or a
// list), enum, const, properties, required, additionalProperties, items, minItems, maxItems, minLength,
// maxLength, minimum, maximum, anyOf, oneOf, allOf and local $ref ("#/$defs/...", "#/definitions/...").
// Other keywords are ignored.
type Schema struct {
	// root is the whole schema document, the base of $ref pointers.
	root any
}

// Compile parses raw; an empty or malformed schema is domain.ErrInvalidArgument.
func Compile(raw json.RawMessage) (*Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: structured call has no JSON schema", domain.ErrInvalidArgument)
	}
	root, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: JSON schema: %w", domain.ErrInvalidArgument, err)
	}
	return &Schema{root: root}, nil
}

// decodeJSON decodes exactly one JSON value, keeping numbers as json.Number.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid JSON: trailing data after the value")
	}
	return v, nil
}

// Validate checks that doc is one JSON value matching the schema. The error names the first mismatch by
// its JSON path, e.g. "$.facts[2].text: missing required property".
func (s *Schema) Validate(doc []byte) error {
	v, err := decodeJSON(doc)
	if err != nil {
		return fmt.Errorf("response is not valid JSON: %w", err)
	}
	if err := s.check(s.root, v, "$", 0); err != nil {
		return fmt.Errorf("response does not match the JSON schema: %w", err)
	}
	return nil
}

// maxRefDepth bounds $ref resolution so a self-referencing schema can't loop forever.
const maxRefDepth = 64

// keywordCheck checks one group of keywords of node against v at path.
type keywordCheck func(s *Schema, node map[string]any, v any, path string, depth int) error

// keywordChecks lists the checks run in order for every schema node. It is a function rather than a variable
// because the checks recurse into check, which would make a package-level variable an initialization cycle.
func keywordChecks() []keywordCheck {
	return []keywordCheck{
		checkRef, checkType, checkEnum, checkConst, checkObject, checkArray, checkString, checkNumber, checkCombinators,
	}
}

// check validates v against the schema node at path. Boolean schemas accept (true) or reject (false) all.
func (s *Schema) check(node, v any, path string, depth int) error {
	if depth > maxRefDepth {
		return fmt.Errorf("%s: schema nests too deeply", path)
	}
	switch n := node.(type) {
	case bool:
		if !n {
			return fmt.Errorf("%s: no value is allowed here", path)
		}
		return nil
	case map[string]any:
		for _, kc := range keywordChecks() {
			if err := kc(s, n, v, path, depth); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// checkRef validates v against the schema a local $ref points to.
func checkRef(s *Schema, node map[string]any, v any, path string, depth int) error {
	ref, ok := node["$ref"].(string)
	if !ok {
		return nil
	}
	target, err := s.resolve(ref)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return s.check(target, v, path, depth+1)
}

// resolve follows a local JSON pointer ("#", "#/$defs/x", ...) from the schema root.
func (s *Schema) resolve(ref string) (any, error) {
	ptr, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil, fmt.Errorf("unsupported $ref %q (only local refs)", ref)
	}
	cur := s.root
	for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		if tok == "" {
			continue
		}
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unresolvable $ref %q", ref)
		}
		if cur, ok = m[tok]; !ok {
			return nil, fmt.Errorf("unresolvable $ref %q", ref)
		}
	}
	return cur, nil
}

// checkType validates the type keyword, a type name or a list of them.
func checkType(_ *Schema, node map[string]any, v any, path string, _ int) error {
	var names []string
	switch t := node["type"].(type) {
	case string:
		names = []string{t}
	case []any:
		for _, x := range t {
			if name, ok := x.(string); ok {
				names = append(names, name)
			}
		}
	default:
		return nil
	}
	for _, name := range names {
		if hasType(name, v) {
			return nil
		}
	}
	return fmt.Errorf("%s: want type %s, got %s", path, strings.Join(names, " or "), typeName(v))
}

// hasType reports whether v is an instance of the JSON Schema type name.
func hasType(name string, v any) bool {
	switch name {
	case "integer":
		f, ok := number(v)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "number":
		_, ok := number(v)
		return ok
	default:
		return typeName(v) == name
	}
}

// typeName is v's JSON type: null, boolean, number, string, array or object.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number, float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// number returns v as a float64 when it is a JSON number.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(n.String(), 64)
		return f, err == nil
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// checkEnum validates the enum keyword.
func checkEnum(_ *Schema, node map[string]any, v any, path string, _ int) error {
	values, ok := node["enum"].([]any)
	if !ok {
		return nil
	}
	for _, want := range values {
		if jsonEqual(want, v) {
			return nil
		}
	}
	return fmt.Errorf("%s: value is not one of the allowed enum values", path)
}

// checkConst validates the const keyword.
func checkConst(_ *Schema, node map[string]any, v any, path string, _ int) error {
	want, ok := node["const"]
	if !ok || jsonEqual(want, v) {
		return nil
	}
	return fmt.Errorf("%s: value does not equal the schema's const", path)
}

// jsonEqual compares two decoded JSON values; numbers compare by value.
func jsonEqual(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !jsonEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			if yv, ok := y[k]; !ok || !jsonEqual(xv, yv) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// checkObject validates required, properties and additionalProperties on an object value.
func checkObject(s *Schema, node map[string]any, v any, path string, depth int) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if req, ok := node["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				if _, present := obj[name]; !present {
					return fmt.Errorf("%s: missing required property %q", path, name)
				}
			}
		}
	}
	props, _ := node["properties"].(map[string]any)
	additional, hasAdditional := node["additionalProperties"]
	for name, val := range obj {
		sub, known := props[name]
		switch {
		case known:
		case hasAdditional:
			sub = additional
		default:
			continue
		}
		if err := s.check(sub, val, path+"."+name, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// checkArray validates items, minItems and maxItems on an array value.
func checkArray(s *Schema, node map[string]any, v any, path string, depth int) error {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	if n, ok := number(node["minItems"]); ok && float64(len(arr)) < n {
		return fmt.Errorf("%s: want at least %v items, got %d", path, n, len(arr))
	}
	if n, ok := number(node["maxItems"]); ok && float64(len(arr)) > n {
		return fmt.Errorf("%s: want at most %v items, got %d", path, n, len(arr))
	}
	items, ok := node["items"]
	if !ok {
		return nil
	}
	if _, tuple := items.([]any); tuple {
		return nil
	}
	for i, item := range arr {
		if err := s.check(items, item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// checkString validates minLength and maxLength (in characters) on a string value.
func checkString(_ *Schema, node map[string]any, v any, path string, _ int) error {
	str, ok := v.(string)
	if !ok {
		return nil
	}
	n := float64(utf8.RuneCountInString(str))
	if lo, ok := number(node["minLength"]); ok && n < lo {
		return fmt.Errorf("%s: string shorter than %v characters", path, lo)
	}
	if hi, ok := number(node["maxLength"]); ok && n > hi {
		return fmt.Errorf("%s: string longer than %v characters", path, hi)
	}
	return nil
}

// checkNumber validates minimum and maximum on a number value.
func checkNumber(_ *Schema, node map[string]any, v any, path string, _ int) error {
	f, ok := number(v)
	if !ok {
		return nil
	}
	if lo, ok := number(node["minimum"]); ok && f < lo {
		return fmt.Errorf("%s: %v is below the minimum %v", path, f, lo)
	}
	if hi, ok := number(node["maximum"]); ok && f > hi {
		return fmt.Errorf("%s: %v is above the maximum %v", path, f, hi)
	}
	return nil
}

// checkCombinators validates allOf (every branch), anyOf (at least one) and oneOf (exactly one).
func checkCombinators(s *Schema, node map[string]any, v any, path string, depth int) error {
	if all, ok := node["allOf"].([]any); ok {
		for _, sub := range all {
			if err := s.check(sub, v, path, depth+1); err != nil {
				return err
			}
		}
	}
	if anyOf, ok := node["anyOf"].([]any); ok && s.matches(anyOf, v, path, depth) == 0 {
		return fmt.Errorf("%s: value matches none of the anyOf schemas", path)
	}
	if oneOf, ok := node["oneOf"].([]any); ok {
		if n := s.matches(oneOf, v, path, depth); n != 1 {
			return fmt.Errorf("%s: value matches %d of the oneOf schemas, want exactly 1", path, n)
		}
	}
	return nil
}

// matches counts the schemas in subs that v matches.
func (s *Schema) matches(subs []any, v any, path string, depth int) int {
	n := 0
	for _, sub := range subs {
		if s.check(sub, v, path, depth+1) == nil {
			n++
		}
	}
	return n
}
