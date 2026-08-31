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
set -euo pipefail

cd "$(dirname "$0")/.."
repo_root="$(cd .. && pwd)"

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
  "$engine" build -t "$image" -f - . <<'CONTAINERFILE'
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

failed=()
for job in "${jobs[@]}"; do
  echo "==> api-ci: $job"
  # --env-file/--secret-file come from the repository .actrc and are the reason a
  # local run cannot reach the production database. Do not override them here.
  if DOCKER_HOST="unix://$socket" act push \
      -C "$repo_root" \
      -W .github/workflows/api-ci.yml \
      -j "$job" \
      -P ubuntu-latest="$image" \
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
