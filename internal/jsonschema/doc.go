// Package jsonschema validates JSON documents against the subset of JSON Schema that structured LLM output
// uses.
//
// # Where it sits
//
// jsonschema is a leaf utility package: it imports only the standard library and domain (for
// domain.ErrInvalidArgument), so adapters, fakes and services may all use it. Three places do:
//
//   - the gateway adapter (internal/ai/gateway) compiles the schema of every ai.LLM.Structured call
//     and validates the model's reply before decoding it; the mismatch text is what its retry sends back to
//     the model;
//   - the LLM fake (internal/fake) validates scripted replies the same way, so a mismatch between an
//     engine's schema and its Go types fails in unit tests as it would in production;
//   - the reflect engine compiles ReflectRequest.response_schema up front, so a malformed schema is
//     INVALID_ARGUMENT before any model call.
//
// # Usage
//
// [Compile] parses a schema once; [Schema.Validate] checks a document against it:
//
//	sch, err := jsonschema.Compile(req.Schema) // empty or malformed: domain.ErrInvalidArgument
//	if err != nil {
//		return err
//	}
//	if err := sch.Validate([]byte(reply)); err != nil {
//		// e.g. "response does not match the JSON schema: $.facts[2]: missing required property \"text\""
//	}
//
// A [Schema] is immutable after Compile and safe for concurrent use.
//
// # Supported keywords
//
//   - type, as a name or a list such as ["string","null"]; "integer" accepts any whole number, including 3.0.
//   - enum and const; numbers compare by value, arrays and objects structurally.
//   - properties, required and additionalProperties (a boolean or a schema). A property not listed in
//     properties is checked against additionalProperties when that keyword is present, and is otherwise
//     accepted.
//   - items as a single schema; the tuple form (an array of schemas) is not checked.
//   - minItems, maxItems, minLength and maxLength (counted in Unicode characters), minimum and maximum.
//   - allOf (every branch), anyOf (at least one) and oneOf (exactly one), and boolean schemas (true accepts
//     everything, false nothing).
//   - local $ref: "#", "#/$defs/..." and "#/definitions/...", with JSON-pointer escapes (~0, ~1). A non-local
//     or unresolvable ref fails validation. Nesting (including $ref recursion) stops at depth 64, so a
//     self-referencing schema cannot loop.
//
// Every other keyword (pattern, format, exclusiveMinimum, ...) is ignored, so validation is never stricter
// than the schema says. Keyword checks only apply to values of the matching JSON type (minLength ignores a
// number, and so on); type is what rejects the wrong type.
//
// # Parsing and errors
//
// Both the schema and the document must be exactly one JSON value (trailing data is an error); numbers are
// decoded with json.Decoder.UseNumber, so large integers keep their precision. Validate reports the first
// mismatch only, prefixed with its JSON path ("$", "$.name", "$.list[3]").
//
// # Tests
//
// jsonschema_test.go covers each keyword, bad schemas and the bounded $ref loop. The subset is hand-written
// rather than a third-party validator because it is small and fully tested, and it lives outside the
// gateway adapter so the gateway, the fake and reflect share one implementation. SPEC "Ports"
// (ai.LLM.Structured).
package jsonschema
