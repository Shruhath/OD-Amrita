#!/usr/bin/env bash
#
# pg_backup.sh — logical-snapshot backup for the OD-Amrita application database.
#
# WHAT THIS IS FOR
#   The app DB (Neon) holds every OD record and the immutable audit ledger. The
#   managed host's PITR window is short (Neon caps at 30 days); the reliability
#   requirement is 90-day recoverability. This script is the second layer: a
#   scheduled pg_dump logical snapshot, retained 90 days in object storage,
#   portable and provider-independent. PITR + these snapshots together are what
#   satisfy that requirement. See docs/backup-and-monitoring-runbook.md.
#
# USAGE
#   pg_backup.sh [--dry-run] [--local-only] [--help]
#
# ENVIRONMENT
#   BACKUP_DATABASE_URL  Required. Connection URI of the database to dump.
#                        If UNSET, falls back to DATABASE_URL_MIGRATOR — the owner
#                        role, which pg_dump needs in order to read every object.
#                        If SET BUT EMPTY this is a hard error, never a fallback:
#                        an empty value almost always means a secret lookup failed
#                        (e.g. `$(pass …)` under cron, which has no TTY), and
#                        falling back would quietly dump whatever OTHER database
#                        happens to be in the environment.
#                        NEVER hard-code a connection string in this file.
#   BACKUP_UPLOAD_CMD    Required unless --local-only. Shell command that ships the
#                        dump off-box; it receives the dump path as "$1". Without it
#                        the artifact only ever exists on the machine that ran the
#                        backup, which is not a backup — so its absence is an error,
#                        not a warning.
#   BACKUP_DIR           Local staging directory for the artifact. Default ./backups
#   RETENTION_DAYS       Prune local artifacts older than this. Default 90, minimum 1.
#   BACKUP_MIN_BYTES     Reject a dump smaller than this. Default 4096.
#   BACKUP_TIMEOUT_SECONDS  Kill a hung pg_dump after this long. Default 3600.
#
# WHY THE DUMP IS VERIFIED BEFORE IT IS TRUSTED
#   `set -e` only catches a pg_dump that exits NON-zero. A pg_dump pointed at an
#   empty database — or at the Supabase auth broker instead of the Neon app DB,
#   which is the single mistake the runbook exists to prevent — exits ZERO and
#   produces a small, perfectly valid, useless archive. So after dumping we check
#   the artifact is of plausible size AND that it actually contains audit_log data.
#   Only then is it uploaded and only then are old snapshots pruned.
#
# SCHEDULING
#   Run it daily from cron on any host that can reach the database, with the
#   secrets supplied by the environment (never by this file). NOTE: a crontab
#   command is a SINGLE line — cron has no backslash line-continuation — and a
#   failed secret lookup must abort rather than degrade, so go through a wrapper:
#
#     # /etc/cron.d/od-backup
#     0 2 * * * od  /path/to/od-backup-cron.sh >> /var/log/od-backup.log 2>&1
#
#   ...where od-backup-cron.sh is:
#
#     #!/usr/bin/env bash
#     set -euo pipefail                      # a failed `pass` aborts here...
#     BACKUP_DATABASE_URL="$(pass od/backup-dsn)"
#     export BACKUP_DATABASE_URL             # ...rather than reaching the backup empty
#     export BACKUP_UPLOAD_CMD='aws s3 cp "$1" s3://<bucket>/od-amrita/'
#     exec /path/to/api/scripts/pg_backup.sh
#
#   Alert on a non-zero exit. Every failure path here is non-zero on purpose: a
#   cron that reports green while the bucket stays empty is the failure mode this
#   whole script is written against.
#
# SAFETY
#   Rehearse with --dry-run: it validates the configuration, prints the exact plan,
#   and touches nothing — no dump, no upload, no delete.

set -euo pipefail

# Every artifact here is a full copy of the users table and the audit ledger.
# Without this, the ambient umask (commonly 022) leaves the dump world-readable on
# the backup host for the whole retention window.
umask 077

readonly DEFAULT_BACKUP_DIR="./backups"
readonly DEFAULT_RETENTION_DAYS=90
readonly DEFAULT_MIN_BYTES=4096
readonly DEFAULT_TIMEOUT_SECONDS=3600
readonly ARTIFACT_GLOB='od-amrita-*.dump'

usage() {
    cat <<'EOF'
pg_backup.sh — logical-snapshot (pg_dump) backup of the OD-Amrita application DB.

USAGE
  pg_backup.sh [--dry-run] [--local-only] [--help]

FLAGS
  --dry-run     Validate the configuration, print the plan, and exit. Runs no
                pg_dump, uploads nothing, deletes nothing, writes nothing. Use this
                to rehearse before a real run.
  --local-only  Permit a run with no BACKUP_UPLOAD_CMD, leaving the dump on this
                machine. Off by default: a dump that never goes off-box is not a
                backup, so its absence is normally a hard error rather than a
                warning nobody reads in a cron log.
  --help        Show this help and exit.

ENVIRONMENT
  BACKUP_DATABASE_URL  Required. Connection URI of the database to dump. If UNSET,
                       falls back to DATABASE_URL_MIGRATOR (the owner role — pg_dump
                       must be able to read every object). If SET BUT EMPTY, that is
                       a hard error, not a fallback — an empty value means a secret
                       lookup failed, and falling back would dump the wrong database.
                       Supplied by the environment only; no connection string is ever
                       stored in this repo.
  BACKUP_UPLOAD_CMD    Required unless --local-only. Ships the dump off-box; the dump
                       path is passed to it as "$1".
  BACKUP_DIR           Local staging dir for the artifact. Default ./backups
  RETENTION_DAYS       Prune local artifacts older than this many days. Default 90,
                       minimum 1 (there is no "disable prune" value — 0 would mean
                       "delete everything older than a day").
  BACKUP_MIN_BYTES     Reject a dump smaller than this many bytes. Default 4096.
  BACKUP_TIMEOUT_SECONDS  Kill a hung pg_dump after this long. Default 3600.

SCHEDULE
  Run daily via cron (or a CI schedule) through a wrapper that fetches the secret
  under `set -e` and execs this script — a crontab command is a single line, and a
  failed secret lookup must abort rather than reach the backup as an empty DSN.
  Alert on a non-zero exit. See the SCHEDULING section at the top of this file.

RESTORE
  These artifacts are pg_dump custom-format (-Fc), restored with pg_restore. The
  full procedure — including re-applying db/init/00_roles.sql, without which the
  restored DB refuses every application connection — is in
  docs/backup-and-monitoring-runbook.md.
EOF
}

# fail prints an actionable error and exits non-zero. Every failure path in this
# script goes through here: a backup tool that fails quietly is worse than none,
# because the cron stays green while the bucket stays empty.
fail() {
    echo "pg_backup: $*" >&2
    exit 1
}

dry_run=false
local_only=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            dry_run=true
            shift
            ;;
        --local-only)
            local_only=true
            shift
            ;;
        --help | -h)
            usage
            exit 0
            ;;
        *)
            # Fail closed on anything unrecognized. A mistyped `--dry-rnu` must never
            # be shrugged off and turned into a real run against production.
            echo "pg_backup: unknown flag: $1" >&2
            echo >&2
            usage >&2
            exit 2
            ;;
    esac
done

# ── Configuration ────────────────────────────────────────────────────────────
#
# Set-but-empty is treated as a failed secret lookup, NOT as "unset". `${VAR:-alt}`
# would silently accept an empty $(pass …) and fall through to DATABASE_URL_MIGRATOR
# — dumping the dev database under a production filename. `${VAR+x}` distinguishes
# the two cases.
if [[ -n "${BACKUP_DATABASE_URL+x}" ]]; then
    dsn="$BACKUP_DATABASE_URL"
    [[ -n "$dsn" ]] || fail "BACKUP_DATABASE_URL is set but EMPTY — refusing to run.
  An empty value almost always means the secret lookup failed (e.g. \`\$(pass …)\` under
  cron, which has no TTY). Falling back to DATABASE_URL_MIGRATOR here would back up
  whatever other database happens to be in this environment. Fix the secret lookup."
else
    dsn="${DATABASE_URL_MIGRATOR:-}"
    [[ -n "$dsn" ]] || fail "BACKUP_DATABASE_URL is not set (no DATABASE_URL_MIGRATOR fallback either).
  Set BACKUP_DATABASE_URL to the connection URI of the database to back up.
  Use the migrator/owner role — pg_dump must be able to read every object.
  Supply it from the environment; never commit it."
fi

backup_dir="${BACKUP_DIR:-$DEFAULT_BACKUP_DIR}"
retention_days="${RETENTION_DAYS:-$DEFAULT_RETENTION_DAYS}"
min_bytes="${BACKUP_MIN_BYTES:-$DEFAULT_MIN_BYTES}"
timeout_seconds="${BACKUP_TIMEOUT_SECONDS:-$DEFAULT_TIMEOUT_SECONDS}"
upload_cmd="${BACKUP_UPLOAD_CMD:-}"

[[ "$retention_days" =~ ^[0-9]+$ ]] || fail "RETENTION_DAYS must be a whole number of days, got: ${retention_days}"
# 0 is the value someone reaches for meaning "never prune"; to find(1) it means
# "-mtime +0" — delete everything older than 24 hours. Refuse it.
[[ "$retention_days" -ge 1 ]] || fail "RETENTION_DAYS must be at least 1, got: ${retention_days}
  (0 does NOT mean 'never prune' — to find(1) it means 'delete everything older than a day'.)"
[[ "$min_bytes" =~ ^[0-9]+$ ]] || fail "BACKUP_MIN_BYTES must be a whole number of bytes, got: ${min_bytes}"
[[ "$timeout_seconds" =~ ^[0-9]+$ ]] || fail "BACKUP_TIMEOUT_SECONDS must be a whole number of seconds, got: ${timeout_seconds}"

# A dump that never leaves this machine is not a backup. Absent an uploader we fail
# closed, so the cron's exit-code alert actually fires, instead of emitting a warning
# into a log nobody reads while the bucket stays empty for ninety days.
if [[ -z "$upload_cmd" && "$local_only" != true ]]; then
    fail "BACKUP_UPLOAD_CMD is not set — a dump that never leaves this machine is not a backup.
  Set it to a command that ships the artifact off-box; the dump path arrives as \"\$1\":
      BACKUP_UPLOAD_CMD='aws s3 cp \"\$1\" s3://<bucket>/od-amrita/'
  If you really do want a local-only dump (a drill, a manual export), pass --local-only."
fi

# UTC, sortable, collision-free. Without the timestamp every run overwrites the
# last one and "90 days of snapshots" quietly collapses into exactly one snapshot.
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
dump_path="${backup_dir}/od-amrita-${stamp}.dump"
# Dump to a .part file and rename only on success, so an interrupted run (disk full,
# SIGTERM from a cron timeout, Neon compute suspend) can never leave a truncated
# artifact that looks — by name, glob and mtime — exactly like a good one. Note
# ARTIFACT_GLOB does not match *.dump.part, so a corpse is never restored from,
# never uploaded and never counted.
dump_tmp="${dump_path}.part"

# The single source of truth for what pg_dump actually runs. The dry-run plan below
# PRINTS this array rather than restating it in prose: a plan that is a hand-written
# echo drifts from the command it claims to describe, and then the tests that assert
# on the plan are testing the advertisement instead of the product.
#
# -Fc  custom format: compressed, and the only format pg_restore can restore
#      selectively from. A plain-SQL dump forecloses the restore path the runbook
#      promises Amrita IT.
#
# Ownership and GRANTs are deliberately KEPT in the dump (no --no-owner, no
# --no-privileges). od_runtime's table-level DML grants are issued by the goose
# migrations, and goose will not re-issue them on a restored database — it sees the
# schema already at the latest version and does nothing. Strip the privileges here
# and the restore "succeeds" into a database the app cannot read. The restore
# procedure therefore bootstraps the roles (db/init/00_roles.sql) BEFORE
# pg_restore, so these owner/grant statements land on roles that exist.
pg_dump_args=(-Fc --file="$dump_tmp")

if [[ "$dry_run" == true ]]; then
    # Print the plan, touch nothing. Not even a mkdir: an operator rehearsing
    # against production must be able to trust that --dry-run has no side effects.
    # The DSN is deliberately shown as the variable name, never its value — this
    # output goes to a cron log.
    echo "pg_backup: DRY RUN — no dump, no upload, no delete, nothing written."
    echo
    echo "  would dump    : PGDATABASE=\$BACKUP_DATABASE_URL pg_dump ${pg_dump_args[*]}"
    echo "  artifact      : ${dump_path}  (written as ${dump_tmp}, renamed on success)"
    echo "  staging dir   : ${backup_dir}  (created if absent, mode 700)"
    echo "  would verify  : artifact >= ${min_bytes} bytes, and pg_restore -l shows audit_log data"
    if [[ -n "$upload_cmd" ]]; then
        echo "  would upload  : BACKUP_UPLOAD_CMD with the dump path as \$1"
    else
        echo "  would upload  : (nothing — --local-only; the dump would stay on this machine)"
    fi
    echo "  retention     : ${retention_days} days"
    echo "  would prune   : delete ${ARTIFACT_GLOB} in ${backup_dir} older than ${retention_days} days"
    echo "  timeout       : ${timeout_seconds} seconds"
    exit 0
fi

# pg_restore is needed to VERIFY the dump, not only to restore it. It ships in the
# same postgresql-client package as pg_dump, so requiring both costs nothing.
command -v pg_dump >/dev/null 2>&1 || fail "pg_dump is not on PATH — install the Postgres client tools (postgresql-client) on the host that runs this backup."
command -v pg_restore >/dev/null 2>&1 || fail "pg_restore is not on PATH — it is needed to verify the dump before trusting it (same postgresql-client package as pg_dump)."

mkdir -p "$backup_dir"

# One backup at a time. A hung dump plus tomorrow's cron would otherwise run two
# pg_dumps at once against the same database; two launches inside the same UTC second
# would also collide on one filename.
if command -v flock >/dev/null 2>&1; then
    exec 9>"${backup_dir}/.pg_backup.lock"
    flock -n 9 || fail "another pg_backup run is already in progress (lock held on ${backup_dir}/.pg_backup.lock).
  If yesterday's run hung, kill it — a backup that never exits never trips the cron's exit-code alert."
fi

# An interrupted run must not leave the partial artifact behind.
cleanup() { rm -f "$dump_tmp"; }
trap cleanup EXIT

echo "pg_backup: dumping to ${dump_path}"

# The DSN goes in the ENVIRONMENT, never in argv. libpq accepts a full connection
# URI as PGDATABASE. Passing it as a pg_dump argument would publish the password in
# /proc/<pid>/cmdline — readable by every local user via `ps auxww` — for the entire
# duration of the dump, which is the longest-running command of the day.
#
# A hung connection (a stalled TCP handshake to a waking Neon compute) would
# otherwise block forever: the job never exits, so the cron's exit-code alert never
# fires, and the backup outage is silent and unbounded.
timeout_status=0
PGDATABASE="$dsn" timeout "$timeout_seconds" pg_dump "${pg_dump_args[@]}" || timeout_status=$?
if [[ "$timeout_status" -eq 124 ]]; then
    fail "pg_dump timed out after ${timeout_seconds}s and was killed — the database was unreachable or too slow.
  Nothing was uploaded and nothing was pruned. Raise BACKUP_TIMEOUT_SECONDS if the dump legitimately takes longer."
elif [[ "$timeout_status" -ne 0 ]]; then
    fail "pg_dump failed (exit ${timeout_status}) — nothing uploaded, nothing pruned, partial artifact discarded."
fi

# ── Verify the artifact before trusting it ───────────────────────────────────
#
# A dump of an empty or WRONG database exits 0 and produces a small, valid, useless
# archive. Uploading it and then pruning good snapshots on the strength of it is how
# a backup system becomes a data-destruction system. These two checks are what turn
# "we dumped something" into "we dumped THE database".
bytes="$(wc -c <"$dump_tmp" | tr -d '[:space:]')"
if [[ "$bytes" -lt "$min_bytes" ]]; then
    fail "dump is only ${bytes} bytes (minimum ${min_bytes}) — refusing to upload it or prune anything.
  An almost-empty dump usually means the DSN points at the wrong (or an empty) database."
fi

if ! pg_restore -l "$dump_tmp" 2>/dev/null | grep -q 'TABLE DATA public audit_log'; then
    fail "the dump contains no audit_log table data — refusing to upload it or prune anything.
  The immutable audit ledger is the whole reason this backup exists. Its absence means the DSN
  is pointing at the wrong database — most likely the Supabase auth broker instead of the Neon
  app DB. See docs/backup-and-monitoring-runbook.md §1."
fi

mv "$dump_tmp" "$dump_path"
trap - EXIT

echo "pg_backup: dumped ${bytes} bytes to ${dump_path} (audit_log data present)"

if [[ -n "$upload_cmd" ]]; then
    echo "pg_backup: uploading ${dump_path}"
    # The dump path is "$1" inside BACKUP_UPLOAD_CMD, so the command can place it
    # anywhere in its own argument list (e.g. `aws s3 cp "$1" s3://bucket/`).
    # -euo pipefail applies INSIDE the uploader too: without it, a piped uploader
    # (`aws s3 cp … | tee log`) would mask its own failure behind tee's success and
    # we would prune good snapshots believing the new one had shipped.
    bash -euo pipefail -c "$upload_cmd" pg_backup_upload "$dump_path"
    echo "pg_backup: uploaded"
else
    echo "pg_backup: --local-only — artifact left in ${backup_dir}, NOT shipped off-box."
fi

# Prune last, and only after a successful, verified, uploaded dump: never delete an
# old good snapshot on a day when the new one failed.
#
# NOTE: this prunes only the LOCAL staging directory. The authoritative 90-day
# retention is a lifecycle rule on the storage bucket (runbook §2.3) — a script-only
# prune dies with the cron host.
echo "pg_backup: pruning local artifacts older than ${retention_days} days"
find "$backup_dir" -maxdepth 1 -type f -name "$ARTIFACT_GLOB" -mtime +"$retention_days" -print -delete

echo "pg_backup: done"
