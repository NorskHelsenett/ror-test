#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
export E2E_API_IMAGE=unused E2E_RUNNER_IMAGE=unused E2E_ARTIFACTS=/tmp
export DOCKER_DEFAULT_PLATFORM=linux/amd64
project="e2e-seed-$(date +%s)-$$"
compose() { docker compose -p "$project" -f "$root/testenv/compose.yaml" "$@"; }
cleanup() {
  local exit_code=$?
  if [[ "$exit_code" != 0 ]]; then
    compose logs --no-color seed >&2 || true
  fi
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
compose pull mongodb seed
for attempt in 1 2 3; do
  printf 'Fresh amd64 seed startup %s/3\n' "$attempt"
  compose up --abort-on-container-exit --exit-code-from seed seed
  compose up -d --wait mongodb
  compose exec -T mongodb mongosh 'mongodb://test:synthetic-only@127.0.0.1:27017/admin' --quiet --eval '
    const target = db.getSiblingDB("nhn-ror");
    if (target.acl.countDocuments({}) !== 4) throw new Error("expected four grants");
    if (target.apikeys.countDocuments({type:"Cluster"}) !== 2) throw new Error("expected two cluster keys");
    if (target.resourcesv2.countDocuments({}) !== 6) throw new Error("expected six resources");
  '
  compose down --volumes --remove-orphans
done
printf 'All fresh amd64 seed startups passed\n'