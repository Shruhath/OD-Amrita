# Backup & monitoring runbook — OD-Amrita

**Last updated:** 2026-07-11
**Status:** 🟡 **Documented; dev DB confirmed; not yet operational.** The backup script
ships and `/health` is live. Confirmed on the dev Neon project (2026-07-11): **Free tier,
6-hour PITR window, no scheduled snapshots** — see §2.2, this is thinner than assumed and
needs a plan upgrade before the event. Still open, tracked in §7: the daily `pg_dump` is
**not scheduled anywhere yet**, no Uptime Robot monitor exists (no deployed URL), and — the
one that matters most — **no restore drill has been run**. Until that drill happens, this
document describes a backup nobody has ever restored from.
**Owner:** Shruhath

> **A code review of the first draft of this runbook (2026-07-11) found that its restore
> procedure would have restored an old dump on top of the live production database**, and
> that its verification step pointed at a port the API does not listen on. Both are fixed
> below (§5.2, §5.4). That is precisely the class of defect a drill catches and a proofread
> does not — which is why the drill in §7 is not a formality.

> This document records *exactly* what an operator must configure, check and run to
> keep the OD-Amrita database recoverable and the service observable — page by page,
> in the same style as `docs/auth-microsoft-supabase-setup.md`. It is written for
> **standard Amrita IT**: no knowledge of the original build is assumed.
> **No secret values are stored here** — only the names of the settings and where the
> real values live.

---

## 1. Topology — which database you are actually backing up

**Read this section before touching anything.** Getting it wrong produces a backup
that runs daily, reports success, and is useless.

| System | What it holds | Back it up? |
|---|---|---|
| **Neon Postgres** (the app DB) | **Everything that matters**: `audit_log` (the immutable ledger), `users`, `auth_session_revocations`, and every future OD/event/scan table. This is what the API reads and writes via `DATABASE_URL_RUNTIME`. | ✅ **YES — this is the backup target.** |
| **Supabase** | **Auth broker only.** It brokers the Microsoft OAuth login and holds GoTrue's own identity/session tables (`auth.users`, `auth.sessions`). Its Data API is OFF and RLS is OFF — our backend never writes application data to it. | ⚪ Light (see §2.4) |

> **⚠️ The trap.** The original requirement (NFR29) says *"Supabase automated daily
> snapshots."* That wording predates the hybrid-auth split of 2026-07-03, and it is now
> **wrong**. Supabase is the **broker**; the application data and the **audit ledger live
> on Neon**. Snapshotting the Supabase project would back up GoTrue's identity tables and
> **miss the entire immutable audit log** — the one table the whole reliability guarantee rests
> on (the audit-log replay/reconciliation oracle reads it back). Back up **Neon**.

**Why the audit ledger drives everything here.** `audit_log` is append-only and
tamper-evident by DB trigger (migration `0002`). It is the system's record of who marked
which student present. It is the most backup-critical table in the project, and the reason
retention runs to 90 days rather than the host's PITR window.

**The DSNs** (values live in the environment / `.env`, never in this repo — see
`api/.env.example`):

| Variable | Role | Used for | Where the value comes from |
|---|---|---|---|
| `DATABASE_URL_MIGRATOR` | `od_migrator` — DDL owner, owns the schema | goose migrations, **and `pg_dump`** (the dump must read every object) | `.env` / the deployment's secret store |
| `DATABASE_URL_RUNTIME` | `od_runtime` — DML only, owns nothing | the API's connection pool | `.env` / the deployment's secret store |
| `SUPERUSER_DSN` | the cluster's bootstrap/owner role — **not** an app role | **restore only**: creating the two app roles on a fresh database (§5.3) | The host's admin connection string. On **Neon** there is no true superuser: use the project's **owner role** (`neondb_owner`) URI from the Neon console → *Connection Details*. On self-hosted/docker, the `postgres` superuser. |
| `RESTORE_DSN` | `od_migrator` **on the database being restored INTO** | **restore only**: `pg_restore`, and the post-restore checks (§5.2, §5.4) | You set it by hand during a restore, after creating the target DB. **It is not the same as `DATABASE_URL_MIGRATOR`** — see the warning in §5.2. |

> **⚠️ `RESTORE_DSN` exists to stop the worst mistake available in this document.**
> `DATABASE_URL_MIGRATOR` points at the database you are restoring *from* — the live
> one. If you run `pg_restore` against it, you replay an old dump **on top of
> production**. Every restore command below therefore uses `$RESTORE_DSN`, which you
> set to the **new, empty** target. Set it once, at the top of the restore, and never
> substitute the live DSN.

---

## 2. Backup posture

### 2.1 The honest answer to "90-day point-in-time recovery"

**No managed Postgres host we would plausibly use offers 90-day PITR.** Verified
2026-07-08:

| Provider | Max PITR window | Notes |
|---|---|---|
| **Neon** (our app DB) | **30 days** (Scale plan) | Free = 6h (1 GB cap); Launch = 7d; Scale = 30d. **No 90-day option.** History is billed per GB-month of WAL. |
| Supabase | 30 days (Enterprise) | Pro = 7d daily backups + PITR add-on. Not the app DB anyway. |

So "daily snapshots with 90-day PITR" cannot be satisfied literally. It is satisfied
**in substance** by two layers, and the runbook prescribes both:

| Layer | Mechanism | Horizon | Answers the question |
|---|---|---|---|
| **1 — PITR** | The host's built-in point-in-time recovery, enabled at its **maximum window** | Fine-grained, short (≤30d) | *"Someone made a bad write 20 minutes ago — put it back."* |
| **2 — Logical snapshots** | Scheduled `pg_dump -Fc` (§3), retained **90 days** in object storage | Coarse, long (90d) | *"We need the state from two months ago"* — and gives a **portable, provider-independent** copy that does not depend on Neon still existing. |

Together these meet the intent of NFR29. Layer 2 is also what guarantees the audit
ledger is durably recoverable independent of any one vendor's retention policy.

### 2.2 Layer 1 — PITR on the app DB (operator confirms)

Neon console → **Backup & Restore** (per branch) → *Restore from history* → **Configure**.

**Confirmed on the current (dev) project — 2026-07-11:**

| Setting | Value | Confirmed |
|---|---|---|
| Neon project (app DB) | `Amrita-OD` | ✅ 2026-07-11 |
| Branch | `production` | ✅ 2026-07-11 |
| Plan / tier | **Free** | ✅ 2026-07-11 |
| **History retention (PITR window)** | **6 hours** — the Free-tier maximum | ✅ 2026-07-11 |
| Scheduled snapshots (Neon's own) | **None. "No snapshots, no schedule set"** — Neon gates snapshot *schedules* behind a paid plan | ✅ 2026-07-11 |
| Production app DB | *not yet chosen — hosting deferred* | ☐ (§7) |

> **⚠️ Read the number above carefully: PITR is 6 hours, not 30 days.** On the Free tier,
> anything that happened more than **6 hours** ago cannot be recovered by PITR at all, and
> Neon will not run scheduled snapshots for us either. **The `pg_dump` layer (§2.3) is
> therefore not a supplement — right now it is the *only* thing standing between us and
> permanent loss of the audit ledger beyond a 6-hour horizon.** Two consequences:
>
> 1. **Before the event, upgrade the app-DB plan** (Neon Launch = 7d, Scale = 30d) so that
>    PITR covers at least the whole event window and the days after it, when disputes are
>    raised. A 6-hour window during a live event means a bad write discovered the next
>    morning is unrecoverable by PITR.
> 2. **Until then, the daily `pg_dump` must actually be running and landing in a bucket.**
>    An unscheduled script is not a backup. See §7.

### 2.3 Layer 2 — the 90-day logical snapshot schedule

| Item | Value | Confirmed |
|---|---|---|
| Script | `api/scripts/pg_backup.sh` (§3) | ✅ ships in-repo |
| Cadence | Daily (suggested `02:00` local, off-peak) | ☐ |
| Runs from | `______` (cron host / CI schedule) | ☐ |
| Storage bucket / location | `______` | ☐ |
| Encryption at rest | `______` (enable it — the dump contains every student record) | ☐ |
| **Retention** | **90 days** — enforce with a **bucket lifecycle rule**, not only the script's local prune | ☐ |

> **Retention is enforced in the bucket, not by the script.** `pg_backup.sh` prunes its
> own *local staging* directory, but the authoritative 90-day retention is a **lifecycle
> policy on the object-storage bucket** (S3/GCS/R2 "expire objects older than 90 days").
> A script-only prune breaks the moment the cron host is replaced.

### 2.4 Supabase (auth broker) — light posture, deliberately

The Supabase project is treated as **reconstructable**, so it gets no special backup
effort. The posture rests on reconstructability, **not** on Supabase having backups:

- Identity is keyed by `supabase_subject`, which we **persist in Neon's `users` table**.
- If the Supabase project were lost, the recovery is: re-create the project, re-apply the
  auth setup (`docs/auth-microsoft-supabase-setup.md` §9 is a from-scratch checklist), and
  users log in again with Microsoft. No OD data is lost, because none of it lives there.

> **Do not assume Supabase is backing this up for you.** Per §2.1, Supabase's daily
> backups are a **Pro-tier** feature. Our Supabase plan is `______` (operator: record it
> — §7). If it is Free, the auth project has **no backups at all**, and the paragraph
> above is the *entire* recovery plan. That is an acceptable trade only because it is
> true that nothing irreplaceable lives there — verify that claim still holds before
> relying on it.

*(If Amrita IT policy requires the auth broker to be fully backed up too, add it here —
that is a policy call, not a technical constraint.)*

---

## 3. The backup script — `api/scripts/pg_backup.sh`

A dependency-light `pg_dump` wrapper. **No connection string is stored in it** — it reads
the DSN from the environment.

```bash
api/scripts/pg_backup.sh --help        # full usage
api/scripts/pg_backup.sh --dry-run     # validate config + print the plan; writes nothing
api/scripts/pg_backup.sh               # real run
```

| Environment variable | Required? | Meaning |
|---|---|---|
| `BACKUP_DATABASE_URL` | **Yes** | Connection URI of the DB to dump. Falls back to `DATABASE_URL_MIGRATOR` **only if unset**; set-but-empty is a hard error (see below). **Use the migrator/owner role** — `pg_dump` must be able to read every object. |
| `BACKUP_UPLOAD_CMD` | **Yes**, unless `--local-only` | Shell command that ships the dump off-box; receives the dump path as `"$1"`. A dump that never leaves the machine is not a backup, so its absence **fails the run** rather than printing a warning nobody reads. |
| `BACKUP_DIR` | No (default `./backups`) | Local staging directory. Created mode `700`; artifacts are written `0600`. |
| `RETENTION_DAYS` | No (default `90`, minimum `1`) | Local prune horizon. `0` is refused — to `find(1)` it means "delete everything older than a day", not "never prune". |
| `BACKUP_MIN_BYTES` | No (default `4096`) | A dump smaller than this is rejected as suspect. |
| `BACKUP_TIMEOUT_SECONDS` | No (default `3600`) | A hung `pg_dump` is killed after this long, so the run always exits and always alerts. |

**Behavior worth knowing before you schedule it:**

- Artifacts are named `od-amrita-<UTC>.dump` (e.g. `od-amrita-20260711T020000Z.dump`) —
  UTC and sortable, so runs never overwrite each other. The dump is written as
  `…​.dump.part` and renamed only on success, so an interrupted run never leaves a
  truncated file that looks like a good one.
- Custom format (`pg_dump -Fc`), so `pg_restore` can restore selectively.
- **Ownership and `GRANT`s are kept in the dump on purpose.** See §5.3 — stripping them
  produces a restore the application cannot connect to.
- **The dump is verified before it is trusted.** `pg_dump` exits **zero** when it dumps an
  *empty* or *wrong* database — that is how you get a confident, regular, useless backup.
  So the script checks the artifact is a plausible size **and** that `pg_restore -l` shows
  `audit_log` table data in it. A dump with no audit ledger in it is refused: it means the
  DSN is pointing somewhere else (most likely the Supabase broker — §1).
- **The DSN travels in the environment, never in `pg_dump`'s argv** — argv is readable by
  every local user via `ps auxww` for the whole duration of the dump.
- It **fails closed** on: a missing DSN; a **set-but-empty** DSN (that means a secret
  lookup failed — falling back would dump the wrong database); no uploader (without
  `--local-only`); an unknown flag; an invalid `RETENTION_DAYS`; a failed, hung, empty or
  wrong-database dump; a failed upload. In every case: non-zero exit, **nothing uploaded,
  nothing pruned**. A cron that reports green while the bucket stays empty is the failure
  mode this whole script is written against, so **alert on the cron's exit code**.
- Pruning happens last, and only after a verified, uploaded dump — a bad day never deletes
  good snapshots.
- One run at a time (`flock`), so a hung dump plus tomorrow's cron cannot double up.

**Scheduling.** Secrets come from the environment; never commit them. Note two traps that
the obvious one-liner walks straight into: **a crontab command is a single line** (cron has
no backslash continuation), and **a failed secret lookup must abort**, not arrive as an
empty DSN. So go through a small wrapper:

```bash
#!/usr/bin/env bash
# /usr/local/bin/od-backup-cron.sh
set -euo pipefail                       # a failed `pass` aborts the run here...
BACKUP_DATABASE_URL="$(pass od/backup-dsn)"
export BACKUP_DATABASE_URL              # ...rather than reaching the backup empty
export BACKUP_UPLOAD_CMD='aws s3 cp "$1" s3://<bucket>/od-amrita/'
exec /path/to/api/scripts/pg_backup.sh
```

```cron
# /etc/cron.d/od-backup — one line, and alert on a non-zero exit
0 2 * * * od  /usr/local/bin/od-backup-cron.sh >> /var/log/od-backup.log 2>&1
```

> **The live schedule is not wired in this repo yet, on purpose.** The production host is
> not chosen (hosting is deferred), and a cron pointed at a DSN that does not exist yet
> would be dead weight that reports success. The script is ready; wiring the schedule is an
> operator step in §7.

---

## 4. Monitoring — `/health` + Uptime Robot

### 4.1 The `/health` contract (already live — do not change it)

`GET /health` is served by the API (shipped with the database foundation; unchanged by this
runbook). Implementation: `api/internal/handler/health/health.go`, routed in
`api/cmd/api/main.go`; covered by `api/test/health/health_test.go` and
`api/internal/handler/health/health_test.go`. It pings the runtime connection pool with a
2-second timeout and answers:

| Condition | Status | Body |
|---|---|---|
| DB reachable | **200** | `{"status":"ok","db":"up"}` |
| DB unreachable / ping timeout | **503** | `{"status":"degraded","db":"down"}` |

The route is registered outside the auth group — **no token is required**, which is what
lets an external monitor poll it.

> **The 503 is deliberate. Do not "fix" it to always return 200.** `/health` is a
> **readiness** probe: it fails when the API cannot actually serve requests. That is
> precisely the outage Uptime Robot exists to catch. An always-200 health check would keep
> the monitor green through a total database outage — a green light on a dead system is
> worse than no light at all. (The response shape is owned by the generated OpenAPI
> contract; changing it means regenerating the contract, never hand-editing.)

### 4.2 Uptime Robot monitor — configuration to create

| Field | Value | Confirmed |
|---|---|---|
| Monitor type | **HTTP(s)** (or *Keyword*) | ☐ |
| URL | `https://<deployed-host>/health` | ☐ |
| Keyword (if keyword monitor) | `ok` — present in the 200 body, absent from the 503 body (`degraded`) | ☐ |
| Alert condition | Non-200 response (a 503 **must** alert) | ☐ |
| Monitoring interval | 5 minutes (Uptime Robot free tier) | ☐ |
| Alert contacts | `______` (Shruhath + at least one Amrita IT contact — a monitor alerting one person who is asleep is not monitoring) | ☐ |
| Registered on | `______` (date) | ☐ |

> **Blocked on a deployed URL.** Hosting is deferred, so there is no public backend URL to
> point a monitor at yet. The config above is final; creating the monitor is an operator
> step (§7). If a staging/tunnel URL exists sooner, register against it and note it here as
> provisional.

---

## 5. Restore procedure (for standard Amrita IT)

Two layers, two restore paths. **Both end with the same verification.** Read §5.3 before
running either — it is the step that is easy to skip and guarantees a false failure.

### 5.1 Path A — PITR restore (recent damage, within the retention window)

Use when: a bad write / accidental delete happened **inside the PITR window** (§2.2) and you
want the database as it was at a specific second.

1. **Stop writing.** Take the API offline (or scale to zero) so no new rows land while you
   restore. Note the exact timestamp you want to recover to (UTC).
2. Neon console → project → **Branches → New branch → "Restore to a point in time"**
   (Neon models PITR as a branch created from history). Pick the timestamp.
3. Neon gives the new branch its **own connection URI**. Do **not** delete the damaged
   branch yet — keep it until you have verified the restored one.
4. **The roles already exist here — do NOT run the §5.3 bootstrap.** A PITR branch is a copy
   of the cluster, so `od_migrator` and `od_runtime` come with it, grants and all. Just
   confirm they are there:
   ```bash
   psql "<restored-branch-owner-uri>" -c '\du'   # expect od_migrator and od_runtime
   ```
   (§5.3's bootstrap is for Path B, where the target is a genuinely fresh database with no
   roles at all. Running it here is at best a no-op and at worst changes passwords out from
   under the DSNs you are about to use.)
5. Re-point the application: set `DATABASE_URL_RUNTIME` (and `DATABASE_URL_MIGRATOR`) to the
   restored branch's URI, in the environment only.
6. **Verify — §5.4** (with `RESTORE_DSN` = the restored branch's migrator URI).

### 5.2 Path B — logical restore from a 90-day `pg_dump` snapshot

Use when: the damage is **older than the PITR window**, or Neon itself is unavailable and you
are restoring onto a different host. This path needs nothing from the original provider but
the dump file.

> ### 🛑 Before you type anything: `pg_restore` restores into whatever DSN you hand it.
>
> `DATABASE_URL_MIGRATOR` points at the database you are restoring **from** — the live one.
> Pointing `pg_restore` at it replays an old dump **on top of production**, corrupting the
> very data you were called in to save. Every command below uses **`$RESTORE_DSN`**, which
> you set to the **new, empty target** in step 3. Set it once; never substitute the live DSN.

1. **Fetch the snapshot** from the storage bucket (§2.3). Artifacts are named
   `od-amrita-<UTC>.dump`; pick the one just *before* the damage.
   ```bash
   aws s3 cp s3://<bucket>/od-amrita/od-amrita-20260711T020000Z.dump .
   ```
2. **Create an empty target database.** A **new Neon project**, or any Postgres 17 host with
   a fresh, empty database. *Not* a Neon **branch** of the damaged project — a branch is a
   full copy of the data, not an empty database, and restoring into one puts you right back
   in the "already exists / duplicate rows" swamp. Never the branch you are restoring *from*.
3. **Point `RESTORE_DSN` at that new, empty database** — and check that it really is empty
   and really is not production:
   ```bash
   export RESTORE_DSN='<the NEW empty database's od_migrator URI>'
   psql "$RESTORE_DSN" -c '\dt'        # expect: "Did not find any relations."
   ```
   If that lists tables, **stop**: the target is not empty and is very possibly the live DB.
4. **Bootstrap the roles on the target — BEFORE restoring. See §5.3.**
5. **Restore.** `-j 4` parallelizes; drop it on a small host.
   ```bash
   pg_restore --dbname "$RESTORE_DSN" --no-comments --exit-on-error -j 4 \
       od-amrita-20260711T020000Z.dump
   ```
   **`--exit-on-error` is not optional.** Without it `pg_restore` logs failures, carries on,
   and exits 0 with a quiet `WARNING: errors ignored on restore: N` — so a restore that
   silently dropped a table's data looks exactly like a clean one, and §5.4's checks can
   still pass. **`pg_restore` must exit 0 and print no `errors ignored` line.**

   *If it aborts complaining that a **`role ... does not exist`**, you skipped step 4 — go to
   §5.3. If it complains that objects **already exist**, your target was not empty — go back
   to step 2/3; do not "push through" the errors.*
6. **Verify — §5.4.**

### 5.3 ⚠️ The role bootstrap — the step that makes or breaks the restore

**Symptom if you skip it:** the restore appears to succeed, then the application cannot
connect (or connects and is denied on every query), and IT reasonably concludes the backup
was corrupt. **It is not corrupt. The roles are missing.**

OD-Amrita uses **two database roles** (`api/db/init/00_roles.sql`):

- **`od_migrator`** — DDL owner. Owns the schema and every table; runs goose.
- **`od_runtime`** — the application pool. **DML only**, owns nothing, cannot `CREATE`/`ALTER`/
  `DROP`. This is what stops a compromised app credential from rewriting the audit ledger.

These roles are **cluster infrastructure, not schema** — they are *not* inside the dump and
*not* created by migrations. A **fresh** database has neither. (A Neon **PITR branch** does
have them — it is a copy of the cluster. This section is for **Path B only**; see §5.1 step 4.)

Do these four steps **in this order**, on the restore target, before `pg_restore`:

**1. Create the roles.** As the cluster's bootstrap/owner role (`$SUPERUSER_DSN` — see §1;
on Neon this is the project's `neondb_owner`, since Neon has no true superuser):

```bash
psql "$SUPERUSER_DSN" -f api/db/init/00_roles.sql
```

The file is idempotent (`IF NOT EXISTS`), so re-running it is safe.

**2. Change both passwords immediately — before anything else.**

> ⚠️ `00_roles.sql` creates the roles with **dev-only placeholder passwords that are
> committed to a public repository** (`migrator_dev_only_change_me`,
> `runtime_dev_only_change_me`). Until you replace them, two login roles with world-known
> passwords exist on a database that, on Neon, is reachable from the internet. Do not run
> the restore first and change them "after" — that leaves the window open for the entire
> duration of a multi-gigabyte `pg_restore`.

```bash
psql "$SUPERUSER_DSN" <<'SQL'
ALTER ROLE od_migrator PASSWORD '<new-migrator-secret>';
ALTER ROLE od_runtime  PASSWORD '<new-runtime-secret>';
SQL
```

**3. Rebuild the DSNs with the new passwords.** The `RESTORE_DSN` from §5.2 step 3 still
carries the placeholder password and will now fail to authenticate. Keep the secret **out of
the URI** — a password in a connection string ends up in your shell history and in `ps`
output; let libpq take it from the environment instead:

```bash
export PGPASSWORD='<new-migrator-secret>'                       # not in the URI
export RESTORE_DSN="postgresql://od_migrator@<restored-host>:5432/<db>"
psql "$RESTORE_DSN" -c 'SELECT current_user;'                   # must succeed before you go on
```

*(`unset PGPASSWORD` when the restore is finished. For anything long-lived, use a `~/.pgpass`
file with mode `0600` rather than an environment variable.)*

**4. Now run the restore** (§5.2 step 5). Afterwards, put the new runtime password into the
application's `DATABASE_URL_RUNTIME` — in the environment only, never in a file in the repo.

> **Why the ordering matters, and why the dump keeps its `GRANT`s.** `od_runtime`'s
> table-level DML grants are issued by the goose migrations (`0001`–`0004`). On a restored
> database, goose will **not** re-issue them — it sees the schema already at the latest
> version and does nothing. The grants therefore have to come from the **dump itself**, which
> is why `pg_backup.sh` deliberately does *not* pass `--no-owner`/`--no-privileges`. For those
> owner/grant statements to apply, the roles must **already exist** when `pg_restore` runs.
> Roles first, then restore. Reverse the order and you get a database the app cannot read.

### 5.4 Verify the restore (do not skip — this is the whole point)

0. **`pg_restore` exited 0** and printed no `errors ignored on restore` line (§5.2 step 5).
   If it did, the restore is partial — do not proceed.

1. **Schema parity.** The quickest check needs nothing but `psql` — read the migration
   version straight out of the table goose keeps:
   ```bash
   psql "$RESTORE_DSN" -c 'SELECT max(version_id) FROM goose_db_version;'
   # Expect the latest migration number (4 as of this writing). A missing table or a
   # lower number means you restored an empty/partial database — stop and investigate.
   ```
   *If the restore host happens to have the repo and a Go toolchain, `DATABASE_URL_MIGRATOR="$RESTORE_DSN" make -C api migrate-up`
   is the fuller check — it should apply nothing and print "no migrations to run". Do not go
   hunting for a Go toolchain mid-incident just to run it; the `psql` query above is enough.*

2. **The audit ledger came back** — it is the reason for all of this:
   ```sql
   SELECT count(*), max(created_at) FROM audit_log;
   SELECT count(*) FROM users;
   ```
   Compare against what you expect for the recovery point.

3. **The app can actually connect as `od_runtime`** (this is what §5.3 protects). Boot the
   API against the restored DSN and hit the health endpoint. **The API serves HTTPS on port
   8443 by default** (`API_PORT`, `TLS_ENABLED` — see `api/.env.example`); the dev
   certificate is self-signed, hence `-k`:
   ```bash
   curl -sk https://localhost:8443/health
   # → {"db":"up","status":"ok"}      ← 200/db:up means od_runtime connected and queried.
   ```
   *(If you run the API with `TLS_ENABLED=false` behind a TLS-terminating ingress, it is
   plain HTTP on the same port: `curl -s http://localhost:8443/health`.)*

   A `503 {"status":"degraded","db":"down"}` here means the app cannot reach or cannot use
   the restored database — almost always the missing role bootstrap (§5.3).
   **`curl: (7) Connection refused` is not a restore failure** — it means you are pointed at
   the wrong port or scheme. Check `API_PORT` before concluding anything about the backup.

4. **Record the outcome** in §7's drill log. A restore nobody wrote down is a restore nobody
   can repeat.

---

## 6. Pre-event verification hook

The pre-event dry-run checklist and the go-live gate both reference *"backup/monitoring live"* —
**these are the items they mean.** Before an event, confirm §7 has no unchecked boxes and
that the most recent snapshot in the bucket is < 24h old. Do not re-document these checks
elsewhere; point at this runbook.

---

## 7. Operator checklist (human-only steps — tracked, not dropped)

Everything below needs a console, an account, or a human decision. A code agent cannot do
any of it. Items blocked on the deferred hosting decision are marked **[blocked]** — they
are tracked here precisely so they are not silently forgotten at deploy time.

**Backup — layer 1 (PITR)**
- [x] Confirm the **PITR / history-retention window** on the **dev** Neon project and record the plan tier + window in §2.2. *(Done 2026-07-11: project `Amrita-OD`, **Free** tier, **6-hour** window, no scheduled snapshots.)*
- [ ] **Upgrade the app-DB Neon plan before the event** so the PITR window covers the event **and the days after it** when disputes surface (Free = 6h is not enough; Launch = 7d, Scale = 30d). Record the new window in §2.2.
- [ ] **[blocked: hosting]** Confirm PITR is enabled at the plan maximum on the **production** app DB once the host is chosen, and record it in §2.2.

- [ ] Record the **Supabase plan tier** in §2.4. If it is Free, the auth project has no
      built-in backups at all — confirm the reconstructability argument still holds.

**Backup — layer 2 (90-day logical snapshots)**
- [ ] Create the object-storage bucket for dumps; enable **encryption at rest**; record it in §2.3.
- [ ] Set a **bucket lifecycle rule to expire objects after 90 days** (the authoritative retention — §2.3).
- [ ] **[blocked: hosting]** Schedule `api/scripts/pg_backup.sh` daily (cron or CI) against the production DSN, with `BACKUP_UPLOAD_CMD` set, and **alert on non-zero exit**.
- [ ] Confirm a dump actually lands in the bucket, and that it is non-trivial in size.

**Monitoring**
- [ ] **[blocked: deployed URL]** Create the **Uptime Robot monitor** against `https://<deployed-host>/health` per §4.2 (alert on non-200 — the 503 must page someone).
- [ ] Add a second alert contact besides Shruhath (§4.2).

**The one that actually proves any of this works**
- [ ] **Run one real restore drill** (§5.2 → §5.4): restore a `pg_dump` snapshot into a throwaway database, bootstrap the roles, confirm goose reports schema parity and the API returns `{"status":"ok","db":"up"}` against it. **A documented-but-never-tested restore is not a restore.**
- [ ] Run the PITR restore drill (§5.1) as well, once the production host is settled.

**Drill log** — record every drill here, even the failed ones (especially those):

| Date | Path (PITR / pg_dump) | Restored into | Outcome | Notes |
|---|---|---|---|---|
| `______` | `______` | `______` | `______` | `______` |

---

## 8. Decisions recorded here (ratify or change)

| # | Decision | Why |
|---|---|---|
| **D1** | **Back up Neon (app DB); Supabase gets a light posture.** | Supabase is the auth broker; all OD data + the audit ledger are on Neon. Supabase identity is reconstructable from `supabase_subject` in Neon `users` + a re-login. §1, §2.4. |
| **D2** | **"90-day PITR" = PITR at host max + `pg_dump` @ 90 days.** | No managed host offers 90-day PITR (Neon and Supabase both cap at 30d). Two layers meet the intent; one layer cannot. §2.1. |
| **D3** | **`/health` is unchanged.** | It already returns system status, and its 503-on-DB-down is the correct readiness signal for the monitor. §4.1. |
| **D4** | **Hosting-blocked steps are tracked, not held open.** | Uptime Robot registration and prod PITR confirmation cannot be done before hosting exists. They are unchecked boxes in §7, actioned at deploy — rather than stalling the foundation on an external decision. |
