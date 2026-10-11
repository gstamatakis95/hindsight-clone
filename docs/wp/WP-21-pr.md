# WP-21 — Final-review fix-up: config writes (FX-A1)

Branch: `wp/WP-21-config-writes`

## Built
- [x] HIGH: with an implicit mask, `models` merges per slot through `config.Merge` instead of being
  replaced. This applies to UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel
  `spec.overrides.models`.
  - Code: `splitModels` and `mergeModelsAt` in `internal/server/convert_fieldmask.go`.
  - An explicit `models` / `overrides.models` path (or a parent) still replaces the list, and `[]` clears
    it.
  - Decision 604 is updated.
- [x] MEDIUM: whole-config writes are validated with `ValidateOverrides(config.RPCBankConfig, …)`.
  - CreateBank and CreateBankFromTemplate (the template config) check the decoded config before calling
    the engine.
  - UpdateBankConfig and UpdateTenantConfig check the **patched** level inside the ConfigPatch. The patch
    stays pure and runs in the engine's transaction.
  - Under an implicit mask the request level is checked too, and so are UpdateMentalModel's request
    overrides with `RPCMentalModelSpec`. Without this the per-slot merge would hide a duplicate slot.
  - ResetBankConfig is deliberately not validated (see decision 2100).

## Acceptance tests (internal/server)
| # | Test | Result |
|---|------|--------|
| 1 | `TestConfigWriteModelsMergePerSlot/UpdateBankConfig` | pass |
| 2 | `TestConfigWriteModelsExplicitMask/{UpdateBankConfig,UpdateTenantConfig,UpdateMentalModel}/{replace,clear}` | pass |
| 3 | `TestConfigWriteModelsMergePerSlot/{UpdateTenantConfig,UpdateMentalModel,…/no stored overrides,…/other overrides kept}`, `TestUpdateMentalModelDuplicateSlot` | pass |
| 4 | `TestConfigWritesValidated/{CreateBank,UpdateBankConfig,UpdateBankConfig/masked,UpdateTenantConfig,CreateBankFromTemplate}/{EMBED binding,no model,duplicate slot,disposition 9,strategy bound to REFLECT,valid}` | pass |
| 5 | `TestServerDefaultsStillValidate`; make gen (no diff), make lint (BREAKING_AGAINST=contracts-v1), make test, make e2e | pass |

## Decisions and CCRs
- `docs/decisions/2100-config-writes-merge-and-validate.md` (new).
- `docs/decisions/604-fieldmask-semantics.md` (updated).
- No CCRs. No frozen paths are touched.

## Known gaps
- `configPatch` and `specPatch` now take a `check` argument, so the existing call sites in
  `internal/server/fieldmask_test.go` pass `nil`. That file needed a mechanical edit.
- The per-slot merge reuses `config.Merge`, not `config.upsertSlot`. `upsertSlot` is unexported, and
  `internal/config` is outside this WP. Merge applies the same rule.
