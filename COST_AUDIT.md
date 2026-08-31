# Fly.io + NeonDB Cost & Infrastructure Audit

## Executive Summary

This is a repository-evidence audit, not a live billing audit. `flyctl`, Fly account state, Neon console state, actual secrets, active branches, and billing exports were not available in the workspace. Consequently, machine count and regional findings use the deployment runbook as the documented production state and are marked with the appropriate confidence.

The largest cost issue is intentional but currently unqualified: production is documented as two always-running `shared-cpu-1x` Fly machines, while each process keeps two warm Neon connections and Fly probes a database-backed `/health` endpoint every 15 seconds. With zero users, this produces at least four warm connections and 11,520 health requests/day, making Neon scale-to-zero unlikely and creating continuous cross-region activity. The second major issue is that Fly is documented in Singapore while Neon is documented in Virginia, increasing synchronous request and connection occupancy.

No active River worker loop, Fly volume, application timer, server-side polling loop, or cleanup job was found. Public reads have bounded per-process TTL/singleflight caching, result caps, rate limits, and a deliberately capped pool; those are good controls and should be retained unless measurements justify changes.

Financial ranking:

1. P0: always-on Fly posture and DB-backed probes/warm connections defeat idle behavior.
2. P1: cross-region synchronous traffic amplifies latency and resource occupancy.
3. P1: unconditional login updates and append-only historical data create avoidable WAL/storage growth.
4. P1/P2: query/index and retention risks become material as data volume grows.
5. P2: build-context and duplicated binary work increase deployment cost, not recurring runtime cost.

## Fly.io Cost Audit

### FLY-001 — Two always-on machines with no declarative scale-down policy

- Severity: P0
- Estimated cost impact: Direct fixed allocation cost for two `shared-cpu-1x` machines; the runbook estimates roughly $5–6/month combined, but current pricing and live count require verification.
- Confidence: High for documented configuration; medium for live state.
- Evidence: `api/fly.toml:18-24,105-110`; `docs/deployment-runbook.md:38-45,337-344`.
- Exact configuration: `primary_region = "sin"`, `[[vm]] size = "shared-cpu-1x"`; machine count is set imperatively by `flyctl scale count 2`.
- Current behavior: Two machines are maintained for redundancy; `auto_stop_machines`, `auto_start_machines`, `min_machines_running`, max count, and concurrency policy are absent.
- Why it costs money: both allocations continue during idle periods, and machine count can drift from source control.
- Recommended change: explicitly choose two always-on, one warm plus burst capacity, or scale-to-zero; encode/reconcile the decision through deployment automation and a periodic live-state audit.
- Expected impact: removes ambiguity and can eliminate most idle Fly allocation if cold starts are acceptable.
- Risk: fewer/auto-stopped machines reduce redundancy and may increase cold-start latency.
- Verification: `fly machines list`, `fly scale show`, allocation hours, and request latency after idle windows.

### FLY-002 — Database-backed health check runs continuously

- Severity: P1 for Neon interaction; P2 for direct Fly cost
- Estimated cost impact: 2 machines × 4 checks/minute × 60 × 24 = 11,520 health requests/day, plus DB connection activity; direct Fly request cost is small but the downstream Neon wake/compute effect can be material.
- Confidence: High.
- Evidence: `api/fly.toml:86-103`; `api/cmd/api/main.go:87`; `api/internal/handler/health/health.go:17-43`.
- Exact configuration: `/health`, interval `15s`, timeout `5s`, grace period `10s`; handler calls `db.Ping` with a two-second timeout.
- Current behavior: Fly continuously asks each machine to open/use a DB connection and removes machines from rotation when Neon is unavailable.
- Why it costs money: traffic exists with zero users and can keep/wake Neon compute; every probe also crosses the Fly↔Neon region boundary.
- Recommended change: separate process-only `/live` from DB-backed `/ready`; use readiness only where routing requires it and lengthen its interval if the SLO permits.
- Expected impact: makes idle Neon suspension possible and reduces periodic connection/network work.
- Risk: delayed failure detection or routing to an instance whose DB is unavailable.
- Verification: compare health request counts, `pg_stat_activity`, Neon compute active/suspended intervals, and Fly routing events.

### FLY-003 — Fly and Neon are documented in different regions

- Severity: P1
- Estimated cost impact: Indirect but potentially meaningful: longer request duration, more concurrent requests/connections, and more machine capacity needed for the same throughput.
- Confidence: High for documented state.
- Evidence: `api/fly.toml:18-24`; `docs/deployment-runbook.md:47-57,585-590`.
- Exact configuration: Fly `sin`; runbook records Neon in Virginia and a desired Southeast Asia region.
- Current behavior: synchronous reads/writes and health checks cross a long WAN path.
- Why it costs money: connection and machine resources remain occupied longer; retries/timeouts can amplify load.
- Recommended change: co-locate Neon with the selected Fly region, or deliberately move Fly after measuring both user and DB latency.
- Expected impact: lower p50/p95 latency, lower connection occupancy, and less pressure to scale.
- Risk: database migration/replication and regional availability tradeoffs.
- Verification: p50/p95 endpoint latency, DB RTT, active connections, CPU, and compute-hours before/after.

### FLY-004 — Missing `.dockerignore` and unnecessary binary compilation

- Severity: P2
- Estimated cost impact: build CPU/time, context transfer, and registry/build-cache churn; not a recurring VM charge.
- Confidence: High.
- Evidence: no root or `api/` `.dockerignore`; `api/Dockerfile:5-18,20-27`.
- Exact configuration: repository-root context is required, but the image builds both `./cmd/api` and `./cmd/worker`, then copies only one selected binary.
- Current behavior: unrelated frontend, VCS, dependency, and local artifact files may be sent as build context; every build compiles an unused entrypoint.
- Why it costs money: remote build bandwidth and builder CPU are spent on inputs/artifacts not used by the runtime image.
- Recommended change: add a carefully tested root `.dockerignore`; use separate build targets or compile only the selected binary while preserving required Go workspace files.
- Expected impact: lower deploy time and builder/transfer cost.
- Risk: over-excluding `go.work`, `contracts`, or required test workspace content can break builds.
- Verification: compare context bytes, build duration, builder CPU, image digest/layers, and successful API/worker builds.

### FLY-005 — Manual deploy and scaling permit resource drift

- Severity: P2
- Estimated cost impact: indirect; stale images, orphaned machines, or forgotten regions/volumes can create unplanned allocation.
- Confidence: High.
- Evidence: `docs/deployment-runbook.md:304-344`; `.github/workflows/api-ci.yml` has CI but no Fly deployment/reconciliation workflow.
- Exact operation: manual `flyctl deploy` and `flyctl scale count 2`.
- Current behavior: count, region, image, and cleanup depend on operator memory.
- Why it costs money: stale resources and repeated deploy failures consume allocation/build time.
- Recommended change: controlled deployment/reconciliation with image SHA, intended region/count, post-deploy checks, and stale-resource review.
- Expected impact: fewer orphaned allocations and repeat builds.
- Risk: production credentials and automation expand blast radius.
- Verification: reconcile live machines, volumes, regions, image digest, and monthly usage against policy.

### Fly items found to be optimized or absent

- Distroless static runtime and non-root user: `api/Dockerfile:20-27`.
- Only TLS/443 service is declared; no unnecessary port-80 redirect listener: `api/fly.toml:78-84`.
- Smallest documented VM class and no Fly volumes: `api/fly.toml:105-110`.
- Connection lifetime jitter reduces synchronized reconnect storms: `api/internal/store/pool.go:36-55`.
- No deployed Fly process group, cron, active River loop, or duplicate worker was found; the worker is only a skeleton (`api/cmd/worker/main.go:9-12`).

## Neon/Postgres Cost Audit

### NEON-001 — Warm pool connections and health activity defeat scale-to-zero

- Severity: P0
- Estimated cost impact: at least four warm runtime connections (2 machines × `MinConns=2`) plus 11,520 DB-backed probe requests/day; exact Neon dollars require billing data.
- Confidence: High.
- Evidence: `api/internal/store/pool.go:25-35,53-58`; `api/fly.toml:98-103`; `api/internal/handler/health/health.go:27-43`.
- Exact configuration: `MaxConns=10`, `MinConns=2`, `HealthCheckPeriod=30s` unless DSN overrides.
- Current behavior: each API process keeps warm connections and independently health-checks them even without users.
- Why it costs money: persistent connections and periodic pings oppose Neon compute suspension.
- Recommended change: for scale-to-zero deployments, test `MinConns=0` (or 1), remove redundant pool health checks, and separate `/live`/`/ready`.
- Expected impact: allows compute suspension and reduces idle connection/compute activity.
- Risk: cold connection/wake latency and less immediate failure detection.
- Verification: Neon active-hours/suspension, `pg_stat_activity`, connection counts by application, and cold-request latency.

### NEON-002 — Pooled-versus-direct endpoint is not enforced or documented

- Severity: P1
- Estimated cost impact: possible excess backend connections and ineffective pooling; magnitude cannot be determined without the secret hostname.
- Confidence: Medium.
- Evidence: `api/internal/config/config.go:132-159`; `api/internal/store/pool.go:20-22`; `api/.env.example:26-27`.
- Exact configuration: `DATABASE_URL_RUNTIME` is parsed directly with no validation of a Neon pooled endpoint.
- Current behavior: a direct or pooled DSN may be supplied, while the app pool is independently capped at ten per machine.
- Why it costs money: a direct endpoint can consume database connections inefficiently; stacking pools can obscure capacity and behavior.
- Recommended change: decide and document the endpoint policy, validate hostname shape at deploy time without logging credentials, and size the app pool against Neon limits.
- Expected impact: predictable connection usage and safer scale planning.
- Risk: changing endpoint semantics can affect transactions/session behavior.
- Verification: inspect secret metadata securely and compare `pg_stat_activity`/Neon pooler metrics.

### NEON-003 — Returning login unconditionally updates `users`

- Severity: P1
- Estimated cost impact: one UPDATE/WAL row version and index maintenance on every login/provisioning call, even when values do not change.
- Confidence: High.
- Evidence: `api/db/queries/users.sql:6-24`; `api/cmd/api/main.go:139-147`.
- Exact function/query: `ProvisionUser` uses `ON CONFLICT ... DO UPDATE` for `email`, `name`, `last_login_at`, and `updated_at`.
- Current behavior: `/auth/me` provisioning writes on every returning login.
- Why it costs money: PostgreSQL UPDATE creates a new row version and WAL, increasing I/O, vacuum, and storage churn.
- Recommended change: use `IS DISTINCT FROM` for identity fields and throttle/decouple `last_login_at` telemetry.
- Expected impact: fewer writes and less WAL/CPU on authenticated traffic.
- Risk: stale login telemetry or altered provisioning semantics.
- Verification: compare affected-row counts, WAL volume, dead tuples, and query calls before/after.

### NEON-004 — Append-only/history tables lack retention or archival policy

- Severity: P1
- Estimated cost impact: unbounded growth in Neon storage, indexes, backups, vacuum work, and future scan cost.
- Confidence: High.
- Evidence: `api/db/migrations/0001_audit_log.sql:19-28`; `0004_auth_session_revocations.sql:18-56`; `0009_students_roster.sql:40-64`; `api/internal/service/od_record/management.go:22-24`; no cleanup implementation; worker skeleton at `api/cmd/worker/main.go:9-12`.
- Exact data: `audit_log`, revocations, roster import history, and soft-removed OD rows.
- Current behavior: rows accumulate; no scheduled cleanup/partition/archive path exists.
- Why it costs money: storage and backup size grow; indexes/vacuum and eventual reads become more expensive.
- Recommended change: obtain data-owner retention requirements, then add bounded archival/partitioning/cleanup with monitoring.
- Expected impact: controls long-term storage and maintenance cost.
- Risk: deleting or altering audit/attendance history may violate compliance or product guarantees.
- Verification: table/index bytes, WAL, backup bytes/duration, dead tuples, and retention-job outcomes.

### NEON-005 — Audit and OD writes amplify WAL and index work

- Severity: P1
- Estimated cost impact: each changed auditable business write also inserts an audit row and updates multiple OD indexes; material during marking bursts.
- Confidence: High.
- Evidence: `api/internal/store/tx.go:116-149`; `api/db/queries/audit.sql:11-13`; `api/db/migrations/0011_od_records.sql:26-37`; `0016_od_record_od_date.sql:48-67`.
- Exact function/configuration: `Store.WithAudit` transaction and `od_records` partial unique plus supporting indexes.
- Current behavior: business change and audit row co-commit; each OD mutation maintains several indexes.
- Why it costs money: more WAL, random I/O, lock/index maintenance, and storage than the business row alone.
- Recommended change: preserve audit co-commit as required; measure index usage with `pg_stat_statements`/`EXPLAIN` before removing only demonstrably redundant indexes.
- Expected impact: potential write-cost reduction without weakening integrity.
- Risk: removing an index can regress reads or uniqueness guarantees.
- Verification: write latency, WAL bytes, index usage, buffer writes, and burst-load throughput.

### NEON-006 — Audit lookup lacks a supporting index

- Severity: P1 conditional on audit volume/read frequency
- Estimated cost impact: future full-table scans and sort work for audit reads; additional index write cost if added.
- Confidence: High.
- Evidence: `api/db/queries/audit_log.sql:1-5`; `api/db/migrations/0001_audit_log.sql:19-28`.
- Exact query: filters `target_table,target_id` and orders by `created_at`; migration only provides the primary-key index.
- Current behavior: lookup may scan the growing immutable ledger.
- Why it costs money: CPU, I/O, and longer-running connections rise with table size.
- Recommended change: test `CREATE INDEX ... (target_table,target_id,created_at,id)` only after realistic `EXPLAIN (ANALYZE, BUFFERS)` evidence.
- Expected impact: lower audit-read compute at scale.
- Risk: extra per-insert index maintenance and storage.
- Verification: plan, buffers, latency, and index write overhead.

### NEON-007 — Public cache is duplicated per machine and misses still query Neon

- Severity: P2
- Estimated cost impact: approximately one miss per key per machine per TTL window; two-machine bursts duplicate reads.
- Confidence: High.
- Evidence: `api/cmd/api/main.go:223-243`; `api/internal/service/publiccache/publiccache.go:77-149`; `docs/deployment-runbook.md:603-629`.
- Exact configuration: in-process TTL/singleflight caches; day-board default TTL `60s` in `api/internal/config/config.go:286-308`.
- Current behavior: each machine maintains its own cache; each miss can perform main plus category reads (`api/internal/service/publicattendance/service.go:357-370`).
- Why it costs money: duplicate Neon reads and compute during public bursts.
- Recommended change: keep local cache at two machines; measure hit rate before considering shared cache or event-driven invalidation.
- Expected impact: existing cache already collapses most burst traffic; future scale may justify a shared layer.
- Risk: Redis/shared cache adds cost, network hops, invalidation complexity, and another always-on dependency.
- Verification: instrument hit/miss by machine and compare Neon query calls/compute.

### NEON-008 — Query risks are workload-dependent, not immediate defects

- Severity: P2
- Estimated cost impact: conditional CPU/I/O growth as data volume rises.
- Confidence: Medium.
- Evidence: `api/db/queries/od_records.sql:373-390` (day board filters/order); `api/db/queries/students.sql:107-135` (intentional sequential roster search).
- Exact queries: optional predicates/order for day board; `lower(roll_no || ' ' || name)` search.
- Current behavior: result caps exist; roster comments defer `pg_trgm` until much larger data.
- Why it costs money: poor selectivity or scans can consume Neon compute at scale.
- Recommended change: run `EXPLAIN (ANALYZE, BUFFERS)` for whole-day and filtered boards; do not add `pg_trgm` until roster size/metrics justify it.
- Expected impact: targeted indexing only where it reduces measured cost.
- Risk: unnecessary indexes increase WAL/storage/write cost.
- Verification: plans, buffers, p95 query time, and `pg_stat_statements`.

## Fly.io ↔ Neon Interaction Audit

The critical feedback loop is:

```text
Fly machines stay allocated
  -> health checks hit /health every 15s
  -> /health pings Neon
  -> MinConns keeps warm DB sessions
  -> Neon remains active
  -> cross-region RTT lengthens each request
  -> connections and machine capacity remain occupied longer
```

The synchronous marking path has no queue to absorb WAN latency (`docs/deployment-runbook.md:47-50`), so co-location should be evaluated before increasing machine or pool size. A DB outage also makes Fly remove instances from rotation (`api/fly.toml:86-103`), coupling database availability to Fly routing.

## Background/Idle Activity Audit

| Activity | Trigger/frequency | Neon touch | Zero-user effect |
|---|---|---:|---|
| Fly `/health` | Every 15s per machine | `Ping` | Yes; strongest idle-cost driver |
| pgxpool minimums | Pool lifecycle | Warm connections | Yes; 2 per process |
| pgxpool health check | 30s configured period | Connection checks | Yes; verify implementation/need |
| Web polling | Implemented intervals are user-page activity (5s/60s) | Only while page is open | No zero-user activity, but can amplify active load |
| River worker | Skeleton prints only | No | No current DB activity |
| Cleanup/cron | None implemented | No | No current activity; retention risk |
| Backup/migrations | Manual/scheduled operational actions | Yes | Episodic, not idle baseline |

## Connection/Pool Audit

Positive controls: separate migrator/runtime DSNs (`api/internal/config/config.go:132-159`), `MaxConns=10`, lifetime jitter, lazy pool creation, and pool close on shutdown (`api/cmd/api/main.go:71-75`).

Main issue: `MinConns=2` × two documented machines creates four persistent connections; maximum application-side connections can reach 20 before any Neon pooler behavior. Size from observed concurrency, not defaults or redundancy assumptions.

## Query/Transaction Amplification Audit

### QUERY-001 — Authenticated live polling multiplies multi-query status work

- Severity: P1 during active marking sessions
- Estimated cost impact: the live screen can issue 12 status requests/minute/browser at its 5-second interval; each protected request can add revocation/RBAC reads and status/counter queries. Exact dollars require concurrency/query metrics.
- Confidence: High for behavior; medium for production magnitude.
- Evidence: `web/src/routes/portal/live/LivePortal.tsx:17-18,79-84`; `api/internal/middleware/auth/revocation_guard.go:24-51`; `api/internal/middleware/rbac/sqlc_loader.go:22-42`; `api/internal/service/marking/scope.go:130-151`; `api/internal/service/od_record/status.go:82-120`.
- Exact function/configuration: `LIVE_POLL_MS=5000`; live status requests run until an error and are not server-cached.
- Current behavior: active browsers repeatedly request status while authorization and status calculation perform multiple SQL operations.
- Why it costs money: polling multiplies Neon queries and keeps compute active during marking sessions.
- Recommended change: instrument statements/request first; then consider combining safe reads, short-lived scope caching, or a longer interval outside active slot windows. Add push only if measured concurrency justifies its cost.
- Expected impact: fewer Neon queries and lower active-session compute.
- Risk: stale counters or authorization if caching is too broad; push infrastructure adds cost.
- Verification: compare requests/browser, SQL calls/request, p95 status latency, Neon compute, and concurrent-marking correctness.

### QUERY-002 — Public attendance reads are bounded

- Severity: P2 positive control
- Estimated cost impact: avoids unbounded anonymous result reads and associated Neon CPU/network cost.
- Confidence: High.
- Evidence: `api/internal/service/publicattendance/service.go:37-60,160-178`; `api/db/migrations/0011_od_records.sql:26-36`; `api/db/migrations/0016_od_record_od_date.sql:60-66`.
- Exact function/configuration: public record/event caps and indexed attendance dimensions.
- Current behavior: public reads are bounded and use targeted indexes.
- Why it costs money: this is a cost-saving control; removing it could make anonymous requests scan/return history.
- Recommended change: retain and validate with query metrics.
- Expected impact: bounded Neon work and network response size.
- Risk: caps may omit data if product limits are too low.
- Verification: query plans, response sizes, and endpoint p95 under public load.

### QUERY-003 — Roster search is sequential-scan by design

- Severity: P2 conditional
- Estimated cost impact: currently negligible at the documented ~1,500-row roster; potentially meaningful if the roster grows substantially.
- Confidence: High.
- Evidence: `api/db/queries/students.sql:85-112` documents the non-btree multi-term `LIKE` expression and current roster size.
- Exact query: lowercased concatenated roll/name search with multiple terms.
- Current behavior: sequential scan is intentional and expected to be sub-millisecond at current scale.
- Why it costs money: future large scans could consume Neon CPU and buffers.
- Recommended change: do not add `pg_trgm` now; add/benchmark it only after size and query metrics justify it.
- Expected impact: avoids index storage/write cost before it provides value.
- Risk: delayed optimization if growth is not monitored.
- Verification: roster row count, `EXPLAIN (ANALYZE, BUFFERS)`, p95 search latency, and `pg_stat_statements`.

### QUERY-004 — Audit co-commit is intentional write amplification

- Severity: P1 by workload, but not a defect
- Estimated cost impact: one audit insert per changed auditable write plus transaction overhead; material during attendance bursts.
- Confidence: High.
- Evidence: `api/internal/store/tx.go:116-149`; `api/db/queries/audit.sql:11-13`.
- Exact function/configuration: `Store.WithAudit` commits the business write and audit row together; deduplicated no-ops do not create an audit row.
- Current behavior: changed writes produce two durable rows in one transaction.
- Why it costs money: additional WAL, row/index work, and transaction duration.
- Recommended change: preserve this invariant; optimize surrounding indexes/queries only with evidence rather than removing audit integrity.
- Expected impact: no direct reduction without changing requirements; preserves correctness while containing future optimizations.
- Risk: removing it breaks audit conservation and rollback guarantees.
- Verification: audit/business row conservation tests, WAL/write metrics, and burst throughput.

- Additional request amplification exists because protected requests perform revocation lookup (`api/internal/middleware/auth/revocation_guard.go:24-51`) and RBAC principal loading (`api/internal/middleware/rbac/sqlc_loader.go:22-42`) before business work.
- Public day cache misses execute multiple reads (`api/internal/service/publicattendance/service.go:357-370`).
- Login provisioning is the notable avoidable write amplification (`api/db/queries/users.sql:6-24`).

## Caching Audit

Public caches are bounded, TTL-configurable, singleflight-collapsing, and have HTTP cache headers (`api/internal/service/publiccache/publiccache.go`, `api/cmd/api/main.go:223-243`, `api/fly.toml:46-64`). This is appropriate for two machines. The tradeoff is duplicated cache state and misses per machine; a distributed cache is not justified without hit/miss and Neon-cost measurements.

## Polling/Realtime Audit

User-facing pages include polling while actively open: mark/live/event attendance surfaces use 5-second intervals and the public board uses 60 seconds. They stop on query errors in the inspected components. No server-side ticker, timer, or zero-user polling loop was found. Treat active-page polling as load amplification during events, not as a Neon idle-scale-to-zero blocker.

## Worker/Queue/Job Audit

`api/cmd/worker/main.go:9-12` is a skeleton that prints a message; `api/fly.toml` declares no process group. Local compose defines a separate worker, but no production worker cost is proven. Do not allocate a persistent Fly worker until it has real queue work. If implemented, isolate it as a separately sized/scaled process group and define idle behavior.

## Storage/Logging/Networking Audit

- Storage risk is dominated by unbounded audit/import/revocation/history data, not logs (`api/db/migrations/0001_audit_log.sql`, `0004`, `0009`).
- Cross-region network RTT is a recurring cost/latency amplifier.
- `api/scripts/pg_backup.sh` performs a full logical dump, upload, verification, and retention prune when scheduled; this is necessary operational work but should have a budget and monitored duration/bytes (`api/scripts/pg_backup.sh:281-344`).
- No Fly volumes or persistent application storage were found.

## Scaling/Autoscaling Audit

No autoscaling policy is declared. The documented two-machine redundancy posture is financially predictable but not elastic. Scale-to-zero requires coordinated changes: Fly lifecycle policy, cheap liveness/readiness design, `MinConns=0`, no periodic DB checks, and acceptance of Neon wake/cold-start latency.

## Deployment/CI/CD Cost Audit

`.github/workflows/api-ci.yml` validates/builds but does not deploy Fly. `.github/workflows/db-migrate.yml` runs multiple migration status/version operations against Neon during deployment; these are episodic and acceptable if intentional, but can be reduced if each connection is not needed. Backup scheduling is described but not proven active (`docs/backup-and-monitoring-runbook.md:4-10`). Manual deployment increases drift risk. Build context and dual binary compilation are avoidable build costs.

## Production Risks

- Neon compute may never suspend under the current health/pool behavior.
- Fly and Neon failure domains/regions are coupled by a DB-backed readiness check.
- Actual Fly machine/volume/branch state is unverified.
- Unbounded historical data can produce future storage, backup, vacuum, and query costs.
- Reducing pool minimums or health checks without measuring can increase cold-start latency and false readiness.
- Removing audit writes or indexes without product/plan evidence can damage integrity or performance.

## Findings Ranked by Financial Impact

| Rank | ID | Severity | Financial mechanism |
|---:|---|---|---|
| 1 | FLY-001 / NEON-001 | P0 | Always-on Fly allocation plus DB activity prevents idle behavior |
| 2 | FLY-002 | P1/P2 | 11,520 daily DB-backed probes at documented count |
| 3 | FLY-003 | P1 | Cross-region latency increases occupancy and capacity pressure |
| 4 | NEON-003 | P1 | Unnecessary UPDATE/WAL on every returning login |
| 5 | NEON-004 | P1 | Unbounded storage/index/backup/vacuum growth |
| 6 | NEON-005/006 | P1 | Write amplification and future audit scan cost |
| 7 | NEON-007/008 | P2 | Duplicate cache misses and conditional query CPU |
| 8 | FLY-004/005 | P2 | Build transfer/CPU and configuration drift |

## Recommended Changes

1. Measure current Fly machines, volumes, app regions, Neon branches/endpoints, compute-hours, connection counts, and billing.
2. Decide explicitly whether reliability requires two always-on machines. If not, test one warm/auto-stop behavior.
3. Add `/live` without DB access and separate DB-backed readiness; reduce readiness frequency.
4. Set/test `MinConns=0` (or 1) and revisit `HealthCheckPeriod` for scale-to-zero.
5. Co-locate Fly and Neon after measuring regional alternatives.
6. Make the runtime DSN pooled/direct policy explicit and size the app pool to measured concurrency.
7. Change login provisioning to avoid no-op updates and throttle last-login telemetry.
8. Establish retention/archival policy for audit, revocation, roster-import, and historical OD data.
9. Use `EXPLAIN`/`pg_stat_statements` to justify an audit lookup index and any OD index changes.
10. Add `.dockerignore`, avoid compiling unused binaries, and reconcile Fly state in controlled deployment automation.

## Things Already Optimized

- Runtime/migrator role separation and DML-only runtime design.
- Pool maximum cap, lifetime jitter, lazy connections, and shutdown close.
- Bounded TTL/singleflight public caching, HTTP cache headers, rate limits, and response caps.
- Distroless/non-root image, TLS-only external service, no Fly volume, no active worker/cron.
- Student upsert avoids unchanged-value updates (`api/db/queries/students.sql:8-25`).
- Audit payload is not separately indexed, avoiding unnecessary per-write index cost.

## Things NOT Worth Optimizing

- Do not remove audit co-commit; it is a core integrity guarantee.
- Do not introduce Redis solely for two machines and a small cache without measured duplication cost.
- Do not add `pg_trgm` to the documented ~1,500-row roster before workload evidence.
- Do not remove indexes or micro-optimize serialization/UUID work without query and billing data.
- Do not run cleanup that changes attendance/audit retention semantics without data-owner approval.

## Measurement Plan

Before/after each change, capture:

- Fly machine count, state, region, uptime, CPU/memory, request concurrency, health-check count, restarts, image digest, volumes.
- Neon compute active-hours/suspension intervals, storage bytes, branch/endpoint inventory, WAL, backup size/duration, and billed usage.
- `pg_stat_activity` by application/role, pool stats, and `pg_stat_statements` calls/time/blocks.
- Cache hit/miss by endpoint and machine.
- `EXPLAIN (ANALYZE, BUFFERS)` for audit lookup, whole-day/filtered board, roster search, and representative writes.
- Cold-request latency and error rate after idle/wake.

Success gates: zero-user Neon suspension is observable; no unacceptable cold-start/error regression; connection floor matches policy; p95 event-day latency and write throughput remain within SLO; storage/WAL trends improve; and monthly Fly/Neon spend is compared against the baseline.

## Final Architecture Recommendation

Adopt a measured hybrid posture: co-locate Fly and Neon, retain one warm API machine for availability during normal hours, allow a second machine to start for measured bursts, and keep the worker absent until it has real queue work. Separate process liveness from DB readiness, make readiness less frequent, and allow the runtime pool to reach zero idle connections if cold starts meet the SLO. Preserve bounded in-process public caching and audit co-commit. Add retention only after policy approval, and use query/index changes only when `EXPLAIN`, workload metrics, and billing data show a meaningful gain. Reassess whether two always-on machines are worth the small fixed cost after measuring actual incident/redundancy requirements.
