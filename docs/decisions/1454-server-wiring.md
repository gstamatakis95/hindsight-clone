# 1454 — Server wiring: override RPC names, operation reads and glue

## Context
The server passes rpc names to `Deps.ValidateOverrides`. `config.ValidateOverrides` only knows the names in
its allowlist table. The server's admin request types mirror the engines' (decisions 1205, 1302). No engine
implements `server.OperationReader` or `reflect.PageReader`.

## Decision
- **Override names.**
  - PreviewPrompt validates with `config.PreviewRPC(op)`: the RPC whose overrides the previewed operation
    takes.
  - CreateMentalModel, UpdateMentalModel (inside the patch) and the template specs of
    CreateBankFromTemplate validate with `config.RPCMentalModelSpec`.
  - Before this, any such request carrying overrides failed with INVALID_ARGUMENT ("unknown rpc").
  - `server.TestRealValidatorAcceptsServerRPCNames` runs every name through the real validator.
- **Type aliases.** The server's `ConfigPatch`, `SpecPatch`, `ListDocumentsRequest`, `DocumentPage`,
  `DocumentView`, `ScopePage`, `ClearResult`, `ListMentalModelsRequest`, `MentalModelPage`,
  `RefreshRequest`, `HistoryRequest` and `HistoryPage` are aliases of the engine types. Compile-time
  assertions show that each engine admin satisfies its server interface.
- **CreateBankFromTemplate** calls `TemplateCodec.CreateNamed` and returns `mental_model_builds`. Retain
  copies `IngestResult.Usage` into `RetainResponse.usage` (CCR WP-07-1).
- **OperationReader** (`cmd/memd/glue.go`) reads through `Queue.Get`, `Queue.List` and `EventRepo.List`, in
  one short transaction each. An unknown or tombstoned bank is NOT_FOUND, operations and events included
  (SPEC: once deleting, reads return NOT_FOUND). Clients see a delete finish when GetBank returns NOT_FOUND
  (decision 1306).
- **PageReader** for the reflect page tools: search through `mentalmodel.PageSearcher`, reads through
  `MentalModelRepo.Get`.
- **One tokenizer** serves recall's TokenCounter and include budgets, retain's chunker and the consolidation
  budgets: tiktoken's cl100k_base from `github.com/tiktoken-go/tokenizer` v0.7.0. Its tables are embedded,
  so nothing is downloaded at start. v0.8 needs Go 1.26, and the module is pinned to Go 1.25 (decision 001).

## Alternatives
- **Mapping the names in `cmd/memd`.** Rejected: PreviewPrompt's name depends on the operation, which only
  the server sees.
- **Serving operation reads on a deleting bank,** so the delete operation can be polled. Rejected: the spec
  says reads return NOT_FOUND once deleting.

## Spec lines involved
SPEC "Per-request overrides", "Behavioural rules / Banks and documents" (DeleteBank reads), "Function
signatures" (`ValidateOverrides`); decisions 601, 1205, 1302, 1306; CCR WP-07-1.
