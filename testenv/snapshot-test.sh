#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
export E2E_API_IMAGE=test E2E_RUNNER_IMAGE=test E2E_ARTIFACTS=/tmp E2E_SNAPSHOT=/tmp/synthetic.gz
docker compose -f "$root/testenv/compose.yaml" -f "$root/testenv/snapshot.yaml" config --quiet
if E2E_SNAPSHOT=/does-not-exist E2E_SNAPSHOT_SANITIZED=yes bash "$root/testenv/run.sh" run >/dev/null 2>&1; then
  printf 'Missing snapshot was accepted\n' >&2
  exit 1
fi
if [[ "${1:-}" != --restore && "${1:-}" != --e2e ]]; then
  exit 0
fi
umask 077
temporary=$(mktemp -d)
project="e2e-snapshot-$(date +%s)-$$"
export E2E_SNAPSHOT="$temporary/synthetic.gz"
compose() { docker compose -p "$project" -f "$root/testenv/compose.yaml" -f "$root/testenv/snapshot.yaml" "$@"; }
cleanup() {
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$temporary"
}
trap cleanup EXIT
compose up -d --wait mongodb
compose exec -T mongodb mongosh 'mongodb://test:synthetic-only@localhost:27017/admin' --quiet --eval '
  const target = db.getSiblingDB("nhn-ror");
  target.resourcesv2.insertOne({uid:"snapshot-probe", typemeta:{kind:"Config", apiversion:"e2e/v1"}, metadata:{name:"snapshot-probe"}});
  target.apikeys.insertOne({secret:"synthetic-must-not-restore"});
'
compose exec -T mongodb mongodump --uri='mongodb://test:synthetic-only@localhost:27017/?authSource=admin' --db=nhn-ror --archive --gzip > "$E2E_SNAPSHOT"
before=$(shasum -a 256 "$E2E_SNAPSHOT")
compose down --volumes --remove-orphans
compose run --rm restore
compose exec -T mongodb mongosh 'mongodb://test:synthetic-only@localhost:27017/admin' --quiet --eval '
  const target = db.getSiblingDB("nhn-ror");
  if (target.resourcesv2.countDocuments({uid:"snapshot-probe"}) !== 1) throw new Error("inventory not restored");
  if (target.apikeys.countDocuments({}) !== 0) throw new Error("credentials were restored");
'
if [[ "$before" != "$(shasum -a 256 "$E2E_SNAPSHOT")" ]]; then
  printf 'Source snapshot changed\n' >&2
  exit 1
fi
compose down --volumes --remove-orphans
if [[ "${1:-}" == --e2e ]]; then
  E2E_SNAPSHOT_SANITIZED=yes bash "$root/testenv/run.sh" repeat
fi
printf 'Synthetic snapshot restore checks passed\n'