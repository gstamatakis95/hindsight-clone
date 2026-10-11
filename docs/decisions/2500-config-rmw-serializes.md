# 2500 — Config read-modify-writes serialize

Status: accepted (WP-25)

## Context
`UpdateBankConfig` (which also serves `ResetBankConfig`) and `UpdateTenantConfig` read the stored overrides,
apply the FieldMask patch in Go and write the whole level back. Nothing locked the read. Two concurrent
patches of different fields both succeeded, and the second write dropped the first one's field (a lost
update, reproduced by the WP-25 review).

## Decision
- **Bank level.** `UpdateBankConfig` runs `Ensure`, then `tx.Seq(b)`, then re-reads the bank with `Get`,
  and only then patches. This is the Seq-first rule that every other model-row read-modify-write follows.
  `Seq` locks the bank row. The re-read matters because under READ COMMITTED `Ensure`'s read can predate
  the lock. `ResetBankConfig` goes through `UpdateBankConfig(resetPatch)`, so it is covered too.
  - Side effect: a config write bumps `change_seq` and stamps `last_memory_write_at`. Staleness looks at
    in-scope unit seqs, not at `change_seq` alone, so no mental model goes stale over a config write.
- **Tenant level.** CCR WP-25-1 adds `BankRepo.LockTenantConfig`. In Postgres it is
  `pg_advisory_xact_lock(hashtextextended('tenant_config/' || tenant, 0))`; in the fake it is a no-op,
  because the fake already serializes transactions. `UpdateTenantConfig` takes the lock before
  `TenantConfig`.
  - An adapter-only fix was rejected. `TenantConfig` is on hot paths (the config resolver, retain ingest,
    the linker), so a `FOR UPDATE` read there would serialize every request of a tenant. A row lock also
    locks nothing before the tenant's first config write, when there is no row yet.

## Tests
`postgres.TestConcurrentBankConfigPatchesKeepBoth`, `postgres.TestConcurrentTenantConfigPatchesKeepBoth`,
`porttest:BanksTenantConfigLockSerializes`.
