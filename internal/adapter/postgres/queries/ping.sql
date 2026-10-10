-- Placeholder: sqlc refuses to run with an empty queries/ directory. WP-01 adds the real
-- queries next to this file; delete it then if nothing uses Ping.

-- name: Ping :one
SELECT 1::integer AS ok;
