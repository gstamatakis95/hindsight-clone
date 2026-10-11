# 2100 — Config writes: per-slot model merge and whole-level validation

## Context
Final review A, finding FX-A1, raised two problems.

- With no update_mask, UpdateBankConfig, UpdateTenantConfig and UpdateMentalModel replaced `models`
  wholesale. So binding REFLECT dropped a stored EXTRACT binding. SPEC "Model routing" says "setting one
  slot never clears the others", and the BankConfig comment says "models: merged per slot".
- The whole-config writes were never validated: CreateBank, UpdateBankConfig, UpdateTenantConfig and the
  template config in CreateBankFromTemplate. An EMBED binding, an empty model name, a duplicate slot,
  disposition 9 or a strategy binding REFLECT was stored, and only failed later at call time.

## Decision
- **Merge.** With an implicit mask, `configPatch` and `specPatch` pull the `models` / `overrides.models`
  leaf out of the path list. Inside the patch they merge it per slot into the current value with
  `config.Merge`. The engine-side `upsertSlot` is unexported and `internal/config` is outside this WP;
  Merge applies the same rule and returns memory shared with neither input.
  - An explicit path still replaces the list, and `[]` clears it (decision 604).
- **Validation.**
  - CreateBank and CreateBankFromTemplate run `ValidateOverrides(config.RPCBankConfig, cfg)` on the decoded
    config before calling the engine.
  - UpdateBankConfig and UpdateTenantConfig pass a checker into `configPatch`, which runs it on the
    **patched** level inside the ConfigPatch. The patch stays pure and the engine runs it in its
    transaction, the way `validatedSpec` works for mental models.
  - ResetBankConfig isn't validated. It only unsets fields, so it can't add an invalid value, and it must
    stay able to repair a stored config.
- **Request-level check under an implicit mask.** The per-slot merge collapses a slot that the request
  binds twice, so checking only the patched result would accept `{models:[REFLECT, REFLECT]}`. With an
  implicit mask, the request level is therefore also checked, before the patch is built. That covers the
  config, and the spec's overrides with `RPCMentalModelSpec`.
  - With an explicit mask the list is copied as sent, so the patched-result check sees any duplicate.
- RPCBankConfig allows every field, so this rejects only the slot rules and the disposition range. The
  server defaults already pass it (`config.ServerDefaults` validates itself).

## Alternatives
- **Exporting `config.upsertSlot`.** Not owned by this WP, and Merge is equivalent.
- **Always validating the request level, even with an explicit mask.** That would reject an invalid
  field the mask ignores. Rejected as surprising; the patched-result check is what protects storage.
- **Validating in the bank / tenant engines.** That would duplicate the server's validator wiring.
  Card WP-21 places the check in the ConfigPatch.

## Spec lines involved
SPEC "Model routing" (~line 80), the BankConfig comment (~line 242: "models: merged per slot. Other lists:
set replaces"), decision 604 and decision 401.
