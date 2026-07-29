#!/usr/bin/env bash
#
# smoke.sh — post-deploy smoke check for the OD-Amrita API.
#
# WHAT THIS IS FOR
#   It answers the one question a deploy cannot answer for itself: did the thing
#   that just went out actually work? Two assertions, both of which have failed
#   silently in real deployments:
#
#     1. /health reports the database is UP. A 200 alone is not enough — the API
#        boots fine with an unreachable database and answers 503
#        {"status":"degraded","db":"down"}. That response means the od_runtime
#        pool never reached Neon, which is precisely the failure worth catching
#        before an event rather than during one.
#
#     2. Plain HTTP is REFUSED, not redirected (NFR15). The transport rule is
#        that an insecure request is not ANSWERED and not REDIRECTED; a 301/308
#        redirect still accepts the connection first. The host config in
#        api/fly.toml achieves this by exposing port 443 only, so nothing serves
#        HTTP on 80. What that looks like on the wire depends on who owns the IP:
#        a directly-bound host REFUSES the connection (curl exit 7), while a
#        TLS-terminating edge on a shared Anycast IP (Fly, and most CDNs/ingress)
#        ACCEPTS the port-80 TCP connection and then drops it with no data —
#        empty reply (curl 52) or reset (curl 56), http_code 000. All of those
#        satisfy NFR15 equally: no HTTP response came back. The failure this
#        check exists to catch loudly is any HTTP status line at all — above all
#        a redirect — which means somebody re-added a port-80 handler or switched
#        the service block to [http_service]/force_https.
#
# WHERE IT RUNS
#   OPERATOR-RUN, against the LIVE deployed origin — during the deploy and again
#   in the pre-event dry run. It is deliberately NOT a CI job: CI has no
#   deployed URL, so a pipeline wired around this script could only pass
#   vacuously or fail for the wrong reason. Do not add it to a workflow.
#
# USAGE
#   BASE_URL=https://od-amrita-api.fly.dev api/scripts/smoke.sh
#
# ENVIRONMENT
#   BASE_URL  Required. The deployed HTTPS origin, scheme included, no trailing
#             path. Never hard-coded here — the same check has to work against a
#             re-created app or a replacement host.
#
# EXIT STATUS
#   0  both checks passed
#   1  at least one check failed (details on stderr)
#   2  the script was invoked wrong (missing or malformed BASE_URL)
#
# SEE ALSO
#   docs/deployment-runbook.md          the deploy procedure and operator steps
#   docs/backup-and-monitoring-runbook.md  §5.4 the restore-verify /health check

set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: BASE_URL=https://<deployed-host> api/scripts/smoke.sh

Verifies a deployed OD-Amrita API:
  1. GET <BASE_URL>/health returns a body reporting status ok and db up.
  2. Plain http:// on the same host is refused, not redirected (NFR15).
USAGE
}

case "${1:-}" in
  -h | --help)
    usage
    exit 0
    ;;
  "") ;;
  *)
    printf 'smoke.sh: unknown argument %s\n\n' "$1" >&2
    usage >&2
    exit 2
    ;;
esac

if [ -z "${BASE_URL:-}" ]; then
  printf 'smoke.sh: BASE_URL is required (the deployed HTTPS origin).\n\n' >&2
  usage >&2
  exit 2
fi

# Trailing slashes are the most common paste error and would produce
# "…//health"; strip them rather than failing an operator over punctuation.
base_url="${BASE_URL%"${BASE_URL##*[!/]}"}"

case "$base_url" in
  https://*) ;;
  *)
    printf 'smoke.sh: BASE_URL must be an https:// origin, got %s\n' "$base_url" >&2
    printf '  The whole point of check 2 is that the http:// form is refused.\n' >&2
    exit 2
    ;;
esac

# Reject a BASE_URL that carries a path. Check 1 builds "$base_url/health" while
# check 2 rebuilds the host from scratch, so a path here would make the two
# checks probe different URLs — and "$base_url/health" could 404 a healthy
# deploy (e.g. https://host/api -> https://host/api/health). Origin only.
case "${base_url#https://}" in
  */*)
    printf 'smoke.sh: BASE_URL must be an origin with no path, got %s\n' "$base_url" >&2
    printf '  Pass just the scheme and host, e.g. https://od-amrita-api.fly.dev\n' >&2
    exit 2
    ;;
esac

# Host for check 2's plain-HTTP probe: scheme stripped, and any explicit :port
# dropped so the probe targets port 80. NFR15 is a claim about port 80 — probing
# http://host:443 would test the TLS port in cleartext and pass vacuously. A
# bracketed IPv6 literal keeps its brackets.
host="${base_url#https://}"
http_host="$host"
case "$host" in
  \[*\]) ;;                        # IPv6 literal, no port — leave it
  *\]:*) http_host="${host%:*}" ;; # IPv6 literal with a port — drop the port
  *:*)   http_host="${host%:*}" ;; # host:port — drop the port
esac

failures=0

pass() { printf '  PASS  %s\n' "$1"; }

fail() {
  printf '  FAIL  %s\n' "$1" >&2
  failures=$((failures + 1))
}

printf 'Smoke-checking %s\n\n' "$base_url"

# ── Check 1 — /health reports the database is up ────────────────────────────
printf 'Check 1: GET %s/health\n' "$base_url"

health_body=""
health_exit=0
health_body=$(curl -fsS --max-time 15 "$base_url/health" 2>&1) || health_exit=$?

if [ "$health_exit" -ne 0 ]; then
  fail "GET $base_url/health did not return a 2xx (curl exit $health_exit)."
  printf '        curl said: %s\n' "$health_body" >&2
  printf '        A 503 here is the API telling you it cannot reach the database:\n' >&2
  printf '        check the DATABASE_URL_RUNTIME secret and that the roles exist.\n' >&2
else
  # Whitespace-insensitive and key-order-insensitive: the handler is free to
  # serialize {"db":"up","status":"ok"} in either order.
  compact="${health_body//[[:space:]]/}"

  case "$compact" in
    *'"status":"ok"'*) pass 'body reports status ok' ;;
    *) fail "body does not report status ok — got: $health_body" ;;
  esac

  case "$compact" in
    *'"db":"up"'*) pass 'body reports db up (the runtime pool reached the database)' ;;
    *)
      fail "body does not report db up — got: $health_body"
      printf '        db down means the pool never connected. The deploy is NOT good.\n' >&2
      ;;
  esac
fi

# ── Check 2 — plain HTTP is refused, not answered (NFR15) ───────────────────
printf '\nCheck 2: plain HTTP on %s must be REFUSED, not answered\n' "$http_host"

http_code=""
http_exit=0
http_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "http://${http_host}/health" 2>/dev/null) || http_exit=$?

if [ "$http_exit" -eq 0 ]; then
  # curl got an HTTP response, so SOMETHING is listening on port 80 and answered
  # in cleartext. That fails NFR15 whatever the status: a 200/404/502 accepts and
  # serves the insecure request exactly as a 301/308 redirect accepts it. Only a
  # refused or unanswered connection passes.
  case "$http_code" in
    3??)
      fail "plain HTTP was REDIRECTED with $http_code — NFR15 forbids a redirect; it still accepts the insecure connection before pointing at the secure one."
      ;;
    *)
      fail "plain HTTP was ANSWERED with $http_code — something is listening on port 80. NFR15 requires plain HTTP to hit NO listener and be refused."
      ;;
  esac
  printf '        Check api/fly.toml (§4.1): a port-80 entry, a plain http handler, or force_https has been re-added.\n' >&2
elif [ "$http_exit" -eq 7 ]; then
  # A directly-bound host with the port-443-only service block: nothing is
  # listening on 80, so the connection is refused before any HTTP is spoken.
  pass 'connection refused (curl exit 7) — nothing is listening on port 80'
elif [ "$http_exit" -eq 52 ] || [ "$http_exit" -eq 56 ]; then
  # A TLS-terminating edge (Fly's shared Anycast IP, and most CDNs/ingress) owns
  # port 80 at the IP even with no port-80 service mapped: it accepts the TCP
  # connection and then closes it with no data (curl 52, empty reply) or resets
  # it (curl 56). http_code is 000 — no status line, no Location — so plain HTTP
  # was neither answered nor redirected, which is what NFR15 requires. This, not
  # exit 7, is the compliant result for an edge-terminated deploy like api/fly.toml.
  pass "no HTTP response (curl exit $http_exit) — the edge accepted then dropped the cleartext connection without answering"
elif [ "$http_exit" -eq 28 ]; then
  # A DROP firewall swallows the SYN, so curl times out rather than getting a
  # refusal. Nothing was answered, which is what NFR15 requires.
  pass 'no response before timeout (curl exit 28) — plain HTTP is not answered'
else
  # Any other curl error (DNS failure 6, connect error, …) proves nothing about
  # port 80. Do NOT read it as a satisfied security property — it is inconclusive.
  fail "could not probe plain HTTP (curl exit $http_exit) — INCONCLUSIVE, not a pass. Check the host is reachable, then re-run."
fi

printf '\n'
if [ "$failures" -gt 0 ]; then
  printf 'SMOKE CHECK FAILED — %d assertion(s) failed against %s\n' "$failures" "$base_url" >&2
  printf 'Do not record this deploy as good. See docs/deployment-runbook.md.\n' >&2
  exit 1
fi

printf 'SMOKE CHECK PASSED — %s serves HTTPS, reports db up, and refuses plain HTTP.\n' "$base_url"
