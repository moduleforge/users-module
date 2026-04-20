# users-module — Plan Summary

## Goal

Build a self-contained, reusable user-management module with three sub-projects under `users-module/`:

- `model/` — Postgres 16 schema, Atlas migrations, sqlc-generated Go queries
- `api/` — Go 1.23+ service (chi, pgx/v5, slog, OpenTelemetry)
- `gui/` — Next.js 15 (App Router, React 19, TypeScript strict, Tailwind + shadcn/ui)
- `deploy/` — local (docker-compose), serverless (Cloud Run/App Runner/Fly), kubernetes (Kustomize + CNPG)

Cloud-agnostic; same binary/image runs in all three deploy modes. Drives parity through configuration, not code branches.

## Dependencies on core-module

As of the core-module extraction (see `../../core-module/plan/summary.md`):

- **`model/`** — composes migrations at build time via `make compose`: copies `core-module/model/migrations/0000–0005` + users-module's own `0100+` into a single flat `schema/migrations/` dir (gitignored). sqlc reads the composed schema but only emits types for users-module-owned tables (`omit_unused_structs: true`). Entity/legal_entities/natural_persons/corporations/service_accounts types come from `github.com/moduleforge/core-model/db`.
- **`api/`** — requires `github.com/moduleforge/core-api`. Mounts `corehttpapi.NewRouter(…)` under `/v1`, serving `/v1/entities/*` including `/v1/entities/self`. users-module's admin user-create opens its own pgx tx and delegates the entity chain to `coreSvcs.NaturalPerson.Create(ctx, coredb.New(tx), principal, input)`, then inserts the users + auth_local rows in the same tx. Principal identity is bridged via `auth.CorePrincipalAdapter`; audit writes go through the core services to users-module's `audit.Writer` (which satisfies `core-api/audit.Writer` structurally).
- **`gui/`** — consumes `@moduleforge/core-gui` via yalc (`.yalc/@moduleforge/core-gui`). The profile page is a thin mount point around `<ProfileEditor>`; shadcn primitives `Button`, `Input`, `Label`, `Card`, `Badge`, `Alert` are imported from core-gui. Dialog/Separator/Switch/Table primitives remain local. Tailwind's `@source` directive in `globals.css` picks up core-gui's compiled `dist/`.

Local dev: top-level `go.work` at `user-components/` stitches Go modules; `make link-core` from the repo root rebuilds core-gui and refreshes the yalc link.

## End-user features (from CLAUDE.md)

- Self-service account creation, profile edit, "forgot password"
- Authentication via email+password, email magic-code (5 min), Google OIDC, Microsoft OIDC (optional)
- Account matching by email across auth methods
- Admin: create users, grant admin, edit any profile, assume identity, search, view audit log
- Per-object change history; per-user activity history
- First account → root admin

## Architectural pillars (settled)

- **Domain model: class-table inheritance.** `entities` (root) → `legal_entities` → (`natural_persons` | `corporations`) and `service_accounts` (parallel). `users` is a role extension referencing any leaf entity (enforced by `BEFORE INSERT/UPDATE` trigger). Foreign keys point only upward.
- **Auth: local + pluggable OIDC.** `auth_local` table for password (argon2id) and email-code credentials; `users.auth_issuer + auth_id` (UNIQUE compound) for OIDC identities. A user can hold both. Account linking on verified email.
- **Pluggable OIDC via ClaimMapper.** `api/internal/auth/claims.go` defines a `ClaimMapper` interface; one implementation per provider style (google, microsoft, keycloak, cognito, auth0, authelia, generic-with-jsonpath). Selected per provider via `AUTH_PROVIDER_<ID>_CLAIM_STYLE` (Phase 9 migrated this from the single `OIDC_CLAIM_STYLE`). All mappers output a uniform `Principal{subject, issuer, email, roles}`.
- **Multi-tenant from day 1.** `apps` and `apps_users` tables; `users.default_app_id`. Authorization scopes by app context; admin endpoints/UI to manage apps and assignments. Admin role is global; per-app roles live on `apps_users`.
- **Vanilla SQL only.** No Postgres-only types in the schema (TEXT + CHECK over native enums). Atlas declarative migrations. sqlc for type-safe Go queries.
- **External identifiers are UUID.** Internal IDs are `BIGINT` from sequences, never serialized in responses.
- **Soft delete + audit.** `entities.archived_at`; an `audit_log` table records every mutating operation (actor, target entity, op, before/after JSON, timestamp).
- **REST API** under `/v1`. `Authorization: Bearer <jwt>` header (CLAUDE.md uses "Accept" loosely; we use `Authorization` since that is the HTTP-correct, secure, simplest option).

## Settled prior decisions (do not re-litigate)

- Restart all three legacy components; legacy code is reference only.
- Monorepo named `users-module`; sub-projects are `model`, `api`, `gui`, `deploy`.
- Make is the task orchestrator; targets per `feedback_make_conventions` memory.
- Connection pool defaults: `MaxConns=4` (serverless), `20` (local/k8s); `MaxConnLifetime=5m`, `MaxConnIdleTime=1m`.
- Graceful shutdown: SIGTERM/SIGINT → 25s timeout → drain HTTP, close pool, flush OTel.
- GNU make version guard in root Makefile (BSD make on macOS fails dot-namespaced targets).

## Owner decisions for v1 (settled this session)

- **Omit `legal_id`** (SSN/EIN/passport) on natural_persons and corporations.
- **Multi-tenant from day 1** (data model + admin UI + admin API).
- **Local + OIDC auth**: support both classic credentials and pluggable OIDC.

## Phases

1. **Foundation** — monorepo skeleton (Make, workspaces, docker-compose, OpenTelemetry, env loading).
2. **Data model** — full SQL schema, Atlas migrations, triggers, sqlc setup.
3. **API core** — Go service skeleton, pgx pool, chi router, OIDC middleware + ClaimMapper, `/self`, health, audit hook.
4. **Local auth** — password (argon2id), email-code, forgot-password, account creation, account linking by verified email.
5. **User management** — user CRUD, search, profile edit, admin grant, assume identity, audit/history endpoints.
6. **Multi-tenancy** — apps CRUD, apps_users assignment, default_context, per-request scoping, admin endpoints.
7. **GUI** — Next.js 15: login, signup, profile, admin user mgmt, search, audit, assume, apps mgmt.
8. **Deploy + CI** — docker-compose (must work locally), ko image build (local only — no live push), OpenAPI codegen, GitHub Actions YAML (lint-validated). Cloud Run + Kustomize ship as **complete drafts** only — the implementer has docker but no AWS/GCP/cluster access for v1.

## Documents

- `TODO.md` — phase/task checklist, the live status board.
- `phase.<N>.<title>/phase.<N>.<title>.md` — phase notes.
- `phase.<N>.<title>/phase.<N>.task.<M>.<title>.md` — agent-ready task instructions.
