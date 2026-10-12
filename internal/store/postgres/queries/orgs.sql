-- OrgLockLive takes FOR KEY SHARE on the org row and returns its tombstone (NULL = live); no row = unknown org.
-- Bank creation runs it first: it never waits on an org config update (FOR NO KEY UPDATE), but it waits for an
-- org delete that holds the row FOR UPDATE, and then sees its tombstone.
-- name: OrgLockLive :one
SELECT deleting_at FROM orgs WHERE tenant_id = @tenant_id AND id = @id FOR KEY SHARE;

-- OrgGet returns one org, tombstoned or not.
-- name: OrgGet :one
SELECT * FROM orgs WHERE tenant_id = @tenant_id AND id = @id;

-- OrgCreate inserts a new org; an existing id is a unique violation.
-- name: OrgCreate :exec
INSERT INTO orgs (tenant_id, id, name, config) VALUES (@tenant_id, @id, @name, @config);

-- OrgSetConfig replaces a live org's config; a tombstoned or unknown org matches nothing.
-- name: OrgSetConfig :execrows
UPDATE orgs SET config = @config, updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id AND deleting_at IS NULL;

-- OrgConfigLock serializes read-modify-writes of one org's config until the tx ends.
-- name: OrgConfigLock :exec
SELECT pg_advisory_xact_lock(hashtextextended('org_config/' || @tenant_id::text || '/' || @org_id::text, 0));

-- OrgList returns one keyset page of a tenant's live orgs (tombstoned ones left out) whose id starts with
-- id_prefix, in byte order of id, after after_id ("" = from the start).
-- name: OrgList :many
SELECT * FROM orgs
WHERE tenant_id = @tenant_id AND deleting_at IS NULL
  AND starts_with(id, @id_prefix::text)
  AND id COLLATE "C" > @after_id::text
ORDER BY id COLLATE "C"
LIMIT @lim::int;

-- OrgLockForDelete locks the org row FOR UPDATE and returns it: it waits for bank creations in flight (FOR KEY SHARE)
-- and makes new ones wait, so an org delete runs it first.
-- name: OrgLockForDelete :one
SELECT * FROM orgs WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- OrgMarkDeleting tombstones an org; marking it again keeps the first time.
-- name: OrgMarkDeleting :execrows
UPDATE orgs SET deleting_at = COALESCE(deleting_at, @at::timestamptz), updated_at = now()
WHERE tenant_id = @tenant_id AND id = @id;

-- OrgMarkBanksDeleting tombstones every live bank of an org. It runs as its own statement after OrgLockForDelete,
-- so under READ COMMITTED it sees every bank committed while that lock waited.
-- name: OrgMarkBanksDeleting :execrows
UPDATE banks SET deleting_at = @at::timestamptz, updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND deleting_at IS NULL;

-- OrgBanksAwaitingDelete returns, in byte order, up to lim tombstoned banks of an org with no pending or processing
-- delete_bank operation.
-- name: OrgBanksAwaitingDelete :many
SELECT b.id FROM banks b
WHERE b.tenant_id = @tenant_id AND b.org_id = @org_id AND b.deleting_at IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM operations o
    WHERE o.tenant_id = b.tenant_id AND o.org_id = b.org_id AND o.bank_id = b.id AND o.kind = 'delete_bank'
      AND o.status IN ('pending', 'processing'))
ORDER BY b.id COLLATE "C"
LIMIT @lim::int;

-- OrgCountBanks counts an org's bank rows, tombstoned or not.
-- name: OrgCountBanks :one
SELECT count(*) FROM banks WHERE tenant_id = @tenant_id AND org_id = @org_id;

-- OrgDeleteEvents deletes every lifecycle event of an org's banks.
-- name: OrgDeleteEvents :exec
DELETE FROM lifecycle_events WHERE tenant_id = @tenant_id AND org_id = @org_id;

-- OrgDeleteOperations deletes every operation of an org, its banks' and its own.
-- name: OrgDeleteOperations :exec
DELETE FROM operations WHERE tenant_id = @tenant_id AND org_id = @org_id;

-- OrgDeleteRow deletes a tombstoned org's row; a live org matches nothing, and a bank left in it fails the foreign
-- key (RESTRICT).
-- name: OrgDeleteRow :execrows
DELETE FROM orgs WHERE tenant_id = @tenant_id AND id = @id AND deleting_at IS NOT NULL;

-- OrgTablesWithOrgID lists, in name order, the base tables of the current schema that have an org_id column, orgs
-- excluded: the tables an org delete verifies are empty for the org.
-- name: OrgTablesWithOrgID :many
SELECT c.table_name::text AS table_name
FROM information_schema.columns c
JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE c.table_schema = current_schema() AND c.column_name = 'org_id' AND t.table_type = 'BASE TABLE'
  AND c.table_name <> 'orgs'
ORDER BY c.table_name;
