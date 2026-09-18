#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(cd "$(dirname "$0")/.." && pwd)
workspace=$(dirname "$root")
mode=${1:-run}
baseline=${2:-}
if [[ "$mode" != run && "$mode" != compare && "$mode" != repeat ]]; then
  printf 'Usage: bash testenv/run.sh run | repeat | compare BASELINE_IMAGE\n' >&2
  exit 2
fi
if [[ "$mode" == compare && -z "$baseline" ]]; then
  printf 'An explicit baseline image is required\n' >&2
  exit 2
fi
if [[ -n "${E2E_SNAPSHOT:-}" ]]; then
  if [[ "${CI:-}" == true || "${E2E_SNAPSHOT_SANITIZED:-}" != yes || ! -f "$E2E_SNAPSHOT" ]]; then
    printf 'Snapshots are local-only and require an existing sanitized archive and E2E_SNAPSHOT_SANITIZED=yes\n' >&2
    exit 2
  fi
  export E2E_SNAPSHOT="$(cd "$(dirname "$E2E_SNAPSHOT")" && pwd)/$(basename "$E2E_SNAPSHOT")"
fi
run_id="e2e-$(date +%s)-$$"
export E2E_ARTIFACTS="$root/artifacts/$run_id"
mkdir -p "$E2E_ARTIFACTS/bin"
architecture=$(docker info --format '{{.Architecture}}')
case "$architecture" in
  aarch64|arm64) architecture=arm64 ;;
  x86_64|amd64) architecture=amd64 ;;
  *) printf 'Unsupported Docker architecture\n' >&2; exit 2 ;;
esac
CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go -C "$workspace/ror-api" build -v -o "$E2E_ARTIFACTS/bin/api" ./cmd/api
CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go -C "$root" build -v -o "$E2E_ARTIFACTS/bin/runner" ./cmd/e2e
export E2E_RUNNER_IMAGE="ror-e2e-runner:$run_id"
candidate="ror-e2e-api:$run_id"
docker build -q -f "$root/testenv/Dockerfile" --build-arg BINARY=api -t "$candidate" "$E2E_ARTIFACTS/bin"
docker build -q -f "$root/testenv/Dockerfile" --build-arg BINARY=runner -t "$E2E_RUNNER_IMAGE" "$E2E_ARTIFACTS/bin"
export E2E_API_IMAGE="$candidate"
compose_files=(-f "$root/testenv/compose.yaml")
if [[ -n "${E2E_SNAPSHOT:-}" ]]; then
  compose_files+=(-f "$root/testenv/snapshot.yaml")
fi
compose() { docker compose -p "$run_id" "${compose_files[@]}" "$@"; }
cleanup() {
  local exit_code=$?
  if [[ "$exit_code" != 0 ]]; then
    compose logs --no-color > "$E2E_ARTIFACTS/startup.log" 2>&1 || true
    printf 'Run failed (%s). Logs: %s/startup.log\n' "$exit_code" "$E2E_ARTIFACTS" >&2
  fi
  printf '%s\n' "$exit_code" > "$E2E_ARTIFACTS/exit-code.txt"
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker image rm "$candidate" "$E2E_RUNNER_IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT
go version -m "$E2E_ARTIFACTS/bin/api" > "$E2E_ARTIFACTS/build.txt"
shasum -a 256 "$root/testenv/seed.js" "$root/testenv/oidc.json" "$root/testenv/scenarios/acl.json" > "$E2E_ARTIFACTS/fixtures.sha256"
if [[ -n "${E2E_SNAPSHOT:-}" ]]; then
  shasum -a 256 "$E2E_SNAPSHOT" > "$E2E_ARTIFACTS/snapshot.sha256"
fi
run_stack() {
  export E2E_API_IMAGE="$1"
  local label="$2"
  if [[ -n "${E2E_SNAPSHOT:-}" ]]; then
    compose run --rm restore
  fi
  compose up -d api
  docker image inspect "$E2E_API_IMAGE" --format '{{.Id}} {{json .RepoDigests}}' > "$E2E_ARTIFACTS/$label-image.txt"
  compose run --rm --no-deps --user "$(id -u):$(id -g)" runner run \
    -suite /scenarios/acl.json -target http://api:8080 -container \
    -ready http://api:9999/health/ready -oidc http://oidc:8080 \
    -label "$label" -out "/artifacts/$label.json"
  compose down --volumes --remove-orphans
}
run_stack "$candidate" candidate
if [[ "$mode" == repeat ]]; then
  baseline="$candidate"
fi
if [[ "$mode" == compare || "$mode" == repeat ]]; then
  run_stack "$baseline" baseline
  go -C "$root" run ./cmd/e2e compare -baseline "$E2E_ARTIFACTS/baseline.json" \
    -candidate "$E2E_ARTIFACTS/candidate.json" -out "$E2E_ARTIFACTS/diff.json"
fi
printf 'Reports: %s\n' "$E2E_ARTIFACTS"