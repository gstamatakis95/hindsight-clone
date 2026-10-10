// Package jsonschema validates JSON documents against the subset of JSON Schema that structured LLM output
// uses: type, enum, const, properties, required, additionalProperties, items, minItems, maxItems,
// minLength, maxLength, minimum, maximum, anyOf, oneOf, allOf and local $ref. Other keywords are ignored.
// The gateway adapter validates model output with it and the LLM fake validates scripted replies with it,
// so a mismatch between an engine's schema and its Go types fails in unit tests as it would in production.
package jsonschema
