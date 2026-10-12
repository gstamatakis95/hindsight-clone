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
