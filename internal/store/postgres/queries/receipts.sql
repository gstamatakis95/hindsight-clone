-- ReceiptCreate inserts a deletion receipt.
-- name: ReceiptCreate :exec
INSERT INTO deletion_receipts (tenant_id, id, org_id_sha256, status, requested_at, banks, server_version)
VALUES (@tenant_id, @id, @org_id_sha256, @status, @requested_at, @banks, @server_version);

-- ReceiptGet returns one receipt of a tenant.
-- name: ReceiptGet :one
SELECT * FROM deletion_receipts WHERE tenant_id = @tenant_id AND id = @id;

-- ReceiptLatest returns an org's most recently requested receipt, found by the org id hash.
-- name: ReceiptLatest :one
SELECT * FROM deletion_receipts WHERE tenant_id = @tenant_id AND org_id_sha256 = @org_id_sha256
ORDER BY requested_at DESC, id DESC
LIMIT 1;

-- ReceiptUpdate replaces a receipt's status, completion time, verification and server version.
-- name: ReceiptUpdate :execrows
UPDATE deletion_receipts
SET status = @status, completed_at = sqlc.narg(completed_at)::timestamptz, verification = @verification,
    server_version = @server_version
WHERE tenant_id = @tenant_id AND id = @id;

-- ReceiptPrune deletes the receipts completed before the cutoff.
-- name: ReceiptPrune :execrows
DELETE FROM deletion_receipts WHERE completed_at < @before::timestamptz;
