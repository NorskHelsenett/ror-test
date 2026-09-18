#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(cd "$(dirname "$0")/.." && pwd)
workspace=$(dirname "$root")
mode=${1:-run}
baseline=${2:-}
archive=${E2E_CANDIDATE_ARCHIVE:-}
candidate=${E2E_CANDIDATE_IMAGE:-}
if [[ -n "$archive" && -n "$candidate" ]]; then
  printf 'Choose either a candidate archive or a published image digest\n' >&2
  exit 2
fi
if [[ -n "$archive" ]]; then
  if [[ ! -f "$archive" || ! "${E2E_CANDIDATE_SHA256:-}" =~ ^sha256:[0-9a-f]{64}$ || ! "${E2E_CANDIDATE_DIGEST:-}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    printf 'Candidate archives require an existing file, E2E_CANDIDATE_SHA256 and E2E_CANDIDATE_DIGEST\n' >&2
    exit 2
  fi
  archive="$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")"
fi
if [[ -n "$candidate" && ! "$candidate" =~ ^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64}$ ]]; then
  printf 'Published candidates must use repository@sha256:<digest>, not a mutable tag\n' >&2
  exit 2
fi
if [[ -n "$archive$candidate" && ! "${E2E_CANDIDATE_PLATFORM:-}" =~ ^linux/(amd64|arm64)$ ]]; then
  printf 'Prebuilt candidates require E2E_CANDIDATE_PLATFORM=linux/amd64 or linux/arm64\n' >&2
  exit 2
fi
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
export E2E_RUNNER_IMAGE="ror-e2e-runner:$run_id"
export E2E_API_IMAGE="ror-e2e-api:$run_id"
owned_candidate=""
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
  if [[ -n "$owned_candidate" ]]; then docker image rm "$owned_candidate" >/dev/null 2>&1 || true; fi
  docker image rm "$E2E_RUNNER_IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
architecture=$(docker info --format '{{.Architecture}}')
case "$architecture" in
  aarch64|arm64) architecture=arm64 ;;
  x86_64|amd64) architecture=amd64 ;;
  *) printf 'Unsupported Docker architecture\n' >&2; exit 2 ;;
esac
export E2E_CANDIDATE_PLATFORM="${E2E_CANDIDATE_PLATFORM:-linux/$architecture}"
GOWORK=off go -C "$root" build -mod=readonly -o "$E2E_ARTIFACTS/bin/e2e-host" ./cmd/e2e
host_runner="$E2E_ARTIFACTS/bin/e2e-host"
expected_config=""
if [[ -n "$archive" ]]; then
  identities=$("$host_runner" inspect-candidate -archive "$archive" -checksum "$E2E_CANDIDATE_SHA256" \
    -digest "$E2E_CANDIDATE_DIGEST" -platform "$E2E_CANDIDATE_PLATFORM" -out "$E2E_ARTIFACTS/candidate-image.json")
  read -r manifest expected_config <<< "$identities"
  docker load -i "$archive"
  candidate="$manifest"
  if ! docker image inspect "$candidate" >/dev/null 2>&1; then
    candidate="$expected_config"
  fi
elif [[ -n "$candidate" ]]; then
  docker pull --platform "$E2E_CANDIDATE_PLATFORM" "$candidate"
else
  CGO_ENABLED=0 GOOS=linux GOARCH="${E2E_CANDIDATE_PLATFORM#linux/}" go -C "$workspace/ror-api" build -v -o "$E2E_ARTIFACTS/bin/api" ./cmd/api
  candidate="$E2E_API_IMAGE"
  owned_candidate="$candidate"
  docker build -q --platform "$E2E_CANDIDATE_PLATFORM" -f "$root/testenv/Dockerfile" --build-arg BINARY=api -t "$candidate" "$E2E_ARTIFACTS/bin"
  go version -m "$E2E_ARTIFACTS/bin/api" > "$E2E_ARTIFACTS/build.txt"
fi
actual_platform=$(docker image inspect "$candidate" --format '{{.Os}}/{{.Architecture}}')
config=$(docker image inspect "$candidate" --format '{{.Id}}')
if [[ "$actual_platform" != "$E2E_CANDIDATE_PLATFORM" || ( -n "$expected_config" && "$config" != "$expected_config" && "$config" != "$manifest" ) ]]; then
  printf 'Loaded candidate does not match verified platform/configuration\n' >&2
  exit 1
fi
docker image inspect "$candidate" --format '{"Id":{{json .Id}},"Os":{{json .Os}},"Architecture":{{json .Architecture}}}' > "$E2E_ARTIFACTS/candidate-runtime.json"
git -C "$root" rev-parse HEAD > "$E2E_ARTIFACTS/harness-commit.txt"
git -C "$root" status --porcelain --untracked-files=normal > "$E2E_ARTIFACTS/harness-worktree.txt"
CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" GOWORK=off go -C "$root" build -mod=readonly -o "$E2E_ARTIFACTS/bin/runner" ./cmd/e2e
docker build -q -f "$root/testenv/Dockerfile" --build-arg BINARY=runner -t "$E2E_RUNNER_IMAGE" "$E2E_ARTIFACTS/bin"
export E2E_API_IMAGE="$candidate"
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
  actual_config=$(docker inspect "$(compose ps -q api)" --format '{{.Image}}')
  if [[ "$label" == candidate && "$actual_config" != "$config" ]]; then printf 'Running API image differs from verified image\n' >&2; exit 1; fi
  docker image inspect "$E2E_API_IMAGE" --format '{{.Id}} {{json .RepoDigests}}' > "$E2E_ARTIFACTS/$label-image.txt"
  compose run --rm --no-deps --user "$(id -u):$(id -g)" runner run \
    -suite /scenarios/acl.json -target http://api:8080 -container \
    -ready http://api:9999/health/ready -oidc http://oidc:8080 \
    -label "$label" -out "/artifacts/$label.json"
  "$host_runner" verify-report -suite "$root/testenv/scenarios/acl.json" -report "$E2E_ARTIFACTS/$label.json" -out "$E2E_ARTIFACTS/$label-verified.json"
  compose down --volumes --remove-orphans
}
run_stack "$candidate" candidate
if [[ "$mode" == repeat ]]; then
  baseline="$candidate"
fi
if [[ "$mode" == compare || "$mode" == repeat ]]; then
  run_stack "$baseline" baseline
  "$host_runner" compare -baseline "$E2E_ARTIFACTS/baseline.json" \
    -candidate "$E2E_ARTIFACTS/candidate.json" -out "$E2E_ARTIFACTS/diff.json"
fi
printf 'Reports: %s\n' "$E2E_ARTIFACTS"