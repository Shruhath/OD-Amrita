# Deployment runbook — OD-Amrita

**Last updated:** 2026-08-09
**Status:** 🟢 **Live and verified end to end.** API on Fly (two machines, both healthy), web on
Vercel, schema on Neon at version **17**, Supabase brokering sign-in. Verified 2026-08-09 by
`api/scripts/smoke.sh`, a browser run against the production origin, and a direct read of the live
schema. §8 records exactly what is done and the four items that still need a human.

> ⚠️ **Two things are live but not right yet**, both in §9: the database is in the wrong region
> (**H7**, ~0.5 s of every query is ocean) and the Neon plan still gives a 6-hour recovery window,
> which does not cover an event plus the dispute days after it.

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

> ### ⚠️ The database is on the other side of the planet from the API
>
> `fly.toml` puts the API in **`sin` (Singapore)** for the reason above. The Neon project is in
> **`us-east-1` (Virginia)** — visible in the DSN host, `…us-east-1.aws.neon.tech`. Every query the
> API makes therefore crosses the Pacific *and* most of a continent.
>
> Measured against the live deployment, 2026-08-09 (15 samples each, from Coimbatore):
>
> | Request | Median |
> |---|---|
> | A read served from the in-process cache (`/api/v1/public/events`) | **0.157 s** |
> | `/health` — one database ping, nothing else | **0.639 s** |
>
> The floor is ~0.150 s, which is this network to Singapore. **So roughly half a second of every
> uncached request is the database round trip alone**, and it is paid again for each query a
> handler makes.
>
> That gap is also the clearest evidence the caching layer works: it takes a repeat public read
> from 0.639 s to 0.157 s — the network floor — by removing the ocean from the path entirely. What
> it cannot do is remove it from the FIRST read, or from any write.
>
> It lands hardest exactly where this runbook already says latency is felt directly: the
> synchronous mark path, where a coordinator is standing in front of a student.
>
> **Fixing it means moving the database, not the API** — the API belongs near campus. Neon cannot
> relocate a project between regions, so this is: create a project in `ap-southeast-1`, dump,
> restore, re-create the two roles, repoint `DATABASE_URL_RUNTIME` and `DATABASE_URL_MIGRATOR`.
> **That is a migration of live attendance data and a deliberate human decision — it has not been
> done.** Do it before the event, not during.

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

**3. Apply the migrations** as `od_migrator`.

> ### ✅ Prefer the workflow — it works from any network
>
> **Actions → `db-migrate` → Run workflow**, type `migrate` to confirm. That is the normal way to
> migrate this database and it is what applied `0014`–`0017` on 2026-08-09.
>
> This is not merely convenient. **Neon speaks Postgres on port 5432 and nothing else, and campus
> wifi permits 80/443 outbound only** — so the local command below simply cannot reach production
> from a machine on that network, and a migration that depends on which wifi someone is joined to
> is a migration that gets skipped. A GitHub runner has open egress, and the migrator credential
> then lives in one place (the `DATABASE_URL_MIGRATOR` repository secret) rather than on whichever
> laptop happened to be somewhere unblocked.
>
> It is manual-trigger only, never `on: push` — a schema change against real attendance is a
> decision someone makes, not a side effect of merging — and it has **no `down` path at all**,
> because `0016`'s Down is a one-way door once a multi-day event has been marked (see the warning
> below). It also asserts the applied version equals the highest migration in the repository, so a
> run that quietly applied nothing cannot look like a success.

The local equivalent, for a network with 5432 open (keep the password out of the URI — libpq reads
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

Expect **18** — migrations `0001`–`0018`. A lower number, or a missing table, means the
migrations did not all apply; stop and fix that before deploying, because the API will fail at
runtime rather than at boot.

> **⚠️ `0016` is a one-way door once a multi-day event has been marked.**
>
> `0016`'s `Down` restores the pre-range unique index `(student_id, event_id, slot)`. That
> succeeds only while no student has been marked in the **same slot on two different days** —
> which is exactly what a multi-day event exists to allow. After the first such mark, a
> `goose down` past `0016` (and therefore `goose reset`) aborts with `23505`.
>
> The abort is **safe**: goose rolls the step back and the database keeps its multi-day key.
> Nothing is lost and nothing is half-applied. But it means **rollback is not your recovery
> plan** past this point — restore from PITR or a dump instead (see
> `backup-and-monitoring-runbook.md`), or reconcile the duplicate `(student, event, slot)`
> rows by hand first and accept that doing so discards attendance records.
>
> This is deliberate. The alternative — a `Down` that deletes or collapses the colliding OD
> records so it can succeed — would destroy student attendance to make a rollback tidy.
> `0017`'s `Down` **is** fully reversible by contrast: it sums each coordinator's per-day
> usage back into the single row the pre-`0017` schema held, because `usage_date` is being
> dropped anyway and no information survives either way.
>
> Take a dump immediately before applying `0015`–`0018` to a database that already holds
> records.

> **⚠️ `0018` is a one-way door once a record has been removed.**
>
> Its `Down` restores the old `status = 'present'` CHECK. After a removal, that CHECK correctly
> rejects the historical `removed` row, so rollback aborts atomically and the widened status
> constraint remains in place. Do not convert removed records back to present or delete them merely
> to make a rollback succeed; reconcile the records deliberately or restore from PITR/a dump.

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

`APP_ENV`, `API_PORT`, `TLS_ENABLED`, `TLS_TERMINATED_BY_TRUSTED_PROXY` and `HTTP_REDIRECT_LISTENER` are **not** secrets and are
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

> ### ⚠️ Migrate FIRST, and deploy from a CLEAN tree
>
> Two mistakes are easy here, and on 2026-08-09 both were made at once, taking the attendance
> views down with a 500 in production.
>
> **`flyctl deploy` builds from your WORKING DIRECTORY, not from git.** `api/Dockerfile` COPYs
> `api/`, `contracts/` and `test/` out of the build context, which is whatever is on disk —
> uncommitted edits, another agent's half-finished feature, a debug print. The running image then
> corresponds to no commit at all, so "what is deployed?" has no answer. **Check `git status` is
> clean and you are on the commit you mean to ship before running this.**
>
> **Schema goes up BEFORE code, never after.** Code that reads a column its migration has not
> created yet fails at query time, not at boot — so the deploy looks successful, `/health` stays
> green (it only pings the pool), and the failure surfaces later as a 500 on whichever screen
> happens to run that query first. Run the `db-migrate` workflow, confirm the version, then
> deploy.
>
> The reverse order is safe for an ADDITIVE migration — a new nullable column that nothing yet
> reads — which is why migrating first is the habit worth having rather than a rule with
> exceptions to memorise.

```bash
git status --short          # must be empty
flyctl deploy --config api/fly.toml --dockerfile api/Dockerfile
```

**4. Confirm it came up:**

```bash
flyctl status --config api/fly.toml
flyctl logs --config api/fly.toml
```

The log line `WARNING serving PLAINTEXT HTTP on :8443 — TLS_ENABLED=false` is **expected and
correct** here. The Fly edge holds the certificate and terminates TLS; the container serves
plain HTTP behind it. `TLS_TERMINATED_BY_TRUSTED_PROXY=true` explicitly records that boundary;
the API refuses to boot in production if `TLS_ENABLED=false` is used without it. The warning
exists so an *accidental* plaintext deployment is visible — on Fly it is the intended arrangement.

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
  but its Postgres port is blocked from the operator network, so migrations `0004`–`0017` are **not**
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

A box is ticked here only when it was **observed**, not when it was believed — the note after each
one says how.

**Database (Neon)**
- [x] Neon role bootstrap and both role passwords — *verified 2026-08-09: `od_migrator` applied migrations against the live database, which is only possible with the correct rotated password.*
- [x] Migrations applied; `max(version_id)` is **17** — *verified 2026-08-09 by reading the live `goose_db_version` directly.* (This said **14** until today and was wrong: `0015`–`0017` shipped after it was written.)
- [ ] ⚠️ **Upgrade the Neon plan before the event.** Free gives a **6-hour** PITR window, which does not cover an event plus the days afterwards when disputes are raised (Launch = 7 days, Scale = 30 days). Record the new window in the backup runbook §2.2. **Still open — this costs money, so it is yours to do.**
- [ ] ⚠️ **Move the project to `ap-southeast-1`** — decision **H7**. Not a nice-to-have: ~0.5 s of every query is currently the Pacific. It is a dump-and-restore of live attendance data, so it is a deliberate human step.

**API (Fly)**
- [x] Fly app created and every secret from §4.2 set — *verified 2026-08-09: all nine secrets present in `flyctl secrets list`, none in `fly.toml`.*
- [x] Deployed and scaled to **two** machines, both passing the health check — *verified 2026-08-09 via `flyctl status`.*
- [x] API origin recorded in §6 — `https://od-amrita-api.fly.dev`.

**Web (Vercel)**
- [x] Vercel project with Root Directory `web` and the three `VITE_*` variables — *verified 2026-08-09 by reading the deployed bundle: it points at the Fly origin and the Supabase project.*
- [x] Production web origin recorded, and deep links load the app rather than a 404 — *verified 2026-08-09: `/portal/events`, `/mark` and `/attendance` all answer 200.*

**Auth (Supabase)**
- [x] Redirect URLs and Site URL — *verified 2026-08-09: `/authorize` accepts the Vercel origin as `redirect_to` and forwards it to Microsoft rather than substituting the Site URL, which it would do for an origin that is not allow-listed.*

**Verification**
- [x] `BASE_URL=https://od-amrita-api.fly.dev api/scripts/smoke.sh` — **PASSED** 2026-08-09 (db up; plain HTTP refused, not redirected).
- [ ] Complete one `@amrita.edu` login on the deployed web app **in Chrome on Android** (§7). **Still open — needs a real university account on a real handset; nothing automated can stand in for it.**
- [ ] Register the uptime monitor against `/health` — see the backup runbook §4.2 and §7. **Still open (console).**
- [ ] Bookmark the web origin on each coordinator device before the event (§6.2). **Still open (physical).**

**Deploy log** — record every deploy here, including the ones that went wrong:

| Date | What changed | API origin | Web origin | Smoke result | Notes |
|---|---|---|---|---|---|
| 2026-08-09 | Migrations `0014`–`0017` applied to Neon; API redeployed from six-days-stale image; CORS preflight fixed to advertise `PUT`; pool connection retirement jittered; public read cache | `https://od-amrita-api.fly.dev` | `https://od-amrita-web.vercel.app` | PASS | The stale image was answering **404 on every public route**. Health checks had been flapping hourly on both machines. |

---

## 9. Decisions recorded here (ratify or change)

| # | Decision | Why |
|---|---|---|
| **H1** | **Fly.io, 2 machines, `bom` (preferred) / `sin` (fallback).** | Two machines (~$5–6/mo total) are cheaper than one instance on the alternatives considered and more redundant than any single one. `bom` (Mumbai) is the only India region and the synchronous mark path has no queue to mask latency; when Fly has no `bom` capacity, `sin` (Singapore) is the next-nearest fallback. **Currently deployed on `sin` — Fly had no `bom` capacity at deploy; revisit when it returns.** |
| **H2** | **Vercel, separate origin, Git integration.** | Matches the decoupled-frontend architecture and reuses the already-built CORS middleware instead of adding single-origin embedding to the Go binary. Instant HTTPS gives the secure context NFC and the camera require. No Actions deploy job — Vercel already builds on push. |
| **H3** | **Neon stays the application database.** | Moving to another Postgres would be lateral: it removes no work, solves no hosting problem, and would mean redoing the dual roles, the audit trigger and the privilege defaults on a different pooler. |
| **H4** | **TLS terminates at the Fly edge; the container runs `TLS_ENABLED=false`.** | Fly manages the `*.fly.dev` certificate; the container cannot easily obtain it. The application already supports ingress-terminated mode. The hazard is that Fly's default shape *redirects* plain HTTP, so §4.1's port-443-only service block is what keeps it **rejected** instead. |
| **H6** | **No Redis. Caching stays in-process.** | See below — the reasoning is long enough to deserve prose. |
| **H7** | **The Neon region is wrong and should move to `ap-southeast-1`.** | The API is in Singapore and the database is in Virginia, so every query pays ~220 ms of ocean. Recorded as a decision **not yet acted on**: it is a migration of live attendance data. See the topology warning in §1. |
| **H5** | **Every paid/console step is a tracked human step.** | Fly, the Neon plan upgrade and Vercel cost money; the Supabase redirect URL needs a console. §8 tracks them as unchecked boxes rather than leaving them implied. |

---

## 10. Should this use Redis? (H6)

**No — not for this event, and the reason is not "too much work".** Redis is a good answer to
problems this deployment does not have, and it would not touch the one it does.

### What Redis would genuinely buy

**One shared cache instead of two.** The public read cache is in-process, so the two Fly machines
each hold their own copy and each pays its own first miss. Redis would make that one miss instead
of two.

**Exact rate limiting.** This is the strongest argument, and it is a real gap: the limiter is also
in-process, so the effective per-client ceiling is roughly **double** the configured number. A
shared counter would make the configured value the true value.

**Cache that survives a restart.** Today a deploy empties both caches and the next requests refill
them.

### What it would cost

**Another hop, and possibly another ocean.** A cache read that is currently a memory lookup —
call it a microsecond — becomes a network call. In-region (Upstash or Fly Redis in `sin`) that is
1–5 ms, which is fine. Provisioned carelessly in the wrong region, it repeats the mistake H7 is
about, and a *slow* cache is worse than no cache.

**Another thing that can be down**, on an unauthenticated public path, during the one hour of the
year that matters. Doing this properly means a fallback path for "Redis is unreachable" and a test
that the fallback works — which is more code than the entire cache it would be replacing.

**A credential, a config surface, a bill, and something else to watch** on event day.

### Why the trade does not pay here

The arithmetic is unkind to Redis. Two machines × one miss per key per lifetime is **two** database
queries where Redis would make one. Saving one query per key per lifetime is not worth a new
network dependency on the public path.

And the shared-cache benefit is aimed at the wrong target. **The expensive thing here is not the
number of cache misses — it is what a miss costs**, and that is ~220 ms of trans-Pacific latency
(H7). Redis would shave a duplicate 220 ms while leaving the other one in place. **Moving the
database to `ap-southeast-1` removes it from every query, cached or not.** That is the same
engineering effort aimed at roughly a hundred times the benefit, which is why H7 is the
recommendation and H6 is a decline.

The rate-limit gap has a one-line mitigation that costs nothing: **configure half the number you
actually want**, and note it where the value is set. That is a worse mechanism than a shared
counter, and it is an entirely adequate one for a single event on two machines.

### When to revisit this

Reopen H6 when any of these becomes true — none of them is true today:

- The API runs **more than about four machines**, so the duplicated-miss factor stops being 2.
- Rate limiting must be **exact** — for example if it ever guards something billable, or a real
  abuse incident happens rather than a hypothetical one.
- Caches must **survive deploys** because they have become load-bearing rather than an optimisation.
- Something genuinely **shared and mutable across machines** appears — a job queue, a distributed
  lock, live presence. That is a Redis-shaped problem. Caching, here, is not.

**Do H7 first.** If the database still cannot keep up once it is in the same region as the API,
that is new information and this decision deserves rereading. Adding Redis before then would be
optimising the second-largest cost while the largest one is still in place.
