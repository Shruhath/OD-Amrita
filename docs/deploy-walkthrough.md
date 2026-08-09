# Go-live walkthrough — putting the foundation on the internet

A hand-holding companion to [`deployment-runbook.md`](deployment-runbook.md). The runbook is the
*reference* (why each thing is shaped the way it is); **this file is the do-it checklist** you follow
top to bottom, once. Every step here needs a console, an account, or money — a code agent cannot run
any of it, which is exactly why you're doing it by hand.

**Order is fixed:** Neon → Fly → Vercel → Supabase → Verify. Each step depends on a value the previous
one produced. Don't skip ahead.

---

## ✅ State check — most of this is already provisioned

Before you touch anything, know what already exists (all in your **gitignored `.env`** at the repo
root, and `web/.env`). You are **not** creating these from scratch:

| Already done | Where it lives | Meaning |
|---|---|---|
| Neon roles `od_migrator` + `od_runtime` created **and passwords rotated** (real hex, not placeholders) | `.env` → `DATABASE_URL_MIGRATOR`, `DATABASE_URL_RUNTIME`, `SUPERUSER_DSN` | Step 1's role-create + password-rotate is **already done**. Do NOT re-run `00_roles.sql` or `ALTER ROLE`. |
| Supabase auth values | `.env` → `AUTH_JWKS_URL`, `AUTH_ISSUER`, `AUTH_AUDIENCE`, `AUTH_ALLOWED_DOMAIN` | Reuse verbatim as Fly secrets — no re-entry. |
| Web Supabase values | `web/.env` → `VITE_SUPABASE_URL`, `VITE_SUPABASE_ANON_KEY` | Two of the three Vercel env vars, ready to copy. |

> **⚠️ Ignore the `Migrator@OD2026` / `Runtime@OD2026` passwords from the chat.** The roles already
> have different, stronger passwords (the hex ones in `.env`). Using the simple ones would mean
> re-`ALTER ROLE` on Neon *and* editing `.env` *and* Fly, for zero benefit. **Keep what's in `.env`.**

**So the only things genuinely left to do:** confirm the **migrations** are applied on Neon (needs raw
5432 — see the blocker below), then Fly + Vercel + Supabase-redirect + verify.

**Two values still to decide** (not in any file yet):
- **First admin email** — an `@amrita.edu` address → Fly secret `AUTH_BOOTSTRAP_ADMINS`.
- **`PORTAL_ALLOWED_ORIGINS`** — the exact Vercel web origin (known after Step 3).

No secret ever gets pasted into a committable file. Everything sensitive stays in `.env` / Fly secrets /
Vercel env vars. (That's why the values live in `.env`, referenced by name below, not written out here.)

---

## ⚠️ THE current blocker — your laptop is on the port-5432-blocked network

Step 1 needs raw Postgres **port 5432**. Right now your laptop's default route is `wlan0 / 10.74.90.x`
(the campus wifi). Live probe: **443 OPEN, 5432 REFUSED.** Switching your *phone* to mobile data does
nothing unless the **laptop itself** leaves this wifi.

**Fix:** connect the **laptop's** wifi to your **phone's hotspot SSID** (or USB-tether the phone). Then
tell me and I'll re-probe 5432 — when it's open, **I can run the migration for me** (it's not a paid or
console step, so I'm allowed to; only the Fly/Vercel/Supabase/paid steps need you).

Everything after Step 1 is pure HTTPS and works on any network, so you can rejoin campus wifi once the
DB is migrated.

---

## Prerequisites (install / log in once)

```bash
# From the repo root: /mnt/Data/Projects/OD-amrita
flyctl version        # install from https://fly.io/docs/flyctl/install/ if missing
flyctl auth login     # run it yourself with:  ! flyctl auth login
```

`psql`/`goose` are **not** installed and don't need to be — goose runs via `go run` (pinned
`v3.27.2`), which is what `make -C api migrate-up` uses. `go` and `podman` are present.

You also need, logged in: **Vercel** (with repo access) and the **Supabase console**
(project `pheeydjlwdqfpyqhhwuc`).

---

## STEP 1 — Neon: confirm/apply migrations (roles already done)

Roles and passwords are already set (see the state table). The only open item is the schema. `migrate-up`
is idempotent — it applies only what's pending — and connecting as `od_migrator` **also proves the
password in `.env` is correct**. This must run on a network with 5432 open (see the blocker above).

**When the laptop is on the hotspot, this is the whole of Step 1** (I can run it for you):

```bash
# From repo root — loads the existing DSNs from .env, applies pending migrations
set -a; source .env; set +a
make -C api migrate-up

# Confirm the schema version — must print exactly 8
cd api && GOWORK=off go run github.com/pressly/goose/v3/cmd/goose@v3.27.2 \
  -dir db/migrations postgres "$DATABASE_URL_MIGRATOR" status
```

Expect goose to apply any of `0001`–`0008` not yet present (possibly none, if already done) and
`status` to show all eight `Applied`.

✅ **Step 1 is done when all 8 migrations show `Applied`.** If it can't connect → you're still on the
5432-blocked network (the `10.74.90.x` wifi); join the hotspot and retry.

---

## STEP 2 — Fly: deploy the API on two machines

All commands run **from the repo root** (`api/Dockerfile` needs the whole repo as build context).

**2a. Create the app** (first time only — does not deploy anything):

```bash
flyctl launch --no-deploy --config api/fly.toml
```

If it offers to overwrite `fly.toml`, **say no** (the committed one is deliberate: port-443-only,
NFR15). If it offers to create a Postgres/Redis, **decline** — Neon is the database.

**2b. Set the secrets — sourced from `.env`, so no secret is typed by hand.** Only the admin email and
the Vercel origin are new values you supply:

```bash
set -a; source .env; set +a
flyctl secrets set \
  DATABASE_URL_RUNTIME="$DATABASE_URL_RUNTIME" \
  AUTH_JWKS_URL="$AUTH_JWKS_URL" \
  AUTH_ISSUER="$AUTH_ISSUER" \
  AUTH_AUDIENCE="$AUTH_AUDIENCE" \
  AUTH_ALLOWED_DOMAIN="$AUTH_ALLOWED_DOMAIN" \
  AUTH_BOOTSTRAP_ADMINS='<your-first-admin@amrita.edu>' \
  PORTAL_ALLOWED_ORIGINS='<expected-vercel-origin>' \
  --config api/fly.toml
```

- `DATABASE_URL_RUNTIME` from `.env` uses **`od_runtime`** (DML-only) — correct; never the migrator.
- `APP_ENV`, `API_PORT`, `TLS_ENABLED`, `HTTP_REDIRECT_LISTENER` are already in `fly.toml`'s `[env]` —
  **do not** add them here.
- `PORTAL_ALLOWED_ORIGINS` must be an **exact origin** — scheme + host, e.g.
  `https://od-amrita-web.vercel.app`. **No trailing slash**, no path, no wildcard. The API refuses to
  boot on a malformed value. Put your best-guess Vercel origin now; we reconcile it at the end of Step 3.

**2c. Deploy:**

```bash
flyctl deploy --config api/fly.toml --dockerfile api/Dockerfile
```

**2d. Confirm it came up:**

```bash
flyctl status --config api/fly.toml
flyctl logs   --config api/fly.toml
```

The log line `WARNING serving PLAINTEXT HTTP on :8443 — TLS_ENABLED=false` is **expected and correct**
— the Fly edge terminates TLS; the container runs plaintext behind it. Not a misconfig.

**2e. Scale to two machines:**

```bash
flyctl scale count 2 --region bom
flyctl status --config api/fly.toml   # expect TWO machines, both passing the /health check
```

**2f. Record the API origin** — `https://od-amrita-api.fly.dev` unless you add a custom domain.

✅ **Step 2 is done when `flyctl status` shows two machines passing `/health`.** (A green `/health` also
proves the runtime DSN's password is correct — the second half of the Step 1 verification.)

**Troubleshooting Step 2**
- *Both machines unhealthy / `/health` 503* → runtime pool can't reach Neon: check `DATABASE_URL_RUNTIME`
  (right host, `sslmode=require`, `od_runtime`) and that Step 1 finished at version 8.
- *Refuses to boot, logs mention `PORTAL_ALLOWED_ORIGINS`* → trailing slash or a path. Re-set as a bare
  origin and redeploy.
- *Build fails resolving `contracts/generated/go`* → you ran deploy from inside `api/`. Run from the repo
  root with the `--config`/`--dockerfile` flags exactly as above.

---

## STEP 3 — Vercel: deploy the web app

1. **Import the repo** (Add New → Project → this GitHub repo).
2. **Root Directory: `web`.** Framework preset **Vite** (build `pnpm build`, output `dist` — inferred).
3. **⚠️ Set Production Branch to `0-event-mvp`** (Settings → Git → Production Branch). Vercel defaults to
   the repo default branch; the wrong branch builds an app missing everything and looks like a broken
   deploy.
4. **Set the three environment variables** (Settings → Environment Variables). The first two are copied
   from `web/.env`; the third is the Fly origin from Step 2f. Vite inlines these at **build time**, so a
   change needs a **redeploy**:

   | Variable | Value | Source |
   |---|---|---|
   | `VITE_API_BASE_URL` | the **Fly origin**, e.g. `https://od-amrita-api.fly.dev` | Step 2f |
   | `VITE_SUPABASE_URL` | `https://pheeydjlwdqfpyqhhwuc.supabase.co` | already in `web/.env` |
   | `VITE_SUPABASE_ANON_KEY` | the anon key | already in `web/.env` |

5. **Deploy**, then note the **production web origin** (`https://<project>.vercel.app`).

**Reconcile the CORS origin:** if the real Vercel origin differs from the `PORTAL_ALLOWED_ORIGINS` guess
from Step 2b, fix it (setting a secret auto-restarts Fly):

```bash
flyctl secrets set PORTAL_ALLOWED_ORIGINS='https://<real-vercel-origin>' --config api/fly.toml
```

✅ **Step 3 is done when the Vercel deploy is live and `PORTAL_ALLOWED_ORIGINS` on Fly equals the real
web origin (no trailing slash).**

---

## STEP 4 — Supabase: allow the return leg of login

Supabase console (project `pheeydjlwdqfpyqhhwuc`) → **Authentication → URL Configuration**:
- **Redirect URLs:** add `<vercel-origin>/**` (e.g. `https://od-amrita-web.vercel.app/**`).
- **Site URL:** set to the same origin.

Without this, login starts, Microsoft succeeds, and the redirect back to the deployed origin is refused.

✅ **Step 4 is done when the deployed web origin is in Redirect URLs and Site URL.**

---

## STEP 5 — Verify the whole thing works

**5a. Smoke-check the API** from the repo root against the real Fly origin:

```bash
BASE_URL=https://od-amrita-api.fly.dev api/scripts/smoke.sh
```

Asserts the two things a deploy can't self-report:
1. `GET /health` reports `"status":"ok"` **and** `"db":"up"` (runtime pool reached Neon).
2. Plain HTTP is **refused, not redirected** (NFR15) — expected: curl exit 7 (connection refused),
   nothing on port 80. A **301/308 is a FAIL** — a port-80 handler or `force_https` crept back into
   `fly.toml`.

Expected final line: `SMOKE CHECK PASSED — … serves HTTPS, reports db up, and refuses plain HTTP.`

**5b. Verify the browser path by hand** (the script can't):
- Open the **Vercel origin in Chrome on Android**, sign in with an `@amrita.edu` account, confirm the app
  loads past login. That exercises the Supabase redirect URL, `VITE_API_BASE_URL`, and CORS on
  `GET /api/v1/auth/me` in one pass.
- Open a deep link directly (e.g. `<vercel-origin>/portal/events`) — it must load the app, **not** a
  Vercel 404 (proves the `vercel.json` SPA rewrite).

**5c. Register the uptime monitor** against the `/health` URL (backup-and-monitoring runbook §4.2 / §7).

**5d. Bookmark the web origin** on each coordinator's Android device.

✅ **Step 5 is done when `smoke.sh` prints PASSED and one real `@amrita.edu` login succeeds on
Chrome-on-Android.**

---

## Don't forget: upgrade the Neon plan before the event

Free tier gives a **6-hour** PITR window — it does not cover an event plus the dispute days after
(Launch = 7 days, Scale = 30 days). Upgrade the `Amrita-OD` plan and record the new window in the backup
runbook §2.2. Paid step; do it before go-live.

---

## When all five steps are green — tell me

Paste the `smoke.sh` PASSED output and a "login worked" confirmation. Then I will, per the automated
delivery rules:

1. Fill in the **Deploy log** table in `deployment-runbook.md` §8 and tick the §8 boxes.
2. Flip the story status from **`blocked`** to **`done`**.
3. Merge `0-event-mvp` → `main`, wait for CI green.
4. Cut tag **`V0.0.1`** and a GitHub Release from it.

Until then the story stays `blocked` and nothing merges — a release tag must only ever point at a
foundation that's actually live and smoke-passed.

---

## Quick reference

```bash
# STEP 1 — Neon (needs laptop on the hotspot, 5432 open)
set -a; source .env; set +a
make -C api migrate-up

# STEP 2 — Fly (from repo root)
flyctl launch --no-deploy --config api/fly.toml
set -a; source .env; set +a
flyctl secrets set DATABASE_URL_RUNTIME="$DATABASE_URL_RUNTIME" AUTH_JWKS_URL="$AUTH_JWKS_URL" \
  AUTH_ISSUER="$AUTH_ISSUER" AUTH_AUDIENCE="$AUTH_AUDIENCE" AUTH_ALLOWED_DOMAIN="$AUTH_ALLOWED_DOMAIN" \
  AUTH_BOOTSTRAP_ADMINS='<admin@amrita.edu>' PORTAL_ALLOWED_ORIGINS='https://<vercel-origin>' \
  --config api/fly.toml
flyctl deploy --config api/fly.toml --dockerfile api/Dockerfile
flyctl scale count 2 --region bom

# STEP 5 — Verify
BASE_URL=https://od-amrita-api.fly.dev api/scripts/smoke.sh
```
