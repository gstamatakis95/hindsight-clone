#!/usr/bin/env bash
# Fail if schema/schema.sql is out of date with the migrations.
#
# Creates a scratch database in the compose postgres container, runs `alembic upgrade head`
# on it from empty, dumps it with schema-dump.sh and diffs the result against the committed
# schema/schema.sql. Starting from empty means an edit to an already-applied revision is
# caught too. The scratch database is dropped on exit; the dev database is not touched.
# Exit 0 = up to date, 1 = diff. Fix a diff with `make migrate` and commit schema/schema.sql.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ALEMBIC="${ALEMBIC:-$ROOT/.venv/bin/alembic}"
COMPOSE=(docker compose -f "$ROOT/docker-compose.yml")
DB_USER="${MEMD_PG_USER:-memd}"
DB_PASSWORD="${MEMD_PG_PASSWORD:-memd}"
PORT="${MEMD_PG_PORT:-55432}"
SCRATCH_DB="memd_schema_check_$$"

psql_admin() {
  "${COMPOSE[@]}" exec -T postgres psql -q -v ON_ERROR_STOP=1 --username="$DB_USER" --dbname=postgres -c "$1"
}

tmp="$(mktemp)"
cleanup() {
  rm -f "$tmp"
  psql_admin "DROP DATABASE IF EXISTS $SCRATCH_DB WITH (FORCE)" >/dev/null 2>&1 || true
}
trap cleanup EXIT

psql_admin "CREATE DATABASE $SCRATCH_DB TEMPLATE template0"
MEMD_DSN="postgresql+psycopg://$DB_USER:$DB_PASSWORD@localhost:$PORT/$SCRATCH_DB" \
  "$ALEMBIC" -c "$ROOT/migrations/alembic.ini" upgrade head
MEMD_PG_DB="$SCRATCH_DB" "$ROOT/scripts/schema-dump.sh" "$tmp"

if ! diff -u "$ROOT/schema/schema.sql" "$tmp"; then
  echo "schema/schema.sql is stale: run 'make migrate' and commit the result." >&2
  exit 1
fi
echo "schema/schema.sql is up to date."
