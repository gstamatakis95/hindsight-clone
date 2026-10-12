# Examples

Nine small programs that show memd from the point of view of the application that uses it. Each one is a
scenario: a goal, the calls that reach it, and output you can read. They use only the generated Go client
(`memd/gen/memory/v1`), gRPC and the standard library, and every folder also has the same calls as
[grpcurl](https://github.com/fullstorydev/grpcurl) commands.

| Scenario | What it shows | RPCs |
|---|---|---|
| [chat-assistant](chat-assistant) | An assistant that remembers a conversation, recalls what is relevant and answers with citations | `Retain`, `Recall`, `Reflect` |
| [model-per-step](model-per-step) | A cheap model for extraction and consolidation, a strong one for reflect and mental models, a reranker; bank-level routing, per-request overrides, and the usage that proves which model ran | `UpdateBankConfig`, `Retain`, `Consolidate`, `Recall`, `Reflect`, `CreateMentalModel`, `GetOperation` |
| [multi-tenant](multi-tenant) | Tenants (`x-tenant-id`) with their own orgs and defaults, banks created from a template, a mission and disposition per bank, isolation | `CreateOrg`, `UpdateTenantConfig`, `GetTenantConfig`, `CreateBank`, `ExportBankTemplate`, `UpdateBankConfig`, `CreateDirective`, `GetBankStats` |
| [async-ingest](async-ingest) | A batch retained with `run_async`, polled through operations, followed through the event feed, with cancel and retry | `Retain`, `GetOperation`, `ListOperations`, `ListEvents`, `CancelOperation`, `RetryOperation` |
| [batch-ingest](batch-ingest) | Bulk ingestion from an at-least-once producer: a status per item, a redelivered batch that writes nothing, a late old version ignored, backing off from a full backlog; commands.md also feeds a Kafka topic to `memd ingest` | `RetainBatch`, `GetOperation`, `GetDocument` |
| [mental-models](mental-models) | Pages that refresh after consolidation or on a cron schedule, agentic versus single-shot, preview, search, history | `CreateMentalModel`, `PreviewMentalModelRefresh`, `GetMentalModel`, `ListMentalModelHistory`, `SearchMentalModels`, `ListMentalModels` |
| [curation](curation) | Fixing memory: edit a fact, invalidate and restore it, retag and reprocess a document, rebuild observations | `UpdateMemory`, `InvalidateMemory`, `RestoreMemory`, `ListMemories`, `RetagDocument`, `ReprocessDocument`, `ClearObservations` |
| [directives-structured](directives-structured) | Standing rules that shape Reflect, a rule for one call, and JSON that matches a schema | `CreateDirective`, `UpdateDirective`, `PreviewPrompt`, `Reflect` with `response_schema_json` |
| [recall-filters](recall-filters) | Tag groups, time windows, budgets, observations first with evidence attached, choosing retrieval arms | `Recall` with `TagFilter`, `time_window_*`, `budget`, `prefer_observations`, `include`, `arms`; `ListRecallArms` |

Each folder holds:

- `README.md`: the goal, how to run it and the output to expect;
- `main.go`: the program, commented with the intent of each step;
- `commands.md`: the same calls as grpcurl commands (needs `grpcurl` and `jq`).

`internal/exampleutil` is the small helper they share: the `-addr`, `-tenant` and `-org` flags, a connection that
sends the tenant header, the org every example creates before it starts (banks live in an org, and a write never
creates one), and polling helpers.

## Shared setup

The examples need a running memd with its worker (`memd all`) and a gateway to call. The quickest way is the
deterministic gateway emulator that the end-to-end tests use. It answers every prompt with schema-valid output and
768-dimension embeddings, so each example runs end to end without a model. Its answers quote the retained text
instead of being real answers.

```sh
make up        # Postgres and MinIO via docker compose
make migrate   # create the schema

# The emulator listens on :4000. -write-config writes memd.yaml, binding every model slot to it.
go run ./test/e2e/gatewayemu/cmd/gatewayemu -write-config memd.yaml &

export MEMD_GATEWAY_URL=http://127.0.0.1:4000/v1 \
  MEMD_S3_ENDPOINT=http://localhost:59000 MEMD_S3_BUCKET=memd MEMD_S3_ACCESS_KEY=memd \
  MEMD_S3_SECRET_KEY=memd-secret MEMD_S3_PATH_STYLE=true
make run       # memd all, serving gRPC on :50051
```

Then, from another terminal, run one example or all of them:

```sh
go run ./examples/chat-assistant
make examples                      # builds everything, then runs every scenario in turn
make examples ADDR=host:50051 TENANT=demo
```

`-addr` (default `localhost:50051`), `-tenant` and `-org` (both default `examples`) can also come from the
`MEMD_ADDR`, `MEMD_TENANT` and `MEMD_ORG` environment variables. An example first creates its org (an org that
already exists is fine) and then banks with fresh names on every run, so running it twice is fine. It exits 1 with a `FAILED:` line when the behaviour it demonstrates does not hold.

### With a real gateway

Nothing in the programs changes: point memd at an OpenAI-compatible gateway and bind your models in the server config
file.

```sh
export MEMD_GATEWAY_URL=https://your-gateway.example/v1 MEMD_GATEWAY_API_KEY=...
cp memd.example.yaml memd.yaml   # then bind the models under defaults.models (and keep the embed binding)
make run
```

[model-per-step](model-per-step) takes `-cheap`, `-strong`, `-reranker` and `-one-off` flags for the model names your
gateway serves. The other examples work with whatever `defaults.models` binds; `memd.example.yaml` has the details.

## Tests

`make e2e` builds every example and runs each against a fresh `memd all` and the emulator (`TestExamples` in
`test/e2e`).
