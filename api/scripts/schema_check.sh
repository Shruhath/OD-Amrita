#!/usr/bin/env bash
# schema_check.sh — report the DEPLOYED schema state, read-only, over HTTPS.
#
# WHY IT SPEAKS HTTPS AND NOT POSTGRES
#   Neon serves Postgres on 5432 and the network this project is developed on
#   permits 80/443 outbound only, so psql cannot reach production from here at
#   all. Neon also exposes a SQL-over-HTTPS endpoint at https://<host>/sql, which
#   that network does allow. This script uses it so the one question you need
#   before and after a migration — "what is actually deployed?" — is answerable
#   from the machine you are standing at.
#
# WHAT IT IS NOT
#   It is not a migration tool and must never become one. Applying a migration is
#   goose's job, transactionally, with its own bookkeeping; hand-running DDL over
#   an HTTP endpoint is how a schema ends up half-applied. This script only READS.
#
# CREDENTIALS
#   Taken from the environment. Nothing is written to disk and no connection
#   string is ever echoed — the runbook's rule that a DSN must not land in a file
#   in this repository applies to this script's output too.
#
# USAGE
#   set -a; . ./.env; set +a          # or export DATABASE_URL_MIGRATOR yourself
#   api/scripts/schema_check.sh
set -euo pipefail

dsn="${DATABASE_URL_MIGRATOR:-${DATABASE_URL_RUNTIME:-}}"
if [ -z "$dsn" ]; then
  echo "Set DATABASE_URL_MIGRATOR (preferred) or DATABASE_URL_RUNTIME first." >&2
  echo "The migrator role is needed to read goose_db_version." >&2
  exit 1
fi

host="${dsn#*@}"
host="${host%%/*}"
if [ -z "$host" ] || [ "$host" = "$dsn" ]; then
  echo "Could not read a host out of the connection string." >&2
  exit 1
fi

# One round trip per question, so a permission failure on one does not hide the
# others. jq is not assumed; python3 is already required by the toolchain.
ask() {
  local sql="$1"
  python3 -c 'import json,sys; print(json.dumps({"query": sys.argv[1], "params": []}))' "$sql" \
    | curl -sS -m 30 -X POST "https://$host/sql" \
        -H "Neon-Connection-String: $dsn" \
        -H "Content-Type: application/json" \
        -d @- \
    | python3 -c '
import json, sys
answer = json.load(sys.stdin)
if "rows" not in answer:
    print("  ERROR", answer.get("code", ""), answer.get("message", ""))
    sys.exit(0)
if not answer["rows"]:
    print("  (no rows)")
for row in answer["rows"]:
    print("  " + "  ".join(f"{k}={v}" for k, v in row.items()))
'
}

echo "host: $host"
echo
echo "schema version (expect 26 after tonight):"
ask "SELECT max(version_id) AS version FROM goose_db_version"
echo
echo "pg_trgm (0025 needs it; only neondb_owner can install it):"
ask "SELECT count(*)::int AS installed FROM pg_extension WHERE extname = 'pg_trgm'"
echo
echo "roster search index (0025 — absent means the search is a sequential scan):"
ask "SELECT count(*)::int AS present FROM pg_indexes WHERE indexname = 'students_search_trgm_idx'"
echo
echo "live-capture reason guard (0026 — must NOT mention reason_note alone):"
ask "SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname = 'od_records_manual_reason_note_ck'"
echo
echo "rows the 0026 predicate would reject (must be 0 before applying):"
ask "SELECT count(*)::int AS violations FROM od_records WHERE NOT (capture_method IN ('nfc','camera') OR actor_role = 'coordinator' OR reason_note IS NOT NULL)"
echo
echo "admin live-capture marks (0 until 0026 is applied AND the api redeployed):"
ask "SELECT count(*)::int AS admin_live_marks FROM od_records WHERE actor_role = 'admin' AND capture_method IN ('nfc','camera')"
