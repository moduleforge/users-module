# model

Postgres 16 schema, Atlas versioned migrations, and sqlc-generated Go queries
for the users-module.

## Layout

- `migrations/` — Atlas versioned migration files (`.sql`)
- `queries/` — sqlc query files (`.sql`), one per concept
- `internal/db/` — sqlc-generated Go code (do not edit)
- `scripts/` — operational SQL scripts (e.g., `relink_auth.sql`)
- `atlas.hcl` — Atlas environment configuration
- `sqlc.yaml` — sqlc v2 configuration

## Prerequisites

- [Atlas CLI](https://atlasgo.io) — `curl -sSf https://atlasgo.sh | sh`
- [sqlc](https://docs.sqlc.dev) — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0`
- Running Postgres instance (local: `docker compose up -d` from `deploy/local/`)

## Postgres dependency

The schema requires the `pgcrypto` extension for `gen_random_uuid()`. This is
the only Postgres-specific dependency; all DDL otherwise uses vanilla SQL.

## Make targets

```
make build            # alias for gen
make gen              # generate Go from sqlc queries
make verify           # atlas validate + sqlc compile
make migrate.new NAME=foo  # create a new migration file
make migrate.up       # apply pending migrations
make migrate.status   # show migration status
make migrate.hash     # recalculate migration integrity hash
make test.integration # apply migrations against DATABASE_URL
make lint             # atlas migrate lint (latest migration)
make clean            # remove generated Go code
```

All targets default `DATABASE_URL` to `postgresql://users:users@localhost:5432/users?sslmode=disable`.
From the ai-sandbox environment, use `host.docker.internal` instead of `localhost`.
