-- DirectiveUpsert creates a directive or replaces its name, rules, tags and active flag.
-- name: DirectiveUpsert :exec
INSERT INTO directives (tenant_id, bank_id, id, name, rules, tags, active)
VALUES (@tenant_id, @bank_id, @id, @name, @rules, @tags, @active)
ON CONFLICT (tenant_id, bank_id, id) DO UPDATE
SET name = EXCLUDED.name, rules = EXCLUDED.rules, tags = EXCLUDED.tags, active = EXCLUDED.active,
    updated_at = now();

-- DirectiveList returns a bank's directives (only active ones when active_only), oldest first.
-- name: DirectiveList :many
SELECT * FROM directives
WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND (NOT @active_only::boolean OR active)
ORDER BY created_at, id;

-- DirectiveDelete removes one directive.
-- name: DirectiveDelete :execrows
DELETE FROM directives WHERE tenant_id = @tenant_id AND bank_id = @bank_id AND id = @id;
