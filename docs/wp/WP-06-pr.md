# WP-06 · Server shell

Branch `wp/WP-06-server-shell`. All code is in `internal/server/**`. Two changes fall outside that path:
- one line in `.mockery.yaml`, see decision 601;
- the generated `internal/mocks/server`.

## Built
- [x] **Interceptors** (`interceptors.go`):
  - `TenantInterceptor` reads `x-tenant-id`. A missing, empty, repeated or malformed header is
    INVALID_ARGUMENT, and health calls are exempt (decision 600).
  - Tenant and bank ids may not contain `/` or control characters. This prevents blob-prefix collisions,
    which could otherwise delete another bank's data.
  - `DeadlineInterceptor(def)` applies the default only when the call has no deadline.
- [x] **`toStatus`** (`status.go`) maps every domain error, context errors and existing statuses; anything
  else is INTERNAL (decision 602).
- [x] **Conversion** (`convert_*.go`): proto ↔ domain for every message, with descriptor-driven enums
  (decision 603).
  - FieldMask patches (`convert_fieldmask.go`) for tenant config, bank config, reset and mental-model
    spec. Paths are applied on the domain struct, so stored explicit clears survive, and patches are pure
    (decision 604).
- [x] **Every service server** (`svc_*.go`, 48 RPCs) delegates to engine interfaces where they exist.
  - Where no engine interface exists, it delegates to server-declared consumer interfaces in `deps.go`
    (decision 601).
  - A nil dependency returns UNIMPLEMENTED instead of panicking.
  - `NewMemoryServer` and `NewMemoryUnitServer` have the spec signatures.
- [x] **`New(d Deps, opts...)`** registers the 8 services and health. **Health** (`health.go`): `Check`
  runs the readiness probes.
- [x] **CCR WP-07-1:** `RetainResponse.usage` is mapped and round-tripped (see gaps).

## Acceptance tests (all pass, `go test -race ./internal/server/`)
| Card item | Tests |
|---|---|
| Round-trip conversion for every message, fuzzed | `TestRoundTripEveryMessage`: every memory.v1 message has a round trip (96, 200 random samples each) or an exemption with a reason, or the test fails. Also `FuzzRoundTrip`, `TestEnumMapping` (every enum value pinned), `TestUnknownEnumRejected`, `TestGoldenRetainRequest`, `TestGoldenRecallRequest`, `TestGoldenReflectRequest`, `TestUpdateMemoryEntitiesPresence`, `TestStrategyMatchBadScope`, `TestMentalModelDetail`, `TestFieldMaskConfigPatch`, `TestFieldMaskInvalid`, `TestFieldMaskReset`, `TestFieldMaskSpecPatch`, `TestFieldMaskPathsResolve` |
| Every RPC registered and reachable over bufconn with mocks | `TestEveryRPCReachable` enumerates the 48 methods from the service descriptors, calls each one with mockery mocks and checks for OK and a mock hit. Also `TestServiceInfo`, `TestHealthWithoutTenant`, `TestTenantReachesEngine`, `TestUnwiredDependency` |
| Error mapping table | `TestToStatus` |
| Interceptors | `TestTenantInterceptor`, `TestDeadlineInterceptor`, `TestBankRef` |

Gates: `make gen` leaves no diff; `make lint BREAKING_AGAINST=contracts-v1` and `make test` pass.

## Decisions and CCRs
- 600 tenant header and id checks
- 601 server-declared consumer interfaces, plus the `.mockery.yaml` line
- 602 error mapping
- 603 conversion conventions
- 604 FieldMask semantics

No CCRs filed.

## Known gaps
- **No implementations yet for the consumer interfaces:** `TenantAdmin`, `BankAdmin`, `DirectiveAdmin`,
  `DocumentAdmin`, `ObservationAdmin`, `MentalModelAdmin` and `OperationReader`.
  - They serve 29 RPCs. WP-14, or an assigned WP, must implement them.
  - ListDocuments, ListScopes, ListMentalModels and DeleteFolder also need port methods that don't exist
    yet (a CCR on `internal/port`).
- **`RetainResponse.usage` stays empty** until WP-08 adds `Usage []domain.Usage` to `retain.IngestResult`.
  Then it's a one-line change in `svc_memory.go` (marked `TODO(WP-08)`).
- **CreateBankFromTemplate.** `TemplateCodec.Create` takes no name and returns only the bank, so the
  request's `name` is dropped and `mental_model_builds` is empty.
- **Override validation.** It runs only through `Deps.ValidateOverrides`, which is nil until WP-14 wires
  `config.ValidateOverrides`.
  - The rpc names passed are the method names (Retain, Recall, Reflect, UpdateMemory, Consolidate,
    ReprocessDocument, PreviewPrompt, CreateMentalModel, RefreshMentalModel).
  - UpdateMentalModel does not validate `spec.overrides`.
- **Explicit clears in jsonb.** BankConfig's JSON `omitempty` drops a persisted explicit clear (a non-nil
  empty list) on reload. That's a domain or adapter concern, recorded in decision 604.
