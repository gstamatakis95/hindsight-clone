# migrations/: rules for coding agents

Alembic owns memd's Postgres schema. Go never runs DDL.

- **Frozen after WP-00.** Do not edit an existing revision. Schema changes need an approved
  CCR (change request) and then go in a new revision.
- **Raw SQL only**, through `op.execute()`. No SQLAlchemy models, no `op.create_table`, no
  autogenerate (`target_metadata` is None).
- **One concern per revision**, named `NNNN_<slug>.py` with `revision = "NNNN"` and
  `down_revision` set to the previous revision.
- **Always write `downgrade()`** and make it fully reverse `upgrade()`: drop every table,
  index, constraint and extension the upgrade created, in reverse order. Use plain
  `CREATE TABLE` / `CREATE INDEX` (no `IF NOT EXISTS`, except for extensions), so a missed
  drop fails the next upgrade loudly.
- **Docstrings:** every revision has a module docstring, and `upgrade()` and `downgrade()`
  each have one (ruff `D` rules enforce this).
- **`tenant_id text NOT NULL` on every table.** Follow docs/decisions/004-schema-conventions.md
  for ids, types, CHECKs and indexes.
- **Regenerate `schema/schema.sql`** with `make migrate` after any change and commit it.
  `make migrate-check` must pass (the CI diff check).
- **Verify** before finishing: `make migrate`, then `make downgrade` (to base) and `make migrate`
  again, then `make migrate-check`, `make ruff`, and `sqlc generate` in
  `internal/adapter/postgres`.
- Never touch the compose image tag without following docs/decisions/003-postgres-image.md.
