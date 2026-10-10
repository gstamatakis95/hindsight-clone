# 601 — Server-declared consumer interfaces for RPCs without an engine

## Context
The card says every service server delegates to engine interfaces. The Engines section covers 19 of the
48 RPCs:
- Retain, Recall and Reflect.
- The memory-unit RPCs.
- Stats, tags, prompt preview, bank delete, templates.
- Retag, the strategy preview, dry run and page search.
- Cancel and retry, via `worker.OperationControl`.

The remaining 29 RPCs have no engine interface:
- Tenant config.
- Bank create, get and config get / patch / reset.
- Directive CRUD.
- List and get documents, reprocess.
- Consolidate, list scopes and the two clears.
- Mental-model CRUD, refresh, clear, history and folders.
- Get and list operations, list events.

Several of them also have no port method yet (list documents, list scopes, list mental models, delete
folder).

## Decision
- **Consumer interfaces.** `internal/server/deps.go` declares small consumer interfaces with domain and
  port types only: `TenantAdmin`, `BankAdmin`, `DirectiveAdmin`, `DocumentAdmin`, `ObservationAdmin`,
  `MentalModelAdmin`, `OperationReader`, plus `ReadinessProbe` for health. The engine rule ("declare the
  small interface you consume") applies to the server too.
- **What they return.** Each returns exactly what its response needs: a `domain.Operation` rather than an
  id, `(op, joined)` for refresh, and the original text for GetDocument.
- **Patches.** Patch RPCs pass a pure `ConfigPatch` / `SpecPatch` function. The implementation applies it
  inside its own write transaction, so two concurrent patches can't lose each other's changes. The server
  never does a get followed by a set.
- **No implementations here.** The server does not implement these interfaces over `port.Store`. Enqueue
  and join rules, Seq stamping and Invalidator calls belong to the engine and integration packages
  (WP-12, WP-13, WP-14).
- **Unwired dependencies.** A nil dependency makes its RPCs return UNIMPLEMENTED ("not wired") instead
  of panicking.
- **Mocks.** Hand-written mocks are forbidden, so `.mockery.yaml` gains one line,
  `memd/internal/server: {}`, which generates `internal/mocks/server`. The file is outside WP-06's Owns
  path but not frozen; this is flagged in the PR. The bufconn tests live in `package server_test` to avoid
  an import cycle.

## Alternatives
- **Implementing the CRUD RPCs in the server over `port.Store`.** Rejected: it puts business logic
  (queue semantics, consolidation resets) in the transport layer, and several RPCs would need port changes
  anyway.
- **Leaving the RPCs unimplemented.** Rejected: the card requires every RPC to be reachable.

## Spec lines involved
gRPC surface (services), Code standards (strict interfaces, consumer-declared interfaces, mockery-only
mocks), Function signatures (`internal/server`).
