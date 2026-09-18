#!/usr/bin/env bash
set -euo pipefail
umask 077
root=$(cd "$(dirname "$0")/.." && pwd)
workspace=$(dirname "$root")
temporary=$(mktemp -d)
cleanup() { rm -rf "$temporary"; }
trap cleanup EXIT
case "$(docker info --format '{{.Architecture}}')" in
  arm64|aarch64) architecture=arm64 ;;
  amd64|x86_64) architecture=amd64 ;;
  *) exit 1 ;;
esac
export E2E_CANDIDATE_PLATFORM="linux/$architecture"
CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go -C "$workspace/ror-api" build -o "$temporary/api" ./cmd/api
docker buildx build --platform "$E2E_CANDIDATE_PLATFORM" --provenance=false \
  -f "$root/testenv/Dockerfile" --build-arg BINARY=api \
  --output "type=oci,dest=$temporary/candidate.tar" "$temporary"
export E2E_CANDIDATE_ARCHIVE="$temporary/candidate.tar"
export E2E_CANDIDATE_SHA256="sha256:$(shasum -a 256 "$E2E_CANDIDATE_ARCHIVE" | awk '{print $1}')"
export E2E_CANDIDATE_DIGEST="sha256:$(tar -xOf "$E2E_CANDIDATE_ARCHIVE" index.json | shasum -a 256 | awk '{print $1}')"
if E2E_CANDIDATE_SHA256="sha256:$(printf '%064d' 0)" bash "$root/testenv/run.sh" run; then
  printf 'Tampered archive checksum was accepted\n' >&2
  exit 1
fi
COMPOSE_PROGRESS=plain bash "$root/testenv/run.sh" repeat
test "$E2E_CANDIDATE_SHA256" = "sha256:$(shasum -a 256 "$E2E_CANDIDATE_ARCHIVE" | awk '{print $1}')"
GOWORK=off go -C "$root" run ./cmd/e2e inspect-candidate -archive "$E2E_CANDIDATE_ARCHIVE" \
  -checksum "$E2E_CANDIDATE_SHA256" -digest "$E2E_CANDIDATE_DIGEST" \
  -platform "$E2E_CANDIDATE_PLATFORM" -out "$temporary/provenance.json" > "$temporary/identities.txt"
read -r manifest config < "$temporary/identities.txt"
docker image inspect "$manifest" >/dev/null 2>&1 || docker image inspect "$config" >/dev/null
printf 'Prebuilt OCI candidate tests passed; source archive unchanged.\n'