# migrations/: rules for coding agents

Alembic owns memd's Postgres schema. Go never runs DDL.

- **One baseline until the first release.** Until the first release tag there is exactly one
  revision, `versions/0001_baseline.py`; edit it in place and run `make migrate` (rebuild the dev
  database first, see README.md). After the release the baseline is frozen and every schema
  change is a new revision `NNNN_<slug>.py` with `revision = "NNNN"` and `down_revision` set to
  the previous one.
- **Raw SQL only**, through `op.execute()`. No SQLAlchemy models, no `op.create_table`, no
  autogenerate (`target_metadata` is None).
- **Keep the baseline readable.** Tables are grouped by area, each followed by its indexes; put
  a `--` comment on any column or index whose purpose is not obvious from its name. A new table
  also goes in `TABLES`, which `downgrade()` drops in reverse.
- **`downgrade()` fully reverses `upgrade()`**: every table, index, constraint and extension it
  created. Use plain `CREATE TABLE` / `CREATE INDEX` (no `IF NOT EXISTS`, except for
  extensions), so a missed drop fails the next upgrade loudly.
- **Docstrings:** every revision has a module docstring, and `upgrade()` and `downgrade()`
  each have one (ruff `D` rules enforce this).
- **`tenant_id text NOT NULL` on every table.** Follow docs/decisions/004-schema-conventions.md
  for ids, types, CHECKs and indexes.
- **Regenerate `schema/schema.sql`** with `make migrate` after any change, then `make gen`
  (sqlc reads the dump), and commit all three. `make migrate-check` must pass (the CI diff check).
- **Verify** before finishing: `make downgrade && make migrate`, then `make migrate-check`,
  `make ruff` and `make gen`.
- Never touch the compose image tag without following docs/decisions/003-postgres-image.md.
