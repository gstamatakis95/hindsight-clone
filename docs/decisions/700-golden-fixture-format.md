# 700 — Golden fixtures are YAML in JSON-compatible flow style, graded by substring

## Context
The WP-07 card asks for golden fixtures at `eval/golden/*.yaml` that CI runs against `memd all` with the fake
gateway. The approved dependency list has no YAML library, and a fake gateway's extraction and answers are not
meaningful enough for an LLM judge.

## Decision
- Fixtures are YAML 1.2 written in its JSON-compatible flow style (JSON is a subset of YAML 1.2), decoded with
  `encoding/json` and `DisallowUnknownFields`. Any YAML tool reads them; no new dependency.
- One fixture = one bank: `name`, `sessions` (`id`, `time`, `context`, `content`), `checks` (`id`, `query`,
  `accept`, optional `category`, default = fixture name).
- Golden runs default to `-mode recall -judge contains`: a check passes when any recalled memory contains one of
  its `accept` substrings (case-insensitive). No model is called, so the run is deterministic; the CLI exits
  non-zero when any check fails, so CI can gate on it.

## Alternatives
- Add `gopkg.in/yaml.v3` through a CCR for block-style YAML. Deferred: block style is nicer to read but not
  needed for an early iteration. If wanted, swapping the decoder is a few lines in `eval/golden/golden.go`.
- A hand-written YAML subset parser. Rejected: more code, and easy to get subtly wrong.
- Grade golden checks with the LLM judge. Rejected: CI would need a real model.

## Spec lines involved
SPEC "File structure" (`golden/*.yaml  # retain→recall fixtures run in CI`); WP-07 card "Golden fixtures";
playbook dependency list.
