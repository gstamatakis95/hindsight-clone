// Package prompts holds every LLM prompt as an embedded text/template under templates/ and renders them by
// name. Engines never build prompt text inline (AGENTS.md "Prompts live in internal/prompts/templates").
//
// # Where it sits
//
// prompts is a leaf package imported by the engines that call models: retain (extraction), consolidate
// (CONSOLIDATE, DEDUP_CHECK), reflect (the tool loop, final answer, structured output), mentalmodel
// (SYNTHESIZE, the delta prompts, the agentic refresh context), timex (TIME_PARSE) and bank (prompt
// preview). It imports only the standard library and domain. Everything is read from an embed.FS, so the
// binary carries its prompts and no file system access happens at run time.
//
// # Files under templates/
//
//   - <name>.tmpl: one message (a system or a user message) in Go text/template syntax. A system/user pair
//     is <op>_system plus <op>_user; there are no shared partials. Templates are parsed once, with
//     Option("missingkey=error").
//   - <system>.schema.json: the response JSON Schema of a structured call, named after its system
//     template (extract_concise.schema.json, consolidate_system.schema.json, ...).
//   - <name>.json: a JSON asset such as a tool list ([{name, description, parameters}], one entry per
//     port.ToolSpec). Tool descriptions are prompt text too, so they live here.
//   - NOTICE: the MIT licence of Hindsight, whose prompts most templates port; each template's header
//     comment names its Hindsight source or says "new in memd".
//
// # Usage
//
// The exported constants in names.go name every template ([ExtractConcise], [ConsolidateSystem],
// [ReflectSystem], ...) and JSON asset ([ReflectTools], [ReflectToolDoneDocument], ...). Each template
// family reads one typed data struct ([ExtractData], [ConsolidateData], [DedupData], [ReflectData],
// [StructuredOutputData], [SynthesizeData], [DeltaData], [RefreshContextData]); the TIME_PARSE templates
// (time_parse_system, time_parse_user) are named by timex itself and read its own data. An engine fills the
// struct once and renders each template of the family with it:
//
//	data := prompts.DedupData{New: candidate, Existing: match.Text}
//	system, err := prompts.Render(prompts.DedupSystem, data)
//	user, err := prompts.Render(prompts.DedupUser, data)
//	schema, err := prompts.Schema(prompts.DedupSystem)
//	req := port.StructuredRequest{Name: prompts.DedupSystem, System: system, User: user, Schema: schema, ...}
//
// The functions:
//
//   - [Render] executes a template; an unknown name is domain.ErrInvalidArgument, and a missing map key or
//     a failing helper is an execution error. Templates may use the helpers join, lower, upper, trim, add
//     and json (compact JSON without HTML escaping, so <, > and & reach the model as written).
//   - [Schema] returns templates/<name>.schema.json; [ExtractSchema] returns an extraction schema, and when
//     optionalDimensions is set it retypes when/where/who/why as string-or-null (Hindsight's optional
//     dimensions, default off; no BankConfig knob sets it yet).
//   - [JSON] returns a JSON asset; [DoneTool] returns a done-tool asset and fills the {directive_rules}
//     slot of the directive variants with the rendered [ReflectToolDirectiveRules] list.
//   - [Version] is the prompt's version; [Names] lists every template.
//
// A missing schema or asset is domain.ErrInvalidArgument; a file that is not valid JSON is an error.
//
// # Versions
//
// [Version] is the first 16 hex digits of the SHA-256 of the template file, followed by its response
// schema when one exists, followed by every tool file of its operation (templates/<op>_tool*.json and
// <op>_tool*.tmpl, sorted, where <op> is the name up to the first underscore; so reflect_system's version
// covers reflect_tools.json and the done variants). Callers put the system template's name in
// port.StructuredRequest.Name or port.ChatRequest.Name, and the call records that version, so traces and
// scripted fakes can tell prompt revisions apart. Editing only a user template does not change the
// recorded version (an accepted trade-off). An unknown name has version "".
//
// # Concurrency
//
// Templates are parsed once (sync.Once) on the first Render; a template that fails to parse fails every
// later Render. All functions are safe for concurrent use.
//
// # Tests
//
// prompts_test.go renders every template with its fixture testdata/<name>.json (decoded into the family's
// data struct with unknown fields rejected, so field names engines use are checked against the templates)
// and with zero data, renders rarely taken branches, pairs schemas with templates, validates every JSON
// asset, and pins Hindsight parity points (fact_type labels, id-less source memories, the N/A marker, the
// narrator rule, the gated causal section, directive rules in done tools, tool files in Version).
// reflect_golden_test.go compares reflect_system without the recall tool against a golden file. SPEC
// "Function signatures" (internal/prompts) defines Render and Version; "Model routing" names the slots.
package prompts
