// Package prompts holds every LLM prompt as an embedded text/template under templates/ and renders
// them by name. Each template's content hash is its version, recorded on every LLM call so traces and
// scripted fakes can tell prompt revisions apart. Engines never build prompt text inline.
package prompts
