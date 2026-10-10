# 303 — Structured output is validated against a JSON Schema subset

## Context
port.LLM.Structured says "the adapter validates against Schema". No JSON Schema library is on the approved
dependency list, and the full draft 2020-12 vocabulary is large.

## Decision
`internal/adapter/gateway/schema.go` implements the subset that structured-output schemas use: the OpenAI
structured-output subset plus common bounds.

- **Supported keywords:**
  - `type`, as a name or a list such as `["string","null"]`. `integer` accepts `3.0`.
  - `enum` and `const`. Numbers compare by value.
  - `properties`, `required`, and `additionalProperties` (boolean or schema).
  - `items`, as a single schema. The tuple form is not checked.
  - `minItems`, `maxItems`, `minLength`, `maxLength` (counted in characters), `minimum` and `maximum`.
  - `allOf`, `anyOf`, `oneOf` and boolean schemas.
  - Local `$ref`: `#`, `#/$defs/...` and `#/definitions/...`, with JSON-pointer escapes. Recursion stops at
    depth 64.
- **Unknown keywords** (`pattern`, `format`, `exclusiveMinimum`, ...) are ignored, so validation is never
  stricter than the schema says.
- **Parsing.** Numbers are decoded with `UseNumber`. The document must be exactly one JSON value.
- **Errors.** The first mismatch is reported with its JSON path. That text is what the retry sends back to
  the model (decision 301).

## Alternatives
- A third-party validator (santhosh-tekuri/jsonschema, xeipuuv/gojsonschema): rejected. It would need a CCR
  for a new dependency, while the subset is about 300 lines and fully tested.
- Only checking that the output is JSON and decodes into `out`: rejected. It misses missing required fields
  and wrong enum values, which are the common model failures.

## Spec lines involved
port.LLM.Structured doc; SWARM_PROMPT dependency list.
