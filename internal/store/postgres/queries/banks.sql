-- BumpBankSeq increments a live bank's change_seq and returns it; the row lock is held until commit.
-- name: BumpBankSeq :one
UPDATE banks
SET change_seq = change_seq + 1, last_memory_write_at = now(), updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND deleting_at IS NULL
RETURNING change_seq;

-- GetBank returns one bank, tombstoned or not.
-- name: GetBank :one
SELECT * FROM banks WHERE tenant_id = $1 AND id = $2;

-- ListBanks returns one keyset page of a tenant's live banks (tombstoned ones left out) whose id starts with
-- id_prefix, in byte order of id, after after_id ("" = from the start).
-- name: ListBanks :many
SELECT * FROM banks
WHERE tenant_id = @tenant_id AND deleting_at IS NULL
  AND starts_with(id, @id_prefix::text)
  AND id COLLATE "C" > @after_id::text
ORDER BY id COLLATE "C"
LIMIT @lim::int;

-- BankChangeSeq returns the bank's latest committed change_seq.
-- name: BankChangeSeq :one
SELECT change_seq FROM banks WHERE tenant_id = $1 AND id = $2;

-- InsertBankIfMissing creates a bank with empty overrides unless it exists.
-- name: InsertBankIfMissing :exec
INSERT INTO banks (tenant_id, id) VALUES ($1, $2) ON CONFLICT (tenant_id, id) DO NOTHING;

-- CreateBank inserts a new bank with its name and config.
-- name: CreateBank :exec
INSERT INTO banks (tenant_id, id, name, config) VALUES ($1, $2, $3, $4);

-- UpsertBankConfig creates the bank if needed and replaces its overrides; a tombstoned bank matches nothing.
-- name: UpsertBankConfig :execrows
INSERT INTO banks AS b (tenant_id, id, config) VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, id) DO UPDATE SET config = EXCLUDED.config, updated_at = now()
WHERE b.deleting_at IS NULL;

-- MarkBankDeleting sets deleting_at unless already set.
-- name: MarkBankDeleting :execrows
UPDATE banks SET deleting_at = COALESCE(deleting_at, @at::timestamptz), updated_at = now()
WHERE tenant_id = $1 AND id = $2;

-- GetTenantConfig returns the tenant-level config.
-- name: GetTenantConfig :one
SELECT config FROM tenants WHERE id = $1;

-- TenantConfigLock serializes read-modify-writes of one tenant's config until the tx ends; it needs no
-- tenants row, so a first-ever write is covered too.
-- name: TenantConfigLock :exec
SELECT pg_advisory_xact_lock(hashtextextended('tenant_config/' || @tenant_id::text, 0));

-- UpsertTenantConfig replaces the tenant-level config.
-- name: UpsertTenantConfig :exec
INSERT INTO tenants (id, config) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET config = EXCLUDED.config, updated_at = now();
