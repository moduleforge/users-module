# deploy/local

Local development stack for the **users-module** project. Brings up real
infrastructure (Postgres, Authelia OIDC, MailHog SMTP catcher) on a shared
Docker network so dev/prod parity is preserved — no mock OIDC.

## Quick start

```sh
# from the users-module repo root, copy the env template:
cp .env.example .env
# edit .env if you want non-default secrets (the defaults are fine for local dev)

cd deploy/local
docker compose up -d
docker compose ps        # all three services should report "healthy"
```

Tear down (and drop the Postgres volume):

```sh
docker compose down -v
```

## Services

| Service    | Image                       | Host port(s)        | Purpose                              |
|------------|-----------------------------|---------------------|--------------------------------------|
| `postgres` | `postgres:16-alpine`        | `5432`              | App data store                       |
| `authelia` | `authelia/authelia:4.38`    | `9091`              | OIDC provider (real, not mocked)     |
| `mailhog`  | `mailhog/mailhog:v1.0.1`    | `1025` (SMTP), `8025` (UI) | Captures outbound mail (email-code auth flow) |

All services join the `users-module-net` bridge network and resolve each other
by service name (e.g. Authelia talks to MailHog at `mailhog:1025`).

## Seeded users

Two users are seeded into Authelia's file-backed user database
(`authelia/users_database.yml`):

| Username | Email                  | Password         | Groups          |
|----------|------------------------|------------------|-----------------|
| `admin`  | `admin@example.test`   | `admin-password` | `admins`, `users` |
| `user`   | `user@example.test`    | `user-password`  | `users`         |

These passwords are **dev-only**; never reuse them anywhere.

To regenerate a hash for a different password (uses argon2id, matching the
`authentication_backend.file.password` parameters in `configuration.yml`):

```sh
docker run --rm authelia/authelia:4.38 \
  authelia crypto hash generate argon2 --password 'yourpass'
```

The legacy/simpler invocation also works on older Authelia builds:

```sh
docker run --rm authelia/authelia:latest authelia hash-password 'yourpass'
```

Paste the resulting `$argon2id$...` string into the `password:` field of the
appropriate user in `authelia/users_database.yml`, then `docker compose
restart authelia`.

## OIDC client

Authelia is preconfigured with a single OIDC client for this project:

- **client_id**: `users-api`
- **client_secret**: `change-me-users-api-client-secret` (placeholder; the
  pbkdf2-sha512 hash is committed in `authelia/configuration.yml`. Regenerate
  with `docker run --rm authelia/authelia:4.38 authelia crypto hash generate
  pbkdf2 --variant sha512 --password 'your-real-secret'` and update both the
  config and your API/GUI `.env`.)
- **scopes**: `openid profile email groups`
- **redirect URIs**:
  - `http://localhost:8080/v1/auth/oidc/callback` (API)
  - `http://localhost:3000/api/auth/oidc/callback` (GUI)

OIDC discovery document:

```sh
curl -s http://localhost:9091/.well-known/openid-configuration | jq
```

## OIDC issuer key

The OIDC signing key (4096-bit RSA) is inlined in the `jwks` block of
`authelia/configuration.yml`. It is committed only because this is a local dev
stack -- **regenerate it for any non-local use**:

```sh
openssl genrsa 4096
```

Paste the output into the `jwks[0].key` field in `authelia/configuration.yml`.

## Postgres access

Default credentials (override in `.env`):

```sh
psql postgresql://users:users@localhost:5432/users -c 'SELECT 1'
```

Or via the running container:

```sh
docker compose exec postgres psql -U users -d users -c 'SELECT 1'
```

## MailHog

Web UI: <http://localhost:8025>. SMTP listener on `localhost:1025`. Authelia
is wired to send password-reset / OTP emails there; the API will use the same
SMTP endpoint for the email-code auth flow once it lands (Phase 3+).

## What's *not* here

- API and GUI containers — they run on the host during development (Phase 3+
  may add them to compose).
- Production / k8s manifests — see `deploy/k8s/` and Phase 8.
- Make targets to drive this stack — see Task 1.2 (`make dev.start`, etc.).

## Troubleshooting

- **Authelia keeps restarting**: check `docker compose logs authelia`. Most
  often it's a missing/short secret env var (each must be >= 32 chars) or a
  syntax issue in `users_database.yml`.
- **Postgres healthcheck never goes healthy**: confirm the host port `5432`
  isn't already taken (`lsof -i :5432`) and that `POSTGRES_USER`/`POSTGRES_DB`
  match between `.env` and the healthcheck.
- **OIDC discovery 404**: Authelia takes ~10s after the container starts
  before the OIDC endpoints respond. Wait for `docker compose ps` to show
  `healthy`, then retry.
