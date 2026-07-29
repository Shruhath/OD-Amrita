# Deployment runbook — OD-Amrita

**Last updated:** 2026-07-24
**Status:** 🟡 **Configuration ships; nothing is deployed yet.** `api/fly.toml`, `web/vercel.json`
and `api/scripts/smoke.sh` are in the repository and reviewed. Every step that costs money or
needs a console — the Fly app, the Vercel project, the Neon role bootstrap, the Supabase
redirect URL — is a human step and is tracked as an unchecked box in §8.
**Owner:** Shruhath

> This document records *exactly* what an operator runs to put OD-Amrita on the internet, and
> what to check afterwards to know it actually worked. It is written for **standard Amrita IT**:
> no knowledge of the original build is assumed.
> **No secret values are stored here** — only the names of the settings and where the real
> values live. For backup, restore and monitoring, see
> [`docs/backup-and-monitoring-runbook.md`](backup-and-monitoring-runbook.md); this file does not
> duplicate it.

---

## 1. Topology — what runs where

| Surface | Host | What it is | Notes |
|---|---|---|---|
| **API** | **Fly.io** — app `od-amrita-api`, **2 machines** in **`sin` (Singapore)**, `shared-cpu-1x` | The Go/gin server from `api/Dockerfile`. Config in `api/fly.toml`. | TLS terminates at the **Fly edge**; the container runs `TLS_ENABLED=false` behind it. **`bom` (Mumbai) is preferred but had no Fly capacity at deploy; `sin` is the nearest fallback — move back to `bom` when capacity returns.** |
| **Web** | **Vercel** — Root Directory `web`, framework preset **Vite** | The React SPA. Built by Vercel's Git integration on push. Config in `web/vercel.json`. | A **separate origin** from the API. That is why CORS and the redirect URL below both matter. |
| **Database** | **Neon** — project `Amrita-OD` | Every OD record and the immutable audit ledger. | The backup target. See the backup runbook §1. |
| **Auth broker** | **Supabase** | Brokers the Microsoft OAuth login and issues the session JWT the API verifies. | Not the application database. Holds no OD data. |

**Two machines, not one.** A single machine on any host is a single point of failure with a
restart window in the middle of it. Two `shared-cpu-1x` machines cost roughly $5–6/month
combined. The count is **not** set in `fly.toml` — it is a deploy-time command (§4, step 5).

**Why a region near India.** Every mark is written synchronously — there is no offline queue to
hide latency behind — so the round trip is felt directly by a coordinator standing in front of a
student. `bom` (Mumbai, ~15 ms) is the nearest region and the preferred home. When Fly has no
capacity in `bom`, `sin` (Singapore, ~40–60 ms) is the accepted fallback — the next-nearest region.
The current deploy is on `sin` for that reason; move it back to `bom` once Mumbai capacity returns.

**Why the web surface must be HTTPS.** Web NFC (`NDEFReader`) and the camera (`getUserMedia`)
are only available to a **secure context**. A non-HTTPS origin does not merely warn — the APIs
are absent, and the card-writing and scanning surfaces cannot work at all. Vercel provides the
certificate automatically; this is not something to configure, only something not to break.

---

## 2. Before you start

You need:

- **`flyctl`** installed and logged in (`flyctl auth login`). It is an operator tool, not a
  repository dependency — nothing in `go.mod` or `package.json` refers to it.
- **A Vercel account** with access to the repository.
- **The Neon console** for the `Amrita-OD` project, including the **owner-role connection
  string** (`neondb_owner`) — Neon has no true superuser. The backup runbook §1 calls this
  `$SUPERUSER_DSN`.
- **The Supabase console** for the auth project.
- **`psql`** and **`goose`** (or `make -C api migrate-up`) to bootstrap and migrate the database.

> **Nothing in this document should ever be pasted into a file in the repository.** Connection
> strings, role passwords, the anon key and the admin email live in Fly secrets, Vercel
> environment variables, or your shell — never in `fly.toml`, `vercel.json`, or this runbook.

---

## 3. Bootstrap the Neon database (do this first)

The API will not serve a single request until the two database roles exist and the migrations
are applied. `/health` reports `db:"down"` and Fly refuses to route to the machine (§7).

This mirrors the backup runbook **§5.3**, which explains *why* each step is needed — read it
once. The short form, run against the **live** `Amrita-OD` database:

**1. Create the two roles** as the Neon owner role:

```bash
psql "$SUPERUSER_DSN" -f api/db/init/00_roles.sql
```

The file is idempotent, so re-running it is safe. It creates `od_migrator` (DDL owner, runs
goose) and `od_runtime` (DML only — this is what stops a leaked application credential from
rewriting the audit ledger).

**2. Change both passwords immediately.**

> ⚠️ `00_roles.sql` creates the roles with **placeholder passwords that are committed to the
> repository**. Until you replace them, two login roles with world-known passwords exist on a
> database Neon exposes to the internet. Do this before anything else, not "after the deploy".

```bash
psql "$SUPERUSER_DSN" <<'SQL'
ALTER ROLE od_migrator PASSWORD '<new-migrator-secret>';
ALTER ROLE od_runtime  PASSWORD '<new-runtime-secret>';
SQL
```

**3. Apply the migrations** as `od_migrator` (keep the password out of the URI — libpq reads
`PGPASSWORD`, and a password in a connection string ends up in your shell log and in `ps`):

```bash
export PGPASSWORD='<new-migrator-secret>'
export DATABASE_URL_MIGRATOR='postgresql://od_migrator@<neon-host>/<db>?sslmode=require'
make -C api migrate-up
unset PGPASSWORD
```

**4. Confirm the schema is at the expected version:**

```bash
psql "$DATABASE_URL_MIGRATOR" -c 'SELECT max(version_id) FROM goose_db_version;'
```

Expect **10** — migrations `0001`–`0010`. A lower number, or a missing table, means the
migrations did not all apply; stop and fix that before deploying, because the API will fail at
runtime rather than at boot.

**5. Build the runtime connection string** for the next section from the **`od_runtime`**
password — never the migrator's. The application pool must not be able to run DDL.

---

## 4. Deploying the API to Fly

All commands run **from the repository root**, not from `api/`.

> **Why the root matters.** `api/Dockerfile` copies `go.work`, `api/`, `contracts/` and `test/`,
> so its build context is the whole repository. A deploy launched from inside `api/` cannot
> resolve the Go workspace and fails while looking for `contracts/generated/go`. The explicit
> `--config` / `--dockerfile` flags are what let the command run from the root.

**1. Create the app** (first time only — this does not deploy anything):

```bash
flyctl launch --no-deploy --config api/fly.toml
```

**2. Set the secrets.** These are **secrets**, not `[env]` entries — none of them may ever
appear in `fly.toml`:

```bash
flyctl secrets set \
  DATABASE_URL_RUNTIME='<od_runtime connection string from §3.5>' \
  AUTH_JWKS_URL='<supabase project>/auth/v1/.well-known/jwks.json' \
  AUTH_ISSUER='<supabase project>/auth/v1' \
  AUTH_AUDIENCE='authenticated' \
  AUTH_ALLOWED_DOMAIN='amrita.edu' \
  AUTH_BOOTSTRAP_ADMINS='<the first admin email>' \
  CARD_SEAL_ACTIVE_VERSION='v1' \
  CARD_SEAL_HMAC_KEYS='v1=<base64url 32-byte random key>' \
  PORTAL_ALLOWED_ORIGINS='<vercel-production-origin>'
```

`APP_ENV`, `API_PORT`, `TLS_ENABLED` and `HTTP_REDIRECT_LISTENER` are **not** secrets and are
already in `fly.toml`'s `[env]` block. Do not duplicate them here.

> **`PORTAL_ALLOWED_ORIGINS` must be an EXACT origin** — scheme and host only, e.g.
> `https://od-amrita.vercel.app`. **No trailing slash**, no path, no query, no wildcard. The
> API validates this at boot and **refuses to start** on a malformed value, so a stray `/` at
> the end turns into a deploy that never comes up. Multiple origins are comma-separated.

> **`CARD_SEAL_HMAC_KEYS` is the server-only NFC card secret.** Generate at least 32 random
> bytes and encode them with unpadded base64url; keep the active version at 16 characters or
> fewer, e.g. `v1`. The API validates this at boot and refuses to start on missing, malformed,
> short, duplicate, or overlong versions. Never put this value in `web/`, `mobile/`, NFC
> payloads, audit payloads, logs, or generated client code.

**3. Deploy:**

```bash
flyctl deploy --config api/fly.toml --dockerfile api/Dockerfile
```

**4. Confirm it came up:**

```bash
flyctl status --config api/fly.toml
flyctl logs --config api/fly.toml
```

The log line `WARNING serving PLAINTEXT HTTP on :8443 — TLS_ENABLED=false` is **expected and
correct** here. The Fly edge holds the certificate and terminates TLS; the container serves
plain HTTP behind it. That warning exists so an *accidental* plaintext deployment is visible —
on Fly it is the intended arrangement.

**5. Scale to two machines** (the count cannot be expressed in `fly.toml`):

```bash
# Use the region set in api/fly.toml — bom when it has capacity, otherwise the
# sin fallback (the current deploy is on sin; see "Why a region near India").
flyctl scale count 2 --region sin
flyctl status --config api/fly.toml   # expect two machines, both passing the health check
```

**6. Record the origin** in §6 below: `https://od-amrita-api.fly.dev` unless a custom domain is
added later.

### 4.1 The transport rule this host config enforces (do not "helpfully" undo it)

Plain HTTP must be **rejected, not redirected** (NFR15). A 301/308 redirect still accepts the
insecure connection and then answers it; the requirement is that it is not answered at all.

`api/fly.toml` achieves this by declaring a `[[services]]` block that exposes **only port 443**.
Nothing is listening on port 80, so a plain-HTTP request gets a refused connection.

> **Do not replace the `[[services]]` block with `[http_service]`.** Fly's convenient default
> implies a port-80 listener, and its `force_https` option answers plain HTTP with a **301
> redirect** — exactly what is forbidden. Do not add a port-80 entry either. The API refuses to
> boot if `HTTP_REDIRECT_LISTENER=true`, so the application layer is already protected; this is
> the matching guarantee at the edge. §7 verifies both.

---

## 5. Deploying the web surface to Vercel

Vercel builds from its own **Git integration** — there is deliberately **no** deploy job in
`.github/workflows/`. A second pipeline would duplicate the build and burn Actions minutes for
nothing. `web-deploy.yml` stays a test-and-build validation gate only.

1. **Import the repository** in the Vercel dashboard.
2. **Root Directory: `web`.** Framework preset: **Vite**. Build command `pnpm build`, output
   directory `dist` — Vercel infers both correctly from the preset.

   > **⚠️ Check the Production Branch before the first deploy.** Vercel defaults to the
   > repository's default branch. If the branch carrying the code you want live is not that
   > branch, Vercel will happily build the wrong one and you get a deploy that succeeds and
   > serves an application missing everything you just built — which reads as a broken deploy
   > rather than a misconfigured one. Set **Project → Settings → Git → Production Branch** to
   > the branch you are shipping, or merge that branch first and ship the default. Fly has the
   > same exposure from the other direction: `flyctl deploy` builds the **working tree you are
   > standing in**, so confirm `git branch --show-current` before deploying the API.
3. **Set the environment variables** (Project → Settings → Environment Variables). Vite inlines
   these at **build time**, so changing one requires a **redeploy**, not just a restart:

   | Variable | Value | If it is missing |
   |---|---|---|
   | `VITE_API_BASE_URL` | the Fly API origin, e.g. `https://od-amrita-api.fly.dev` | The SPA falls back to its own origin and **every API call 404s**. |
   | `VITE_SUPABASE_URL` | the Supabase project URL | The app **white-screens on boot** — the client throws. |
   | `VITE_SUPABASE_ANON_KEY` | the Supabase publishable/anon key | Same — white screen on boot. |

4. **Deploy**, and record the production origin in §6.

**The SPA rewrite is already configured.** `web/vercel.json` rewrites every path to
`/index.html`. Without it, opening a client-side route such as `/portal/events` directly — or
just refreshing the page while on it — returns a Vercel 404, because only `/` exists as a file.
Routing happens in the browser; the host has to serve the app for every non-asset path.

---

## 6. The cross-origin wiring — record the exact values used

The API and the web app are on **different origins**, so three settings have to agree. Each one
fails differently and none of them fail at build time.

> **Read the ✅ marks below as _tunnel/pre-production_, not production.** The top-of-document status
> is still "nothing is deployed to Fly yet" — the checks that pass here were run against the
> development tunnel + local container of §6.3, which does **not** meet the production transport
> posture (NFR15 in particular). Each ✅ says what was actually exercised; none of them asserts the
> Fly deploy.

| Setting | Where it is set | Value used | Confirmed |
|---|---|---|---|
| `PORTAL_ALLOWED_ORIGINS` | Fly secret (§4.2) | `https://od-amrita-web.vercel.app` | ✅ 2026-07-24 **(tunnel only)** — preflight 204 for this origin, 403 for an untrusted one; re-confirm on Fly |
| `VITE_API_BASE_URL` | Vercel env var (§5.3) | the API origin — **currently the pre-production tunnel**, see §6.3 | ✅ 2026-07-24 **(tunnel only)** — points at the tunnel, not the Fly origin yet |
| Supabase **Redirect URLs** | Supabase console (§6.1) | `https://od-amrita-web.vercel.app/**` | ☐ |
| Fly API origin | — | `______` (expected `https://od-amrita-api.fly.dev`) | ☐ not yet deployed |
| Vercel production origin | — | `https://od-amrita-web.vercel.app` | ✅ 2026-07-24 (Vercel build live; the web origin is real, the API it talks to is still the tunnel) |

**Vercel project settings in force** (recorded so they can be re-created): project `od-amrita-web`,
Root Directory `web`, framework preset Vite, **Production Branch `0-event-mvp`**. The production
branch is *not* the repository default — see the warning in §5 step 2; a deployment built from the
default branch serves an application without this work and looks like a broken deploy.

### 6.3 ⚠️ Pre-production: the API is on a development tunnel, not Fly

Until the Fly app exists, the API runs **on an operator laptop** behind a development tunnel that
terminates TLS and forwards to the local container. This is a **testing arrangement only** and is
deliberately called out because it does **not** meet the production transport posture:

- **NFR15 is not satisfied by the tunnel.** Plain HTTP on a tunnel host is not refused the way §4.1's
  port-443-only service block refuses it. `api/scripts/smoke.sh` check 2 is therefore expected to be
  meaningful only once the API is on Fly. Do not read a passing tunnel smoke run as an NFR15 pass.
- **The web app sends an interstitial opt-out header** on API requests so the tunnel's free tier does
  not answer browser requests with an HTML page. That header is inert against any other host and
  **should be removed when the API moves to its own domain**.
- **The database is a local container, not Neon.** The deployed Neon project is reachable over HTTPS
  but its Postgres port is blocked from the operator network, so migrations `0004`–`0010` are **not**
  applied there yet. Applying them (§3) is part of the Fly cutover, from a network that permits 5432.

**Cutover checklist when Fly lands:** deploy per §4 → apply §3 against Neon → set `VITE_API_BASE_URL`
to the Fly origin → remove the interstitial header → redeploy the web → re-run `smoke.sh` and treat
its NFR15 check as binding from that point on.

- **Wrong `PORTAL_ALLOWED_ORIGINS`** → the browser blocks every call on CORS. The API is fine;
  `curl` works; only the browser fails. Remember: **exact origin, no trailing slash.**
- **Missing `VITE_API_BASE_URL`** → the SPA calls itself for the API and everything 404s.
- **Missing Supabase redirect URL** → login starts, Microsoft succeeds, and the return leg
  refuses to come back to the deployed origin.

### 6.1 Supabase redirect URLs

Supabase console → **Authentication → URL Configuration → Redirect URLs**. Today it lists only
the local development entries (`http://localhost:3000/**`, `http://localhost:5173`). Add the
deployed web origin as `<vercel-origin>/**`. Set **Site URL** to the same origin.

### 6.2 Coordinator device setup

> **Open the web origin in Chrome on Android, over HTTPS.** Not a desktop browser, not Safari,
> not an in-app browser inside another application. Web NFC exists only in Chrome on Android,
> and both NFC and the camera require the secure (HTTPS) context — which is what the Vercel
> origin provides. Bookmark the exact URL on each coordinator's device before the event; a
> coordinator typing a URL from memory in a crowded corridor is a support ticket.

---

## 7. Verifying the deploy

Run the smoke check from the repository root, against the real origin:

```bash
BASE_URL=https://od-amrita-api.fly.dev api/scripts/smoke.sh
```

It asserts the two things a deploy cannot assert for itself:

1. **`GET /health` reports the database is up** — a body containing `"status":"ok"` and
   `"db":"up"`. A 200 alone is not enough: the API boots perfectly well with an unreachable
   database and answers **503 `{"status":"degraded","db":"down"}`**, which means the `od_runtime`
   pool never reached Neon. Check the `DATABASE_URL_RUNTIME` secret and §3.
2. **Plain HTTP is refused, not redirected.** The expected result is a refused connection
   (`curl` exit 7) because nothing is listening on port 80. A **301 or 308 is a failure** — it
   means a port-80 handler or `force_https` has been re-added to `api/fly.toml` (§4.1).

The script exits non-zero if either assertion fails. It is **operator-run against the live
host** — during the deploy and again in the pre-event dry run. It is deliberately not a CI job:
CI has no deployed URL, so a pipeline built around it could only pass vacuously.

Then verify the browser path by hand, which the script cannot do:

- Open the Vercel origin in **Chrome on Android**, sign in with an `@amrita.edu` account, and
  confirm the app loads past login. That exercises the Supabase redirect URL, `VITE_API_BASE_URL`
  and CORS on `GET /api/v1/auth/me` in one pass — the three settings in §6.
- Open a deep link directly (e.g. `<vercel-origin>/portal/events`). It must load the app, not a
  Vercel 404 — that is the `vercel.json` rewrite working.

### 7.1 `/health` is fail-closed — know this before you debug an outage

`/health` returns **503** when Neon is unreachable, and `api/fly.toml` health-checks that path.
So an unreachable database causes Fly to **pull the machine out of rotation**.

That is the behaviour we want: a mark must never be routed to an instance that cannot write it.
But it has a consequence worth stating plainly — **Neon is a hard dependency of availability.**
There is no offline queue and no degraded read-only mode. If every machine reports unhealthy,
look at the database first, not at Fly. The `grace_period` in the health check exists for a
different reason: the connection pool connects lazily, so a machine needs a few seconds after
boot before it can honestly answer `db:"up"`.

---

## 8. Operator checklist (human-only steps — tracked, not dropped)

Everything below needs a console, an account, or money. A code agent cannot do any of it, which
is why these are tracked here rather than assumed.

**Database (Neon)**
- [ ] Run the **Neon** role bootstrap on the live `Amrita-OD` database — `00_roles.sql` as the owner role, then **change both role passwords** (§3.1–3.2).
- [ ] Apply migrations as `od_migrator` and confirm `SELECT max(version_id) FROM goose_db_version` returns **9** (§3.3–3.4).
- [ ] **Upgrade the Neon plan before the event.** Free gives a **6-hour** PITR window, which does not cover an event plus the days afterwards when disputes are raised (Launch = 7 days, Scale = 30 days). Record the new window in the backup runbook §2.2.

**API (Fly)**
- [ ] Create the **Fly** app (`flyctl launch --no-deploy`) and set every secret from §4.2 — nothing with a credential in it goes into `fly.toml`.
- [ ] Deploy, then `flyctl scale count 2 --region <fly.toml region>` (`bom` preferred, `sin` the current fallback), and confirm **two** machines pass the health check (§4.3–4.5).
- [ ] Record the API origin in §6.

**Web (Vercel)**
- [ ] Create the **Vercel** project with Root Directory `web`, set the three `VITE_*` variables, and deploy (§5).
- [ ] Record the production web origin in §6, and confirm a deep link such as `/portal/events` loads the app rather than a 404.

**Auth (Supabase)**
- [ ] Add `<vercel-origin>/**` to **Supabase** → Authentication → URL Configuration → Redirect URLs, and set Site URL to the same origin (§6.1).

**Verification**
- [ ] Run `BASE_URL=<api-origin> api/scripts/smoke.sh` and record the result below.
- [ ] Complete one `@amrita.edu` login on the deployed web app **in Chrome on Android** (§7).
- [ ] Register the uptime monitor against the now-known `/health` URL — see the backup runbook §4.2 and §7.
- [ ] Bookmark the web origin on each coordinator device before the event (§6.2).

**Deploy log** — record every deploy here, including the ones that went wrong:

| Date | What changed | API origin | Web origin | Smoke result | Notes |
|---|---|---|---|---|---|
| `______` | `______` | `______` | `______` | `______` | `______` |

---

## 9. Decisions recorded here (ratify or change)

| # | Decision | Why |
|---|---|---|
| **H1** | **Fly.io, 2 machines, `bom` (preferred) / `sin` (fallback).** | Two machines (~$5–6/mo total) are cheaper than one instance on the alternatives considered and more redundant than any single one. `bom` (Mumbai) is the only India region and the synchronous mark path has no queue to mask latency; when Fly has no `bom` capacity, `sin` (Singapore) is the next-nearest fallback. **Currently deployed on `sin` — Fly had no `bom` capacity at deploy; revisit when it returns.** |
| **H2** | **Vercel, separate origin, Git integration.** | Matches the decoupled-frontend architecture and reuses the already-built CORS middleware instead of adding single-origin embedding to the Go binary. Instant HTTPS gives the secure context NFC and the camera require. No Actions deploy job — Vercel already builds on push. |
| **H3** | **Neon stays the application database.** | Moving to another Postgres would be lateral: it removes no work, solves no hosting problem, and would mean redoing the dual roles, the audit trigger and the privilege defaults on a different pooler. |
| **H4** | **TLS terminates at the Fly edge; the container runs `TLS_ENABLED=false`.** | Fly manages the `*.fly.dev` certificate; the container cannot easily obtain it. The application already supports ingress-terminated mode. The hazard is that Fly's default shape *redirects* plain HTTP, so §4.1's port-443-only service block is what keeps it **rejected** instead. |
| **H5** | **Every paid/console step is a tracked human step.** | Fly, the Neon plan upgrade and Vercel cost money; the Supabase redirect URL needs a console. §8 tracks them as unchecked boxes rather than leaving them implied. |
