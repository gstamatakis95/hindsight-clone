# memd design

These documents describe how memd works and why: the behaviour the code implements, the invariants that keep
memory consistent, and the defaults. The API contract itself is `api/memory/v1/memory.proto` (its header states the
shared conventions); the root [README](../../README.md) is the user guide.

| Document | Covers |
|---|---|
| [architecture.md](architecture.md) | Process model, package tiers, the store / graph / ai / blob boundaries, tenancy, transactions and the change sequence, the job queue (kinds, dedupe, claim, heartbeats, retries, cancel and retry, runner), the maintenance tick, lifecycle events, the model gateway, prompts, tokens, Datadog observability |
| [storage.md](storage.md) | Postgres and object storage: conventions, tables, indexes, search query shapes, blob layout, migrations, bank deletion, archive and restore, adapter behaviour |
| [retain.md](retain.md) | Ingest, the retain run, replacement, ownership and resume, extraction, time normalization, entity resolution, links, graph maintenance, reprocess, retag |
| [recall.md](recall.md) | Query parsing, tag filters, the recall arms (and how to add one), budgets, fusion, rerank, boosts, floors, prefer_observations, packing, includes, staleness, trace |
| [consolidation.md](consolidation.md) | Scopes, the consolidation run, related observations and dedup, stamping, failure handling, strategies, refresh triggers, enqueue sources, clearing observations |
| [reflect-and-mental-models.md](reflect-and-mental-models.md) | The reflect loop, tools, evidence and citations, directives, failure classes, structured output; mental models: data, staleness, the refresh pipeline, strategies, queueing, failures and pause, admin, search, preview |
| [curation-and-operations.md](curation-and-operations.md) | The seven services' admin surface: memories and curation, observation admin, documents, banks, config levels, directives, prompt preview, operations and events, request handling, error mapping |
| [configuration.md](configuration.md) | BankConfig levels and merge rules, model routing, FieldMask writes, per-request overrides, every field and default, the server config file, environment variables |
| [known-limitations.md](known-limitations.md) | Accepted gaps, their impact and workarounds |
| [rules-to-tests.md](rules-to-tests.md) | Every behavioural rule mapped to the tests that assert it |

Conventions: present tense; a section names the package that implements it; defaults are the built-in ones unless
the server config file overrides them. When behaviour changes, update the design section, its row in
rules-to-tests.md and the tests together.
