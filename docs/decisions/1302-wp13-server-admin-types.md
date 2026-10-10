# 1302 — Server admin interfaces: identical engine-side types, aliased by the server

## Context
Decision 601 declared `TenantAdmin`, `BankAdmin`, `DirectiveAdmin`, `DocumentAdmin` and
`ObservationAdmin` in `internal/server`, and WP-13 is to implement them "structurally, without importing
internal/server". Go method sets match only when parameter and result types are identical, and several of
those methods use server-declared named types (`ConfigPatch`, `ListDocumentsRequest`, `DocumentPage`,
`DocumentView`, `ScopePage`, `ClearResult`). Engines may not import `internal/server` (depguard), so those
methods cannot be satisfied structurally as declared. Also, `TemplateCodec.Create` drops the bank `name`
and returns no mental-model builds, but the server calls it and WP-13 can't edit the server.

## Decision
- WP-13 declares the same interfaces and identical types in its packages:
  - `bank.ConfigPatch`, `bank.TenantAdmin`, `bank.BankAdmin`, `bank.DirectiveAdmin`;
  - `docs.ListDocumentsRequest`, `docs.DocumentPage`, `docs.DocumentView`, `docs.DocumentAdmin`;
  - `curate.ScopePage`, `curate.ClearResult`, `curate.ObservationAdmin`.
- `DirectiveAdmin` uses only domain and port types, so the WP-13 implementation already satisfies
  `server.DirectiveAdmin`. `TenantAdmin.TenantConfig` and `BankAdmin.CreateBank/GetBank` match too.
- **WP-14** turns the server's named types into aliases, one line each:
  `type ConfigPatch = bank.ConfigPatch`, `type ListDocumentsRequest = docs.ListDocumentsRequest`,
  `type DocumentPage = docs.DocumentPage`, `type DocumentView = docs.DocumentView`,
  `type ScopePage = curate.ScopePage`, `type ClearResult = curate.ClearResult`. The server already imports
  these packages, so no new edge appears, and the server-side interfaces are then satisfied unchanged.
- `TemplateCodec` gains `CreateNamed(ctx, b, name, t) (domain.Bank, []domain.Operation, error)`; `Create`
  stays (it calls `CreateNamed` with an empty name) so the server builds. WP-14 switches
  `CreateBankFromTemplate` to `CreateNamed` and fills `mental_model_builds`.
- `docs.Retagger` gains `Apply(ctx, j)`, the retag_document job; the server only calls `Retag`.

## Alternatives
- Adapters in `cmd/memd/wire.go` converting each type: rejected, pure boilerplate for identical shapes.
- Moving the types to `domain`: rejected, frozen path and RPC-specific shapes.

## Spec lines involved
Decision 601; SPEC "Code standards" (depguard; consumer-declared interfaces), gRPC
CreateBankFromTemplateResponse (`mental_model_builds`).
