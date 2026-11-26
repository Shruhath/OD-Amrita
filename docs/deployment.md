# Self-hosting OD-Amrita

A generic guide to running OD-Amrita on your own infrastructure. It covers what
every deployment needs regardless of host: the API container, the web build,
PostgreSQL, an OpenID sign-in broker and a TLS front end. Every hostname below
is a placeholder; substitute your own.

For local development, see [HOWTO.md](../HOWTO.md).

> **Every password committed to this repository is a local-development
> placeholder** (`api/db/init/00_roles.sql`, `api/docker-compose.yml`,
> `api/Makefile`, `api/.env.example`, the CI workflows). Never use them on a
> database that anything else can reach. With `APP_ENV=production` the API
> refuses to start while either DSN still carries one of them.

## 1. What you are deploying

| Component | What it is | Notes |
|---|---|---|
| API | `api/cmd/api`, a Go (gin) HTTP server | Always-on container. Built with `api/Dockerfile` from the **repository root** (the build copies `go.work`, `api/`, `contracts/` and `test/`). Listens on `API_PORT` (default 8443). |
| Worker | `api/cmd/worker` | Optional. Consumes the domain event stream; with `KAFKA_BROKERS` unset it exits immediately. Same Dockerfile, `--build-arg ENTRYPOINT_BIN=worker`. |
| Web | `web/`, a React + Vite single-page app | Static files. `pnpm --filter od-amrita-web build` produces `web/dist`. The host must rewrite every non-asset path to `/index.html` (see `web/vercel.json` for the rewrite and the security headers to replicate). |
| Database | PostgreSQL 17 | Holds every record and the append-only `audit_log`. This is what you back up. |
| Sign-in broker | Supabase Auth, brokering Microsoft sign-in | Issues the session JWT the API verifies. Its own database holds only identities and sessions. |

The browser talks to the web origin and the API origin directly, so they are
normally two different origins and CORS must be configured (section 6).

## 2. Database roles and bootstrap

The application uses two login roles and never connects as a superuser:

- **`od_migrator`** owns the schema and every table and runs the migrations
  (`DATABASE_URL_MIGRATOR`). Only the migration step needs it.
- **`od_runtime`** is the application's pool (`DATABASE_URL_RUNTIME`). It has
  data access only: no `CREATE`, `ALTER` or `DROP`, and no `UPDATE`/`DELETE` on
  the audit log. This is what stops a leaked application credential from
  rewriting history.

Bootstrap them once, before the first migration, as the cluster superuser (on
managed Postgres, the database owner role your provider gives you):

```bash
# 1. Create the roles, hand the schema to od_migrator, install pg_trgm.
#    Idempotent; safe to re-run.
psql "$SUPERUSER_DSN" -f api/db/init/00_roles.sql

# 2. CHANGE BOTH PASSWORDS NOW. 00_roles.sql creates the roles with the public
#    placeholder passwords committed in that file. Do this before anything
#    else, not after the deploy.
psql "$SUPERUSER_DSN" <<'SQL'
ALTER ROLE od_migrator PASSWORD '<new unique migrator secret>';
ALTER ROLE od_runtime  PASSWORD '<new unique runtime secret>';
SQL
```

Also change the superuser password itself if your Postgres was created from the
Compose file or the Makefile, both of which use committed placeholders.

`00_roles.sql` creates the `pg_trgm` extension because installing an extension
needs a privilege `od_migrator` deliberately does not have. If you bootstrapped
before that line existed, the roster-search migration stops with a message
telling you to run `CREATE EXTENSION IF NOT EXISTS pg_trgm;` as the owner.

Keep passwords out of connection strings where you can: libpq and pgx read
`PGPASSWORD` or a `~/.pgpass` file (mode `0600`), and a password inside a URI
ends up in shell history and `ps` output. Use `sslmode=require` (or stricter)
for any non-local database.

## 3. Running migrations

Migrations are goose files in `api/db/migrations/`, applied as `od_migrator`.
**Apply them before deploying the code that needs them**: code that reads a
column its migration has not created yet fails at query time, not at boot.

Any of these work; they run the same pinned goose over the same files:

```bash
# From a machine that can reach the database
export PGPASSWORD='<migrator secret>'
export DATABASE_URL_MIGRATOR='postgres://od_migrator@db.example.internal:5432/od_amrita?sslmode=require'
make -C api migrate-up
unset PGPASSWORD

# Or the migration image (goose + the migration directory only); build from the repo root
docker build -f api/Dockerfile.migrate -t od-migrate .
docker run --rm -e GOOSE_DBSTRING="$DATABASE_URL_MIGRATOR" od-migrate status
docker run --rm -e GOOSE_DBSTRING="$DATABASE_URL_MIGRATOR" od-migrate up
```

The `db-migrate` GitHub Actions workflow does the same from a runner (manual
trigger only; it reads a `DATABASE_URL_MIGRATOR` repository secret and asserts
nothing is left pending).

Confirm the version afterwards; it must equal the highest-numbered file in
`api/db/migrations/`:

```bash
psql "$DATABASE_URL_MIGRATOR" -c 'SELECT max(version_id) FROM goose_db_version;'
```

**Rollback is not a recovery plan.** Several `Down` migrations deliberately
refuse once real data exists that the older schema cannot represent (multi-day
marks, removed or dispute-created records, slots above 12) rather than delete
attendance to succeed. Read a migration's `Down` before running it; past those
points, recover by restoring (section 9). Take a dump immediately before
applying migrations to a database that already holds records.

## 4. API environment variables

Set these as secrets in your container platform, never in a committed file.
`api/.env.example` documents each one in more detail.

| Variable | Required | Meaning |
|---|---|---|
| `APP_ENV` | yes | `production` for a real deployment. Enables the auth URL requirements and the placeholder-password refusal. |
| `DATABASE_URL_RUNTIME` | yes | DSN for `od_runtime`. Never the migrator's. |
| `DATABASE_URL_MIGRATOR` | migration step only | DSN for `od_migrator`. The running API does not need it; leave it unset there. |
| `API_PORT` | no (8443) | Listen port. |
| `TLS_ENABLED` | no (`true`) | See section 5. |
| `TLS_CERT_FILE`, `TLS_KEY_FILE` | when `TLS_ENABLED=true` | Readable PEM files; checked at boot. |
| `TLS_MIN_VERSION` | no (`1.2`) | `1.2` or `1.3` only. |
| `HTTP_REDIRECT_LISTENER` | no (`false`) | Must stay `false`; the API refuses to boot otherwise. |
| `PORTAL_ALLOWED_ORIGINS` | yes, for the browser | Comma-separated exact web origins (section 6). |
| `AUTH_JWKS_URL` | yes outside development | The broker's JWKS endpoint, `https://` only. |
| `AUTH_ISSUER` | yes outside development | Must equal the token's `iss` (a trailing slash is trimmed). |
| `AUTH_AUDIENCE` | no (`authenticated`) | Must equal the token's `aud`. |
| `AUTH_ALLOWED_DOMAIN` | set it explicitly | Your institution's email domain. The bare domain and any subdomain are accepted. |
| `AUTH_ALLOWED_PROVIDERS` | no (`azure`) | Comma-separated Supabase sign-in providers (the token's `app_metadata.provider`) the API accepts. Anything else, such as an email/password account, is refused even on the allowed domain. |
| `AUTH_BOOTSTRAP_ADMINS` | no | Comma-separated emails provisioned as `admin` on first sign-in. Everyone else starts as `student`. |
| `CARD_SEAL_ACTIVE_VERSION` | yes | Version label of the active card-seal key (16 characters or fewer, e.g. `v1`). |
| `CARD_SEAL_HMAC_KEYS` | yes | `version=key` pairs; each key is at least 32 random bytes, unpadded base64url. Server-only: never put it in `web/`, `mobile/` or logs. Generate with `openssl rand -base64 32 \| tr '+/' '-_' \| tr -d '='`. |
| `PUBLIC_RATE_LIMIT_PER_MINUTE`, `PUBLIC_RATE_LIMIT_BURST` | no | Public read limiter. |
| `PUBLIC_BOARD_RATE_LIMIT_PER_MINUTE`, `PUBLIC_BOARD_RATE_LIMIT_BURST` | no | Public dashboard limiter (defaults 600 / 200). |
| `PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE`, `PUBLIC_DISPUTE_RATE_LIMIT_BURST` | no | Public dispute submission limiter (defaults 5 / 3). |
| `PUBLIC_CACHE_*_TTL_SECONDS`, `MARKING_SCOPE_CACHE_TTL_SECONDS` | no | In-process cache lifetimes; `0` disables a cache. |
| `KAFKA_*` | no | Optional domain event stream; off when `KAFKA_BROKERS` is unset. See `api/.env.example`. |

A set-but-malformed value (a non-numeric limit, an origin with a path, a short
card-seal key) stops the API at boot with an error naming the variable.

## 5. TLS, proxies and client IPs

Plain HTTP is **refused, not redirected**: the API has no port-80 listener and
no redirect option. Choose one of:

- **TLS in the container**: `TLS_ENABLED=true` with `TLS_CERT_FILE` and
  `TLS_KEY_FILE`; expose `API_PORT` directly.
- **TLS at a proxy or load balancer**: `TLS_ENABLED=false` and keep the
  container port private to the proxy. The API logs a warning that it is
  serving plaintext, which is expected here. Configure the proxy to expose
  HTTPS only. Do not answer port 80 with a redirect; leave it closed.

Client IPs matter only for the public rate limiters. The API never trusts
`X-Forwarded-For` or `X-Real-IP`, because a caller can set them to rotate past a
limit. It keys on the TCP peer address, except on Fly.io (detected by the
`FLY_APP_NAME` variable the platform sets), where it uses the edge-supplied
`Fly-Client-IP`. Behind any other proxy, every request appears to come from the
proxy, so all public clients share one bucket. Either raise the public limits
to suit, or apply per-client rate limiting at the proxy.

## 6. CORS and the web build

`PORTAL_ALLOWED_ORIGINS` lists the exact origins the web app is served from:
scheme and host (and port, if not the default) only, e.g.
`https://portal.example.org`. No trailing slash, path, query or wildcard; the
API refuses to start on a malformed entry. Add every origin users actually load,
including an apex and a `www` variant if both serve the app.

The web app reads these at **build time** (Vite inlines them), so changing one
means rebuilding, not restarting:

| Variable | Value |
|---|---|
| `VITE_API_BASE_URL` | The API origin, e.g. `https://api.example.org`. If missing, the app calls its own origin and every API request fails. |
| `VITE_SUPABASE_URL` | Your Supabase project URL. The app does not start without it. |
| `VITE_SUPABASE_ANON_KEY` | The project's publishable (anon) key. Public by design; never use the service-role key here. |
| `VITE_SOURCE_URL` | Optional. Where the footer's source-code link points. |

Serve the web app over HTTPS. The camera scanner and Web NFC exist only in a
secure context, and Web NFC only in Chrome on Android.

## 7. Authentication

Supabase Auth brokers Microsoft sign-in and issues the session JWT. The API
verifies that JWT locally against the JWKS, checks issuer, audience and expiry,
requires a `session_id` claim, rejects an email marked unverified, accepts only
the providers in `AUTH_ALLOWED_PROVIDERS`, and enforces `AUTH_ALLOWED_DOMAIN` on
every protected request. The broker is never the only
admission gate.

1. **Microsoft Entra ID app registration.** Add a Web redirect URI of
   `https://<your-project>.supabase.co/auth/v1/callback` and create a client
   secret. Under *Token configuration*, add the optional claims `email` and
   `xms_edov` with token type **ID** (Supabase reads the ID token; adding them
   to the access token alone changes nothing).
2. **Supabase project.** Turn the Data API, automatic table exposure and
   automatic RLS **off**: clients talk only to the Go API, which enforces
   permissions and writes the audit log. Do not enable the "OAuth Server"
   feature; that makes Supabase an identity provider for other apps.
3. **Azure provider** (Authentication, Providers): paste the client ID and the
   secret *value*. Keep "Allow users without an email" **off**.
4. **URL configuration.** Set the Site URL to your web origin and add
   `https://portal.example.org/**` (each web origin) to Redirect URLs. A
   missing entry makes sign-in fail after the Microsoft round trip.
5. **Before User Created hook.** Reject sign-ups outside your domain before a
   user is ever created. Create this function in the Supabase SQL editor
   (replace `example.edu`), then select it under Authentication, Hooks, Before
   User Created, type Postgres:

   ```sql
   create or replace function public.restrict_signup_to_domain(event jsonb)
   returns jsonb language plpgsql as $$
   declare
     email text;
     host  text;
   begin
     email := lower(coalesce(
       event->'user'->>'email',
       event->'claims'->>'email',
       event->'user'->'raw_user_meta_data'->>'email'));
     host := split_part(email, '@', 2);
     -- The bare domain or any subdomain; the leading dot keeps
     -- look-alikes such as example.edu.attacker.test out.
     if email is null or not (host = 'example.edu' or host like '%.example.edu') then
       return jsonb_build_object('error', jsonb_build_object(
         'http_code', 403,
         'message', 'Only institutional accounts can access this system.'));
     end if;
     return event;
   end;
   $$;

   grant execute on function public.restrict_signup_to_domain to supabase_auth_admin;
   grant usage on schema public to supabase_auth_admin;
   revoke execute on function public.restrict_signup_to_domain from authenticated, anon, public;
   ```

6. **API settings.** `AUTH_JWKS_URL` is
   `https://<your-project>.supabase.co/auth/v1/.well-known/jwks.json`,
   `AUTH_ISSUER` is `https://<your-project>.supabase.co/auth/v1`, and
   `AUTH_AUDIENCE` is `authenticated` unless you changed it in Supabase.
7. **Bootstrap admins.** Put the first administrator's email in
   `AUTH_BOOTSTRAP_ADMINS`. They are provisioned as `admin` on first sign-in and
   can then grant teacher and coordinator roles in the portal. The list only
   affects first provisioning, so you can clear it afterwards.

Session lifetime is Supabase configuration (JWT expiry and session settings),
not API code. Make sure a session outlasts a full marking slot. A forced
sign-out from the admin portal is enforced by the API's own revocation list on
the next request; there is no Supabase service-role key to configure.

## 8. Health and smoke checks

`GET /health` answers `200 {"status":"ok","db":"up"}`. Its database ping is
currently disabled in the source (see `api/internal/handler/health/health.go`),
so it proves the process is serving, not that the database is reachable. Give
your platform's health check a grace period after boot; the pool connects
lazily.

After every deploy, run the smoke check from the repository root:

```bash
BASE_URL=https://api.example.org api/scripts/smoke.sh
```

It asserts the health response and that plain HTTP on port 80 gets no HTTP
response at all (a redirect fails the check). To confirm the database itself,
request a real read such as `GET /api/v1/public/events` once its cache has
expired; a 5xx there is the database. Then check the browser path by hand: sign
in on the web origin, and open a deep link such as `/portal/events` directly to
confirm the SPA rewrite.

## 9. Backups and restore

Back up the **application database** (the one `DATABASE_URL_RUNTIME` points
at), not the auth broker's. Two layers together give long-horizon recovery:

1. **Point-in-time recovery** from your Postgres host, for recent damage.
   Check how far back your plan actually reaches.
2. **Scheduled logical snapshots** with `api/scripts/pg_backup.sh`, kept off
   the database host for as long as you need (the script's default local
   retention is 90 days):

   ```bash
   export BACKUP_DATABASE_URL='<od_migrator DSN, from your secret store>'
   export BACKUP_UPLOAD_CMD='<command that copies "$1" to off-site storage>'
   api/scripts/pg_backup.sh --dry-run   # validate and print the plan
   api/scripts/pg_backup.sh             # real run; see --help for every option
   ```

   The script writes a custom-format dump, refuses one that is suspiciously
   small or contains no `audit_log` data, uploads it, and only then prunes old
   local dumps. It exits non-zero on any failure, so schedule it through a small
   wrapper under `set -euo pipefail` that fetches the DSN, and alert on the exit
   code.

**Restoring a snapshot** into a new, empty database:

1. Create an empty PostgreSQL 17 database. Never restore over the live one.
2. Run `api/db/init/00_roles.sql` there as the superuser and change both role
   passwords (section 2) **before** restoring. Roles are cluster objects, not
   part of the dump, and the dump's ownership and grants only apply if the
   roles already exist.
3. Restore as `od_migrator`, stopping at the first error:

   ```bash
   pg_restore --dbname "$RESTORE_DSN" --no-comments --exit-on-error od-amrita-<timestamp>.dump
   ```

4. Verify: `SELECT max(version_id) FROM goose_db_version;` matches the
   repository, `audit_log` and `users` have the counts you expect, and the API
   starts and serves real reads against the restored `DATABASE_URL_RUNTIME`.

Practise this on a throwaway database before you need it.

## 10. Before going live

- [ ] Postgres superuser, `od_migrator` and `od_runtime` all have new, unique
      passwords stored only in your secret store.
- [ ] `CARD_SEAL_HMAC_KEYS` is freshly generated, not copied from an example.
- [ ] Migrations are applied and the version matches the repository.
- [ ] `APP_ENV=production`; the API starts (it refuses placeholder passwords).
- [ ] HTTPS only, with port 80 closed rather than redirecting.
- [ ] `PORTAL_ALLOWED_ORIGINS`, `VITE_API_BASE_URL` and the Supabase redirect
      URLs all name the same web origins.
- [ ] The domain hook is enabled, and a sign-in from outside your domain is
      rejected.
- [ ] `api/scripts/smoke.sh` passes against the deployed API.
- [ ] Backups are scheduled and alerting, and one restore has been rehearsed.
