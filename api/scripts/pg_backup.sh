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
#   pg_backup.sh [--dry-run] [--help]
#
# ENVIRONMENT
#   BACKUP_DATABASE_URL  Required. Connection URI of the database to dump.
#                        Falls back to DATABASE_URL_MIGRATOR — the owner role,
#                        which pg_dump needs in order to read every object.
#                        NEVER hard-code a connection string in this file.
#   BACKUP_DIR           Local staging directory for the artifact. Default ./backups
#   RETENTION_DAYS       Prune artifacts older than this. Default 90
#   BACKUP_UPLOAD_CMD    Optional. Shell command that ships the dump off-box; it
#                        receives the dump path as "$1". Without it the artifact
#                        only ever exists on the machine that ran the backup,
#                        which is not a backup.
#
# SCHEDULING
#   Run it daily from cron on any host that can reach the database, with the
#   secrets supplied by the environment (never by this file):
#
#     0 2 * * *  BACKUP_DATABASE_URL="$(pass od/backup-dsn)" \
#                BACKUP_UPLOAD_CMD='aws s3 cp "$1" s3://<bucket>/od-amrita/' \
#                /path/to/api/scripts/pg_backup.sh >> /var/log/od-backup.log 2>&1
#
#   A CI schedule (GitHub Actions cron) works the same way. The live schedule is
#   deliberately NOT wired in this repo yet: the production host is not chosen, and
#   a cron pointing at a DSN that does not exist is dead weight that reports green.
#   Wire it when hosting lands — the runbook tracks that as an operator step.
#
# SAFETY
#   Rehearse with --dry-run: it prints the exact plan and touches nothing — no
#   dump, no upload, no delete.

set -euo pipefail

readonly DEFAULT_BACKUP_DIR="./backups"
readonly DEFAULT_RETENTION_DAYS=90

usage() {
    cat <<'EOF'
pg_backup.sh — logical-snapshot (pg_dump) backup of the OD-Amrita application DB.

USAGE
  pg_backup.sh [--dry-run] [--help]

FLAGS
  --dry-run   Print the plan and exit. Runs no pg_dump, uploads nothing, deletes
              nothing, writes nothing. Use this to rehearse before a real run.
  --help      Show this help and exit.

ENVIRONMENT
  BACKUP_DATABASE_URL  Required. Connection URI of the database to dump. Falls back
                       to DATABASE_URL_MIGRATOR (the owner role — pg_dump must be
                       able to read every object). Supplied by the environment only;
                       no connection string is ever stored in this repo.
  BACKUP_DIR           Local staging dir for the artifact. Default ./backups
  RETENTION_DAYS       Prune artifacts older than this many days. Default 90
  BACKUP_UPLOAD_CMD    Optional. Shell command that ships the dump off-box; the dump
                       path is passed to it as "$1". A dump that never leaves the
                       machine is not a backup.

SCHEDULE
  Run daily via cron (or a CI schedule) with the secrets injected from the
  environment. Example crontab entry:

    0 2 * * *  BACKUP_DATABASE_URL="$(pass od/backup-dsn)" \
               BACKUP_UPLOAD_CMD='aws s3 cp "$1" s3://<bucket>/od-amrita/' \
               /path/to/api/scripts/pg_backup.sh >> /var/log/od-backup.log 2>&1

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
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            dry_run=true
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

dsn="${BACKUP_DATABASE_URL:-${DATABASE_URL_MIGRATOR:-}}"
[[ -n "$dsn" ]] || fail "BACKUP_DATABASE_URL is not set (no DATABASE_URL_MIGRATOR fallback either).
  Set BACKUP_DATABASE_URL to the connection URI of the database to back up.
  Use the migrator/owner role — pg_dump must be able to read every object.
  Supply it from the environment; never commit it."

backup_dir="${BACKUP_DIR:-$DEFAULT_BACKUP_DIR}"
retention_days="${RETENTION_DAYS:-$DEFAULT_RETENTION_DAYS}"
upload_cmd="${BACKUP_UPLOAD_CMD:-}"

[[ "$retention_days" =~ ^[0-9]+$ ]] || fail "RETENTION_DAYS must be a whole number of days, got: ${retention_days}"

# UTC, sortable, collision-free. Without the timestamp every run overwrites the
# last one and "90 days of snapshots" quietly collapses into exactly one snapshot.
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
readonly ARTIFACT_GLOB='od-amrita-*.dump'
dump_path="${backup_dir}/od-amrita-${stamp}.dump"

if [[ "$dry_run" == true ]]; then
    # Print the plan, touch nothing. Not even a mkdir: an operator rehearsing
    # against production must be able to trust that --dry-run has no side effects.
    echo "pg_backup: DRY RUN — no dump, no upload, no delete, nothing written."
    echo
    echo "  would dump    : pg_dump -Fc \"\$BACKUP_DATABASE_URL\" -f ${dump_path}"
    echo "  artifact      : ${dump_path}  (custom format, so pg_restore can restore selectively)"
    echo "  staging dir   : ${backup_dir}  (created if absent)"
    if [[ -n "$upload_cmd" ]]; then
        echo "  would upload  : BACKUP_UPLOAD_CMD with the dump path as \$1"
    else
        echo "  would upload  : (nothing — BACKUP_UPLOAD_CMD is unset; the dump would stay on this machine only)"
    fi
    echo "  would prune   : delete ${ARTIFACT_GLOB} in ${backup_dir} older than ${retention_days} days (retention: ${retention_days} days)"
    echo
    echo "Retention is ${retention_days} days by default: no managed Postgres host offers 90-day PITR,"
    echo "so these logical snapshots are what carry recoverability out to 90 days."
    exit 0
fi

command -v pg_dump >/dev/null 2>&1 || fail "pg_dump is not on PATH — install the Postgres client tools (postgresql-client) on the host that runs this backup."

mkdir -p "$backup_dir"

echo "pg_backup: dumping to ${dump_path}"
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
pg_dump -Fc "$dsn" -f "$dump_path"

# `set -e` means we only reach here if pg_dump succeeded, so we never upload a
# truncated artifact and never prune good ones on the strength of a failed dump.
echo "pg_backup: dumped $(du -h "$dump_path" | cut -f1) to ${dump_path}"

if [[ -n "$upload_cmd" ]]; then
    echo "pg_backup: uploading ${dump_path}"
    # The dump path is "$1" inside BACKUP_UPLOAD_CMD, so the command can place it
    # anywhere in its own argument list (e.g. `aws s3 cp "$1" s3://bucket/`).
    bash -c "$upload_cmd" pg_backup_upload "$dump_path"
    echo "pg_backup: uploaded"
else
    echo "pg_backup: BACKUP_UPLOAD_CMD unset — artifact left in ${backup_dir} only (NOT off-box; this is not yet a backup)" >&2
fi

# Prune last, and only after a successful dump: never delete an old good snapshot
# on a day when the new one failed.
echo "pg_backup: pruning local artifacts older than ${retention_days} days (retention: ${retention_days} days)"
find "$backup_dir" -maxdepth 1 -type f -name "$ARTIFACT_GLOB" -mtime +"$retention_days" -print -delete

echo "pg_backup: done"
