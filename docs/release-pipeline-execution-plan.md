# Release pipeline execution plan

Status: implementation plan, not implemented.

## Agreed behavior

- Merges to `ror-api/main` run integration tests for feedback only.
- Release candidates are requested manually with a target final version and API commit/ref.
- Every RC is compiled with the final version: target `v1.25.0` means `ROR_VERSION=v1.25.0`, even when published as `v1.25.0-rc.1`.
- Each candidate records the actual source SHA. A different commit for the same target produces a new RC, never overwrites an existing RC.
- RC images, RC charts, and GitHub prereleases may be published before approval. Their test status is initially pending and can become failed.
- Final publication requires successful integration tests and explicit approval of that candidate.
- Promote the tested image by digest without rebuilding. Never update `latest` during candidate creation.
- No deployment commands, cluster credentials, or deployment workflow dispatches.

## Current files and gaps

| File | Current behavior | Required change |
|---|---|---|
| [testenv/run.sh](../testenv/run.sh) | Always builds a local API image and deletes it during cleanup | Add externally supplied candidate digest and platform; never build or delete that external image |
| [testenv/compose.yaml](../testenv/compose.yaml) | Accepts API image through environment | Explicit candidate platform selection for architecture tests |
| [.github/workflows/e2e.yml](../.github/workflows/e2e.yml) | Standalone PR/manual source testing with local Go workspace | Retain source testing; expose reusable immutable-image testing with explicit harness checkout |
| [API release workflow](../../ror-api/.github/workflows/release.yml) | Publishes on version tag push, including RC tags; assigns `latest` immediately | Replace with a manual, test-gated final promotion workflow |
| [API Dockerfile](../../ror-api/Dockerfile) | Packages prebuilt architecture-specific binaries | Reuse for candidates; pin base image digest and correct source provenance label |
| [API deployment template](../../ror-api/charts/ror-api/templates/deployment.yaml) | Renders image repository plus tag | Support digest references, preserving existing tag fallback |

Candidate build dependencies must come from the selected API commit's `go.mod`
and `go.sum`, using `GOWORK=off`. Shared-library `main` must not replace them.
Publish required shared-library fixes and update the API dependency before using
that API commit as a release candidate. Local workspace success does not prove
the published dependencies contain the RabbitMQ channel fix.

## Ordered implementation

### 1. Add immutable candidate-image testing in ror-test

- Extend `testenv/run.sh` with `E2E_CANDIDATE_IMAGE=repository@sha256:...` and `E2E_CANDIDATE_PLATFORM=linux/amd64|linux/arm64`.
- Keep `run`, `repeat`, `compare`, local source builds, and local snapshot mode working.
- In external-image mode, build only the test runner. Do not require sibling API/shared source checkouts.
- Record the requested index digest, resolved platform manifest digest, platform, harness SHA, suite fingerprint, and fixture hashes with the report.
- Install cleanup handling before build/pull operations. Remove only images and containers owned by the run.
- Make the reusable release-test mode synthetic-only. Verify report completeness as well as process exit status.

Validation: build one local candidate and publish it to a disposable test registry;
run all 34 scenarios using its digest; verify no API build occurs, the external
image remains after cleanup, and a bad digest or failed assertion fails the run.
Keep ordinary current-source and snapshot regression tests green.

### 2. Expose the reusable test workflow

- Add `workflow_call` inputs for candidate digest/platform and optional baseline digest.
- Pin the called workflow in consumers to a literal reviewed `ror-test` commit SHA.
- Explicitly check out `NorskHelsenett/ror-test` at that same SHA; a reusable workflow's default checkout would otherwise check out its caller.
- Use runner architectures matching each image platform where available; qualify emulation explicitly if native runners are unavailable.
- Upload reports on success and failure, with unique names per platform and candidate.
- Provide read-only access to private harness repositories and candidate packages; do not give tests final-release write credentials.

Validation: call the workflow from a temporary API validation workflow, not only
inside ror-test. Confirm that failed, cancelled, missing-report, and skipped test
jobs cannot satisfy a downstream success gate. Lint workflows and unit-test gate
logic. Publish the harness revision before referencing its SHA in API workflows.

### 3. Implement and test candidate identity/state helpers in ror-api

- Add small scripts under `.github/scripts/` for version validation, ref resolution, RC allocation, manifest validation, and promotion preflight; add unit tests beside them.
- Accept only supported final SemVer values (initially `vMAJOR.MINOR.PATCH`). Resolve the source ref once to a full commit SHA on the permitted release branch.
- Reserve `vX.Y.Z-rc.N` with an atomic Git ref creation. Retry allocation on conflicts; gaps are acceptable, reuse is not.
- Associate the reservation with the workflow run ID so a job retry recovers the same attempt. A new manual run gets a new RC.
- Record a candidate manifest: target version, RC tag, source SHA, library version, harness SHA, image index/platform digests, toolchain, chart checksum, workflow run ID, and test report references.
- Treat manifests as records to verify, not authorization by themselves. Promotion must verify trusted workflow identity, run conclusion, and digests against GitHub/registry data.

Validation: reject invalid versions, unknown refs, refs outside allowed history,
existing final versions, conflicting RCs, mismatched digests and forged success
records. Test recovery from a reserved RC whose build never finished.

### 4. Build and publish candidates before approval

- Add `.github/workflows/release-candidate.yml` in ror-api with manual inputs `target_version` and `api_ref`; keep the trusted harness revision pinned in workflow code.
- Use job sequence `prepare -> build -> publish-rc -> integration -> record-result`.
- Reuse the existing amd64/arm64 build matrix and ldflags, but set Version to the target final version, Commit to the selected source SHA, and LibVer to the selected API dependency.
- Remove dependency-changing `go get` commands, pin generator/tool versions, use readonly dependency resolution, and reject unexpected module-file changes.
- Publish immutable RC image tags and signatures; capture their index and per-platform digests. No final tag or `latest` may be written by this path.
- Publish the RC chart and GitHub prerelease labeled as pending tests, then run the reusable integration workflow on every platform that will be included in the final image.
- Record pass/fail/cancellation and report links on the candidate. Failed candidates remain inspectable but are ineligible for promotion.

Validation: API version metadata inside the RC reports the target final version
and correct commit. Force an integration failure and confirm RC artifacts remain
available while final image/chart/tag/release and `latest` remain untouched.

### 5. Define Helm candidate and final packaging

- Add optional `image.digest` to the API chart values/template; use `repository@digest` when set and preserve current tag fallback when absent.
- RC chart: version `X.Y.Z-rc.N`, appVersion `vX.Y.Z`, candidate image digest.
- Final chart: version `X.Y.Z`, same appVersion and image digest, from the identical source commit.
- Changing chart version changes its package digest. Do not claim the RC and final chart archives are byte-identical.
- Prepare both chart packages during candidate creation. Lint/render both and compare workload specifications, allowing only explicitly listed chart-version metadata differences.
- Retain the final chart as a checksummed candidate artifact, not a published final chart. If it expires or disappears, fail promotion and create a new candidate rather than silently rebuilding.

Validation: Helm lint/template pass, digest rendering is correct, existing tag-only
values still render, and the final chart resolves to the same tested image.
This checks packaging/rendering; the current suite does not perform a Helm deployment.

### 6. Replace final release publishing with manual promotion

- Replace the tag-triggered `.github/workflows/release.yml` with manual input `candidate_tag`.
- Use `verify-candidate -> approve-and-publish`; apply the protected `release` environment only to the latter job.
- Preflight verifies the trusted candidate workflow, all required integration jobs succeeded, expected suite completeness, exact tested digests, source SHA, target version, and retained artifact checksums.
- Show candidate, commit, target version, digests and test links in the approval summary. Revalidate immediately after approval, including whether the target was published while approval was pending.
- Reject superseded candidates by default; require selecting the latest eligible attempt for the target. A new candidate never inherits old approval.
- Promote the OCI index by digest and verify final-tag digest equality; do not invoke the API build again. Publish the previously prepared final chart.
- Create the final Git tag at the tested commit and GitHub release with provenance/report links. Update `latest` last, only for the permitted newest stable version.
- Serialize final publication and use create-if-absent checks. On retry, reuse only artifacts with matching expected digests/checksums; fail on conflicting existing content.
- After a completed final release, reject target-version reuse. Permit controlled recovery of an incomplete publication for the same candidate only.

Validation: tests failed/cancelled/skipped, reports missing, no approval, stale
approval, superseded candidate, existing version, altered digest, and concurrent
publication all block final release. Simulate failure between image/chart/release
publication and verify recovery never overwrites another candidate.

### 7. Add post-merge integration feedback

- Add `.github/workflows/integration.yml` in ror-api for pushes to `main`.
- Build the exact merged commit using its pinned dependencies and a commit-scoped CI image, then invoke the same reusable test workflow.
- This path creates no RC/final Git tags, Helm releases, GitHub releases, or `latest` updates.
- Release-candidate tests remain mandatory even when this merge check was green: release build metadata and candidate digests differ.

Validation: merge a harmless change; confirm tests run for that merge SHA and
only the CI image/test artifacts are created. No deployment or release occurs.

### 8. Configure GitHub controls and perform the cutover

1. Confirm organization/repository access for reusable workflows and private GHCR images. Use scoped GitHub App credentials where cross-repository reads are needed.
2. Configure the `release` environment with required reviewers, prevent self-review where supported, and restrict it to trusted workflow branches. Verify the repository plan supports these protections; naming an environment alone does not enforce approval.
3. Protect final release tags and workflow files from alternative publication paths. Review privileged workflow changes separately from ordinary API source refs.
4. Audit external deployment/image-update automation. Ensure neither RC tags nor final publication trigger deployment without the user's separate deployment process.
5. Merge tested ror-test image support and pin that revision in API workflows.
6. Replace/disable the old tag publisher before creating any RC tags: its current glob matches RC tags too.
7. Land candidate, promotion, and merge-test workflows with publication restricted to a disposable rehearsal image/chart location first.
8. Rehearse RC1 failure, RC2 from another commit for the same target, successful tests, blocked approval, approved promotion, and matching final digest. Avoid fake version tags in the real repository: use an isolated rehearsal repository for tag/release tests.
9. Enable production publication only after the gate-failure tests and reviewer configuration are verified. Do not create a real release as part of implementation without authorization.

## Operator runbook after implementation

1. Merge API changes. Inspect the integration result for the merge SHA.
2. Select **Release Candidate**, target `v1.25.0`, and commit A. Receive `v1.25.0-rc.1`, built internally as `v1.25.0`.
3. If RC1 fails, fix the code and start a new candidate run for commit B and target `v1.25.0`. Receive RC2. GitHub's rerun button cannot change the source commit.
4. Once RC2 tests pass, select **Promote Release**, candidate `v1.25.0-rc.2`.
5. Reviewer verifies commit/digests/results and approves publication.
6. Verify final image digest equals RC2's tested digest; inspect chart and GitHub release. Deployment remains a separate manual process.

## Completion criteria

- Prebuilt-image mode passes the existing 34 scenarios without rebuilding API source.
- Both published architectures have required passing test evidence.
- New commits can generate new immutable RCs for one target version.
- Final publication is impossible through the supported workflows without complete successful integration tests and reviewer approval.
- Approved publication preserves the tested image digest and uses the verified chart artifact.
- The old tag-triggered bypass is gone; `latest` is unchanged by candidate attempts.
- Failure, cancellation, supersession, concurrency, and partial-publication recovery have executable tests.
- Repository docs describe operator commands, permissions, evidence, and recovery. GitHub-side settings are verified, not merely documented.
- No deployment is performed. The gate covers the implemented 34 scenarios, not unimplemented broker/agent/Kubernetes workflows.