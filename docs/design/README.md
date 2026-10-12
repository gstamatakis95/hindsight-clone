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

## Diagrams

The diagrams are D2 sources in [diagrams/src](diagrams/src) (shared classes in `_style.d2`), rendered with the ELK
layout to SVGs that follow the viewer's light or dark colour scheme. `make diagrams` re-renders them (d2 v0.9.0) and
`make diagrams-check` fails when a committed SVG is stale. A dashed outline marks something not built yet.

| Diagram | Shows | Embedded in |
|---|---|---|
| [deployment](diagrams/deployment.svg) | The memd processes, Postgres, object storage, the AI gateway, the migration step and the optional broker | [README](../../README.md), [architecture.md](architecture.md) |
| [layering](diagrams/layering.svg) | The package tiers and the allowed import direction | [architecture.md](architecture.md) |
| [job-pipeline](diagrams/job-pipeline.svg) | Enqueue, claim, run, settle, the status moves and the maintenance tick | [architecture.md](architecture.md) |
| [ingest-queue](diagrams/ingest-queue.svg) | Queue ingestion: Kafka, the ingest boundary, memd ingest (decode, group, RetainBatch) and the settlements | [retain.md](retain.md#queue-ingestion) |
| [retain-flow](diagrams/retain-flow.svg) | The Retain RPC, the write transaction, the retain run and its batches | [retain.md](retain.md) |
| [recall-flow](diagrams/recall-flow.svg) | Parse, the arms, fusion, rerank, boosts, packing and includes | [recall.md](recall.md) |
| [reflect-loop](diagrams/reflect-loop.svg) | The tool loop, the evidence guard, closing and overflow, citations | [reflect-and-mental-models.md](reflect-and-mental-models.md) |
| [mental-model-refresh](diagrams/mental-model-refresh.svg) | Triggers, scheduling, the run, the strategies and the write | [reflect-and-mental-models.md](reflect-and-mental-models.md) |
| [consolidation-flow](diagrams/consolidation-flow.svg) | Plan, per-scope decide and apply, finish and the continuation | [consolidation.md](consolidation.md) |
| [deletion-flow](diagrams/deletion-flow.svg) | DeleteOrg's transaction, the delete_org run (fan-out, sweep, blobs, verification) and the receipt | [storage.md](storage.md#deleting-an-org) |
| [curation-flow](diagrams/curation-flow.svg) | Update, invalidate and restore, and the jobs they enqueue | [curation-and-operations.md](curation-and-operations.md) |

Related: [eval/README.md](../../eval/README.md) covers the benchmarks (LoCoMo, LongMemEval, golden fixtures; how a
run works, the LoCoMo methodology and how to add a benchmark), and [examples/README.md](../../examples/README.md)
the runnable client scenarios.

Conventions: present tense; a section names the package that implements it; defaults are the built-in ones unless
the server config file overrides them. When behaviour changes, update the design section, its row in
rules-to-tests.md and the tests together.
