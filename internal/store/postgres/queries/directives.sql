-- DirectiveCreate inserts a directive; a taken id or name in the bank is a unique violation.
-- name: DirectiveCreate :exec
INSERT INTO directives (tenant_id, org_id, bank_id, id, name, rules, tags, active)
VALUES (@tenant_id, @org_id, @bank_id, @id, @name, @rules, @tags, @active);

-- DirectiveUpdate replaces a directive's name, rules, tags and active flag, keeping created_at; a taken name
-- is a unique violation.
-- name: DirectiveUpdate :execrows
UPDATE directives
SET name = @name, rules = @rules, tags = @tags, active = @active, updated_at = now()
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id;

-- DirectiveGet returns one directive, locked for update until the transaction ends.
-- name: DirectiveGet :one
SELECT * FROM directives WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id FOR UPDATE;

-- DirectiveList returns a bank's directives (only active ones when active_only), oldest first.
-- name: DirectiveList :many
SELECT * FROM directives
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND (NOT @active_only::boolean OR active)
ORDER BY created_at, id;

-- DirectivePage returns one keyset page of a bank's directives, oldest first (created_at, then id in byte
-- order), after the (after_created_at, after_id) position when set, only active ones when active_only, and
-- filtered by tags under tags_match with the listing semantics of ReadsListDocuments (no filter_tags = no
-- filter).
-- name: DirectivePage :many
SELECT * FROM directives
WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND (NOT @active_only::boolean OR active)
  AND (CASE @tags_match::text
      WHEN 'exact' THEN tags @> @filter_tags::text[] AND tags <@ @filter_tags::text[]
      WHEN 'any_strict' THEN cardinality(@filter_tags::text[]) = 0 OR tags && @filter_tags::text[]
      WHEN 'all_strict' THEN tags @> @filter_tags::text[]
      WHEN 'all' THEN cardinality(@filter_tags::text[]) = 0 OR cardinality(tags) = 0 OR tags @> @filter_tags::text[]
      ELSE cardinality(@filter_tags::text[]) = 0 OR cardinality(tags) = 0 OR tags && @filter_tags::text[]
  END)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
      OR created_at > sqlc.narg(after_created_at)::timestamptz
      OR (created_at = sqlc.narg(after_created_at)::timestamptz AND id COLLATE "C" > @after_id::text))
ORDER BY created_at, id COLLATE "C"
LIMIT @lim::int;

-- DirectiveDelete removes one directive.
-- name: DirectiveDelete :execrows
DELETE FROM directives WHERE tenant_id = @tenant_id AND org_id = @org_id AND bank_id = @bank_id AND id = @id;
