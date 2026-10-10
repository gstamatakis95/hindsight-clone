# 404 — Prompt template layout, schemas and versions

## Context
The spec gives `prompts.Render(name, data)` and `prompts.Version(name)`, the content hash recorded on every
LLM call. It leaves several things open:
- how a system and user message pair is laid out;
- where response schemas and tool declarations live, given that tool descriptions are prompt text;
- what the hash covers.

## Decision
- **One template, one message.** `templates/<name>.tmpl` is one message, written in Go `text/template`
  with `missingkey=error`. A pair is `<op>_system` plus `<op>_user`. There are no shared partials, so each
  file's hash is its whole content.
- **Response schemas** are named after the system template they belong to:
  `templates/<system>.schema.json`, read with `prompts.Schema(name)`.
  - `Version(name)` hashes the template and, when one exists, its schema. It is the first 16 hex digits
    of SHA-256.
  - Callers put the system template's name in `StructuredRequest.Name` / `ChatRequest.Name`.
  - Editing only a user template does not change the recorded version. This is accepted for v1.
- **Tool declarations** are `templates/<name>.json`, read with `prompts.JSON(name)`. Examples are
  `reflect_tools` and the `done` variants. Each is `[{name, description, parameters}]`, which maps 1:1
  onto `port.ToolSpec`.
- **Typed data.** Every template family has a data struct in `names.go`, such as `ExtractData` or
  `ReflectData`. The render test decodes each fixture into that struct with unknown fields rejected, so
  the field names engines use are checked against the templates.
- **Licence.** `templates/NOTICE` carries Hindsight's MIT licence verbatim. Each template's header
  comment names its Hindsight source (file and symbol), or says "new in memd".

## Alternatives
- **One file per operation with `{{define}}` blocks.** Rejected: it makes `Render`'s name scheme
  two-level.
- **Schemas in Go.** Rejected: they are part of the prompt contract and would change without changing
  `Version`.

## Spec lines involved
Function signatures (internal/prompts), AGENTS.md ("Prompts live in internal/prompts/templates"),
Ports (`StructuredRequest.Name`, `ToolSpec`).
