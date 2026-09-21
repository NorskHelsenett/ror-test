# Prebuilt candidate integration tests

This implements the ror-test portion of the release plan. It **tests** privately
built candidates; it does not publish images, charts, tags, releases, or deployments.
The API candidate builder and final promotion gate are not implemented here.

## Local OCI archive

Prerequisites: Docker with the containerd image store (native OCI archive loading),
Go from this module, Bash, and the candidate archive. Source checkout of ror-api
or shared ror is not required by archive mode. The candidate must be a Linux image
for the selected platform. Release integration tests run only on native amd64.
The local harness still supports arm64 development; local cross-architecture
runs require independently configured emulation.

The build job should export an **uncompressed OCI tar archive** as `candidate.tar`.
For Docker Buildx, use `--output type=oci,dest=candidate.tar`. Record the archive
SHA-256 and OCI index/manifest digest as trusted build-job outputs. Do not calculate
new expected values from an untrusted download and call that verification.

```sh
E2E_CANDIDATE_ARCHIVE=/absolute/path/candidate.tar \
E2E_CANDIDATE_SHA256=sha256:<archive-checksum> \
E2E_CANDIDATE_DIGEST=sha256:<root-index-or-manifest-digest> \
E2E_CANDIDATE_PLATFORM=linux/amd64 \
bash testenv/run.sh run
```

`repeat` runs the same prebuilt candidate twice on separate fresh databases;
`compare <baseline-image>` compares against a separately started baseline.
Release callers must specify any baseline as an immutable `repository@sha256:...`.
Existing local source and sanitized snapshot modes remain available. The reusable
workflow is synthetic-only and never accepts a snapshot.

Archive handling:

1. Check the archive SHA-256 before Docker sees it. Read the tar without extracting
   files; reject unexpected paths, links, duplicate files, oversized metadata,
   malformed layout, missing blobs and descriptor-size mismatches.
2. Validate SHA-256 of every blob. Require the root to identify the expected index
   or manifest and exactly one image configuration for the requested platform.
3. Load the original OCI archive without converting/recompressing it or rebuilding
   the API. Verify the loaded image's platform and exact manifest/configuration
   digest, then verify the API container runs that image.
4. Run all scenario assertions, then independently validate the report against the
   checked-out suite's fingerprint, ordered names, count, statuses and failures.
5. Retain input archive and loaded candidate image. Cleanup removes only run-owned
   stack resources and the runner/source-built API images. No external registry
   writes occur. Native OCI support is required; format conversion is deliberately
   not a fallback because it can change configuration/manifest digests.

`E2E_CANDIDATE_DIGEST` accepts the SHA-256 of `index.json`, or the sole root
descriptor's digest (the wrapping root index used by OCI exporters). Every image
referenced by the archive must have valid referenced blobs. Layer data is hashed
streamingly; manifest/config JSON is limited to 4 MiB per blob and retained small
blobs are bounded to 64 MiB total. Deeply nested or ambiguous platform indexes fail.

Already-published immutable images can also be tested:

```sh
E2E_CANDIDATE_IMAGE=ghcr.io/norskhelsenett/ror-api@sha256:<digest> \
E2E_CANDIDATE_PLATFORM=linux/amd64 bash testenv/run.sh run
```

This mode pulls but never pushes or deletes the supplied image. It is useful for
existing releases, not for staging an untested RC. Archive and image inputs are
mutually exclusive. Mutable candidate tags are rejected.

## Reusable workflow contract

Use [.github/workflows/candidate-e2e.yml](../.github/workflows/candidate-e2e.yml).
The calling build job must upload `candidate.tar` with `actions/upload-artifact`
and expose its immutable artifact ID plus trusted digest outputs. Download is
restricted to the **same caller workflow run**; arbitrary repository/run selection
is not supported. Never invoke this privileged release chain on untrusted PR code.

Example caller job (template; replace both harness placeholders with the same
reviewed, published 40-character ror-test commit SHA):

```yaml
integration:
  needs: build
  uses: NorskHelsenett/ror-test/.github/workflows/candidate-e2e.yml@<HARNESS_SHA>
  permissions:
    contents: read
    actions: read
    packages: read
  with:
    artifact_id: ${{ needs.build.outputs.artifact_id }}
    archive_sha256: ${{ needs.build.outputs.archive_sha256 }}
    index_digest: ${{ needs.build.outputs.index_digest }}
    manifest_digest: ${{ needs.build.outputs.amd64_digest }}
    platform: linux/amd64
    harness_sha: <HARNESS_SHA>
    source_sha: ${{ needs.build.outputs.source_sha }}
  secrets:
    CROSS_REPO_READ_TOKEN: ${{ secrets.CROSS_REPO_READ_TOKEN }}
```

The harness explicitly checks out `NorskHelsenett/ror-test` at `harness_sha`, not
the caller repository. The caller must pin `uses` to the same SHA; that equality
is a caller workflow-review requirement, not something this workflow can infer
from `github.sha` (which belongs to the caller). Checkout is required to be clean.
Private repositories need reusable-workflow access enabled and read-only checkout
credentials. Optional private GHCR baselines need package-read access for the
caller token. Build/test jobs must not receive package-write credentials.

Inputs are `artifact_id`, `archive_sha256`, `index_digest`, `manifest_digest`,
`harness_sha`, `source_sha`, optional `platform` (defaults to `linux/amd64`), and
optional `baseline_image`. Other workflow platforms are rejected.
Artifact layout must contain `candidate.tar` at its root. Artifact IDs are stable;
names alone are not used to select release candidates.

The job uses `ubuntu-24.04` and explicitly checks for native `x86_64`. There is no
architecture test matrix or arm64 runner requirement. Docker is configured with
the containerd snapshotter for OCI import. A multi-platform archive is allowed,
but only its amd64 manifest is executed; arm64 contents do not gain integration
coverage from this run.

## Evidence and downstream gate

On success the job outputs `evidence_artifact_id` and uploads amd64 evidence:

- `candidate-image.json`: archive checksum, index/manifest/config digests, platform.
- `candidate-runtime.json`: actual loaded image ID and platform only, no environment.
- `candidate.json`/`.xml`: scenario results; `candidate-verified.json` binds the
  verified report SHA-256 and suite fingerprint to the complete step count.
- `harness-commit.txt`, `harness-worktree.txt`, fixture hashes, optional baseline
  reports/diff, and `exit-code.txt`.
- `release-evidence.json`: binds these results to the input artifact ID, caller run
  and attempt, asserted source SHA, selected harness SHA, suite and fixture hashes.

`source_sha` is provenance asserted by the trusted build caller, not a source
commit discovered inside the binary. This evidence is not cryptographically
signed and is not authorization on its own. Promotion must verify trusted workflow
identity, exact artifact/run/attempt and all required job conclusions; never accept
a standalone uploaded JSON file as proof of eligibility.

No success evidence is uploaded if the gate fails. Failure/cancellation uploads
only selected diagnostic reports and a bounded synthetic-only `seed.log`, never
the archive, binary, real tokens, full startup logs, Vault logs, or snapshot logs.
An absent success artifact/output, skipped job, timeout, cancellation,
nonzero run exit or incomplete report must block **all RC publication** downstream.
The publishing workflow must require this amd64 job to succeed and validate its
evidence; it must not wait for arm64 test evidence. Existing multi-platform builds
may remain, with build success required for every published platform, but only
amd64 is integration-tested. Publication must preserve the verified index and
tested amd64 manifest digests during external push.

## Verification commands

```sh
go test -race ./internal/e2e ./cmd/e2e
node --test .github/scripts/*.test.cjs
bash testenv/candidate-test.sh
```

The last command is a local rehearsal helper: it first builds one source fixture
using sibling ror-api/shared sources, exports an OCI archive, proves an incorrect
archive checksum is rejected, then executes prebuilt mode twice and checks archive
immutability and loaded-image preservation. Only this helper builds the API;
`run.sh` in prebuilt mode never does. It does not publish anything externally.

Locally verified on arm64: native OCI loading, all 34 scenarios on two fresh stacks,
zero comparison differences, archive immutability, and strict verifier unit tests.
Native amd64 execution, a real cross-repository GitHub call, and eventual RC/final
publication gates still require rollout verification. No RC has been published.