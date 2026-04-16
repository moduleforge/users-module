# users-module — TODO

Status legend: `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked

- [x] **Phase 1 — Foundation** (depends on: none)
  - [x] 1.1 Monorepo skeleton + workspaces (merged deef6bc)
  - [x] 1.2 Make orchestration + GNU make guard (merged 2c3c39c)
  - [x] 1.3 Local docker-compose + Authelia + MailHog (merged fee32b4) — live Docker test pending restart
  - [x] 1.4 Config loader + OTel bootstrap (merged 77d3485)
- [x] **Phase 2 — Data model** (depends on: 1)
  - [x] 2.1 Atlas + sqlc setup
  - [x] 2.2 entities + legal_entities + natural_persons + corporations + service_accounts migrations
  - [x] 2.3 users migration with trigger and UNIQUE auth index
  - [x] 2.4 auth_local + email_codes + password_resets migrations
  - [x] 2.5 apps + apps_users migrations
  - [x] 2.6 audit_log migration + helpers
  - [x] 2.7 sqlc query files per concept
- [ ] **Phase 3 — API core** (depends on: 2)
  - [ ] 3.1 Service skeleton (chi, pgx pool with size guards, slog, OTel, graceful shutdown)
  - [ ] 3.2 ClaimMapper interface + provider implementations
  - [ ] 3.3 OIDC middleware, Principal-on-context, role mapping
  - [ ] 3.4 `/healthz`, `/readyz`, `/v1/self` GET/PUT
  - [ ] 3.5 Audit-log writer hooked into mutation handlers
- [ ] **Phase 4 — Local auth** (depends on: 3)
  - [ ] 4.1 Password (argon2id) registration + login → JWT
  - [ ] 4.2 Email-code request + verify (5-min TTL)
  - [ ] 4.3 Forgot-password flow
  - [ ] 4.4 Account-linking by verified email (local ↔ OIDC, OIDC ↔ OIDC)
- [ ] **Phase 5 — User management** (depends on: 4)
  - [ ] 5.1 Admin user CRUD (`POST/GET/PUT/DELETE /v1/users[/:uuid]`)
  - [ ] 5.2 User search (`GET /v1/users?q=…&email=…`)
  - [ ] 5.3 Admin grant/revoke
  - [ ] 5.4 Assume identity (`POST /v1/users/:uuid/assume`)
  - [ ] 5.5 Audit endpoints (`/v1/users/:uuid/audit`, `/v1/audit/:object_uuid`)
- [ ] **Phase 6 — Multi-tenancy** (depends on: 5)
  - [ ] 6.1 Apps CRUD (`/v1/apps`)
  - [ ] 6.2 Apps_users assignment (`/v1/apps/:uuid/users`)
  - [ ] 6.3 Per-request app context (`X-App: <uuid>` header → fallback to default)
  - [ ] 6.4 Authorization scoping (admin global; user limited to own data within app)
- [ ] **Phase 7 — GUI** (depends on: 5; can run in parallel with 6 from 6.x onward)
  - [ ] 7.1 Next.js 15 app shell + auth context
  - [ ] 7.2 Login (local + OIDC) + signup + forgot-password screens
  - [ ] 7.3 Profile view/edit
  - [ ] 7.4 Admin: user search + detail + edit + grant/revoke + assume
  - [ ] 7.5 Admin: audit log viewer (per-user, per-object)
  - [ ] 7.6 Admin: apps + apps_users management
- [ ] **Phase 8 — Deploy + CI** (depends on: 3 minimally; can run in parallel with 5–7)
  - [ ] 8.1 docker-compose dev loop validated **[local — must work end-to-end]**
  - [ ] 8.2 ko image build + cosign signing **[local build/sign; no live registry push]**
  - [ ] 8.3 Cloud Run example (env-only differences) **[draft only — defer live deploy]**
  - [ ] 8.4 Kustomize base (Postgres via CNPG, ingress, OIDC config) **[draft only; optional kind smoke-test]**
  - [ ] 8.5 GitHub Actions: lint, test, migrate-check, image build, contract test **[YAML drafted + actionlint clean]**
  - [ ] 8.6 OpenAPI contract + codegen verification **[local]**

> v1 access constraint: implementer has docker but no AWS/GCP/cluster. See `phase.8.deploy-ci.md` for the executable vs. draft-only split.

## Reports

Drop progress notes into `report.<N>.<topic>.md` files at the plan root as work proceeds.
