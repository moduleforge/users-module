# deploy/local

Local development stack for the **users-module** project. Brings up real
infrastructure (Postgres, Authelia OIDC, Mailpit SMTP catcher) on a shared
Docker network so dev/prod parity is preserved — no mock OIDC.

## First-time setup

**Add a `/etc/hosts` entry for `authelia` before your first `dev.start`:**

```
127.0.0.1  authelia
```

(Use `sudo` to edit `/etc/hosts`.)

Why: the OIDC issuer URL is `https://authelia:9091` so that the API container
(on the docker network) and your browser (on the host) both see the **same**
issuer string — a hard OIDC requirement (the issuer in the discovery document
must match the URL the client used). Inside the docker network `authelia`
resolves via docker DNS. From your browser it does not, so without this
`/etc/hosts` entry the "Sign in with Authelia" button takes you to a URL your
browser can't reach. The Authelia TLS cert already has `DNS:authelia` as a SAN,
so it validates correctly from both sides once the name resolves.

The API and local email/password login work without this entry; only the
Authelia SSO button in the GUI needs it.

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
| `mailpit`  | `axllent/mailpit:latest`    | `1025` (SMTP), `8025` (UI) | Captures outbound mail (email-code auth flow). Multi-arch (amd64/arm64). |

All services join the `users-module-net` bridge network and resolve each other
by service name (e.g. Authelia talks to Mailpit at `mailpit:1025`).

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

- **client_id**: `users-module`
- **client_secret**: `local-authelia-secret` (placeholder; the
  pbkdf2-sha512 hash is committed in `authelia/configuration.yml`. Regenerate
  with `docker run --rm authelia/authelia:4.38 authelia crypto hash generate
  pbkdf2 --variant sha512 --password 'your-real-secret'` and update both the
  config and the `AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET` value in `.env`.)
- **scopes**: `openid profile email groups`
- **redirect URI**:
  - `http://localhost:8080/v1/auth/oidc/authelia/callback` (API; per-provider
    callback path introduced in Phase 9)

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

## Mailpit

Web UI: <http://localhost:8025>. SMTP listener on `localhost:1025`. Authelia
is wired to send password-reset / OTP emails there; the API also uses the same
SMTP endpoint for the email-code auth flow.

Mailpit replaces MailHog (unmaintained since 2020, amd64-only) with a
multi-arch successor. SMTP surface is unchanged; the web UI differs
cosmetically but exposes the same "received mail" listing.

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
