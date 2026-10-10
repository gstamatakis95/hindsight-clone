#!/usr/bin/env bash
# Dump the migrated memd schema deterministically.
#
#   scripts/schema-dump.sh            # writes schema/schema.sql
#   scripts/schema-dump.sh OUT.sql    # writes OUT.sql instead
#
# pg_dump runs inside the compose postgres container, so client and server versions always
# match. The output is normalised so two dumps of the same schema are byte-identical:
#   - `\restrict <random key>` / `\unrestrict <random key>` lines (pg_dump >= 17.6) are removed;
#     they change on every run and are psql meta-commands sqlc cannot parse anyway.
#   - the "Dumped from/by database version" header comments are removed, so a patch-level
#     image bump alone does not produce a diff.
# The alembic_version table is excluded: it is migration bookkeeping, not schema.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/schema/schema.sql}"
COMPOSE=(docker compose -f "$ROOT/docker-compose.yml")
DB_USER="${MEMD_PG_USER:-memd}"
DB_NAME="${MEMD_PG_DB:-memd}"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

"${COMPOSE[@]}" exec -T postgres pg_dump \
  --username="$DB_USER" --dbname="$DB_NAME" \
  --schema-only --no-owner --no-privileges --no-comments --no-tablespaces \
  --exclude-table=public.alembic_version \
  >"$tmp"

mkdir -p "$(dirname "$OUT")"
sed -E \
  -e '/^\\(un)?restrict /d' \
  -e '/^-- Dumped (from|by) /d' \
  "$tmp" | cat -s >"$OUT"
