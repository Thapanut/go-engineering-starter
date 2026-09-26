# Migrations

- Naming follows golang-migrate: `NNNN_short_name.up.sql` / `NNNN_short_name.down.sql`. Every `up` has a reversible `down`.
- Released migrations are immutable; change schema with a new migration.
- `dev/` holds local-only SQL (test database, synthetic seed). Never run it in production.
