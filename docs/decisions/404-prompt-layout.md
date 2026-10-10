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
- **Directive done tools.** `reflect_tool_done_*directives.json` keep Hindsight's wording with a
  `{directive_rules}` slot. `prompts.DoneTool(name, *ReflectData)` fills it from the
  `reflect_tool_directive_rules` fragment template, a numbered list rather than a whole message. Callers
  read those two assets through `DoneTool`, not `JSON`.
- **Typed data.** Every template family has a data struct in `names.go`, such as `ExtractData` or
  `ReflectData`. The render test decodes each fixture into that struct with unknown fields rejected, so
  the field names engines use are checked against the templates.
- **Licence.** `templates/NOTICE` carries Hindsight's MIT licence verbatim. Each template's header
  comment names its Hindsight source (file and symbol), or says "new in memd".

## Hindsight parity (review round 1)
- **fact_type.** Extraction uses Hindsight's labels `world | assistant`, in the prompt text and the schema
  enum. Models were tuned on Hindsight's wording, and "assistant" tells the model the fact is the agent's
  own experience. WP-08 maps `assistant` to `domain.FactTypeExperience`.
- **Optional dimensions.** Hindsight's `retain_optional_fact_dimensions` (default off, server-level,
  config.py:1657) never removes who/what/when/where/why. They always feed the fact text. The flag only
  changes the "no value" marker from "N/A" to null and adds the OPTIONAL FIELDS section, which helps weak
  models that otherwise invent dates.
  - BankConfig has no knob for it, so `ExtractData.OptionalDimensions` defaults to false (Hindsight's
    default).
  - The schema types the fields `["string","null"]` in both modes, so engines treat "N/A" and null as
    empty.
- **Narrator rule.** It is ported word for word (fact_extraction.py:1847-1861) into `extract_user`, as in
  Hindsight, with the optional `ExtractData.AgentName`.
- **CAUSAL section.** It renders only when `ExtractData.CausalLinks` is set (never in verbatim).
- **Consolidation.** Source memories under existing observations are rendered without unit ids
  (`SourceMemory`), as Hindsight's `_build_observations_for_llm` does. New facts keep their `[id]`,
  because `source_fact_ids` may name only facts of the batch.
  - WP-10 injects the per-call schema constraints: the batch ids as the `source_fact_ids` enum,
    `minItems: 1`, and the remaining capacity as `creates.maxItems`.
- **Dropped:**
  - language and output-language rules (multilingual is out of scope);
  - attachments, chunk numbering and the ISO-timestamp `pattern`;
  - recall's `max_chunk_tokens` and expand's `depth` tool parameters (memd's expand returns source facts
    and chunks);
  - retraction prompts and claims/reduce split synthesis.
- **json helper.** The template `json` helper does not HTML-escape.

## Alternatives
- **One file per operation with `{{define}}` blocks.** Rejected: it makes `Render`'s name scheme
  two-level.
- **Schemas in Go.** Rejected: they are part of the prompt contract and would change without changing
  `Version`.

## Spec lines involved
Function signatures (internal/prompts), AGENTS.md ("Prompts live in internal/prompts/templates"),
Ports (`StructuredRequest.Name`, `ToolSpec`).
