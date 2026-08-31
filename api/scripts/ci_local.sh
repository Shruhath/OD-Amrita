#!/usr/bin/env bash
# ci_local.sh — run the api-ci workflow on this machine, with act.
#
# WHY THIS EXISTS
#   GitHub Actions is billing-blocked on this repository: every workflow fails in
#   about three seconds having run zero steps. So nothing gates a push. act runs
#   the same workflow file locally against the same job definitions, which is the
#   closest thing to the gate that is actually available.
#
#   It is written down as a script rather than left as a command someone remembers
#   because the invocation is not obvious: it needs the podman socket exported as
#   DOCKER_HOST, and it needs a runner image that act's default does not provide.
#
# THE RUNNER IMAGE
#   act's medium image (catthehacker/ubuntu:act-latest) ships neither shellcheck
#   nor psql, both of which real ubuntu-latest has and both of which api-ci uses.
#   Without them the build job dies at the shellcheck step and the integration job
#   cannot bootstrap roles. The alternative act offers is its 'large' image: 17 GB
#   of download for two apt packages.
#
#   So this builds a thin derived image once and reuses it. It is NOT a workflow
#   change: the gate still runs exactly what api-ci.yml says, with the tools the
#   hosted runner would have had.
#
# WHAT IT DOES NOT DO
#   It does not replace CI. Nothing runs it automatically, and it does not block
#   anything. Run it before a deploy.
#
# ⚠️ THE PART THAT NEARLY DESTROYED PRODUCTION, TWICE
#   act reads `.env` from its working directory BY DEFAULT and injects every
#   entry into the job container, where it OVERRIDES the `env:` block the
#   workflow declares. This repository's gitignored `.env` holds the live Neon
#   SUPERUSER_DSN, DATABASE_URL_MIGRATOR and DATABASE_URL_RUNTIME. api-ci's
#   integration job declares those three as its own throwaway postgres — so
#   unguarded, act points them at production and the job then runs, against the
#   database holding real attendance, the superuser role bootstrap, `goose up`,
#   and the reversibility gate's `goose down-to 0`. The last of those drops every
#   table.
#
#   The repository `.actrc` disarms this with --env-file=/dev/null. It is only
#   read from the CURRENT WORKING DIRECTORY, and the first version of this script
#   cd'd into api/ before calling act — so the guard was silently not in effect,
#   and the run reached `psql "$SUPERUSER_DSN" -f db/init/00_roles.sql` aimed at
#   Neon. It failed with "Connection refused" purely because this network blocks
#   outbound 5432. On a hotspot it would have succeeded.
#
#   Three defences now, and none of them may be removed:
#     1. act is invoked from the repository root, so .actrc applies.
#     2. --env-file and --secret-file are passed EXPLICITLY as well, so the guard
#        does not depend on a file being found.
#     3. The three credentials are stripped from this script's own environment
#        before act runs, so an operator who exported them (`. ./.env`) cannot
#        hand them over either.
#   Every value api-ci uses is a CI-only literal in the workflow file, so loading
#   nothing costs nothing.
set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

image="localhost/act-ubuntu-ci:latest"
engine="${CONTAINER_ENGINE:-podman}"
socket="${PODMAN_SOCKET:-/run/user/$(id -u)/podman/podman.sock}"

command -v act >/dev/null 2>&1 || {
  echo "act is not on PATH. Install it from https://nektosact.com and re-run." >&2
  exit 1
}

if [ ! -S "$socket" ]; then
  echo "No container socket at $socket." >&2
  echo "Start it with: systemctl --user start podman.socket" >&2
  echo "Or point PODMAN_SOCKET at a docker-compatible socket." >&2
  exit 1
fi

if ! "$engine" image exists "$image" 2>/dev/null; then
  echo "==> building the runner image (once): $image"
  "$engine" build -t "$image" -f - "$repo_root" <<'CONTAINERFILE'
FROM catthehacker/ubuntu:act-latest
# The two tools ubuntu-latest has and act's medium image does not. api-ci's build
# job shellchecks the operator scripts; its integration job bootstraps DB roles
# with psql. Adding them here keeps the workflow file honest.
RUN apt-get update \
 && apt-get install -y --no-install-recommends shellcheck postgresql-client \
 && rm -rf /var/lib/apt/lists/*
CONTAINERFILE
fi

jobs=("$@")
if [ ${#jobs[@]} -eq 0 ]; then
  jobs=(build acceptance integration)
fi

# Run FROM the repository root. `-C` sets act's workflow directory but NOT the
# directory it looks in for .actrc, and .actrc is what disarms the .env load.
cd "$repo_root"

failed=()
for job in "${jobs[@]}"; do
  echo "==> api-ci: $job"
  # --env-file/--secret-file are ALSO in .actrc. Repeated here on purpose: see the
  # header. A local run must not be able to reach the production database, and
  # that must not depend on a dotfile being picked up. Do not remove either copy.
  # `env -u` strips the credentials from what act inherits, for the same reason.
  if env -u SUPERUSER_DSN -u DATABASE_URL_MIGRATOR -u DATABASE_URL_RUNTIME \
      DOCKER_HOST="unix://$socket" act push \
      -W .github/workflows/api-ci.yml \
      -j "$job" \
      -P ubuntu-latest="$image" \
      --env-file=/dev/null \
      --secret-file=/dev/null \
      --pull=false; then
    echo "==> api-ci: $job PASSED"
  else
    echo "==> api-ci: $job FAILED"
    failed+=("$job")
  fi
done

if [ ${#failed[@]} -ne 0 ]; then
  echo "FAILED: ${failed[*]}" >&2
  exit 1
fi
echo "all requested api-ci jobs passed"
