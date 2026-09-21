# Release pipeline execution plan

Status: partially implemented in ror-test; release publication is not implemented.
Implementation update: the ror-test archive verifier, prebuilt runner mode, strict
report gate and reusable candidate workflow are now implemented locally; see
[candidate testing](prebuilt-candidate-testing.md). Real cross-repository workflow
execution has now reached native amd64 candidate startup in
[run 35573448184](https://github.com/NorskHelsenett/ror-api/actions/runs/35573448184).
Build, archive handoff, and verifier tests passed; synthetic seeding failed before
API scenarios ran. Local amd64 reproduction confirmed that Mongo's temporary
loopback initialization server could pass the old health check prematurely.
The authenticated service-host health check, three-fresh-start regression, and
seed-only CI diagnostics are fixed locally; the caller needs a published updated
harness pin before retrying GitHub. Native GitHub suite/evidence success is still
pending. A test-only API caller exists; production RC/final publishing and approval
controls remain unimplemented.

## Agreed behavior

- Merges to `ror-api/main` run integration tests for feedback only.
- Release candidates are requested manually with a target final version and API commit/ref.
- Every RC is compiled with the final version: target `v1.25.0` means `ROR_VERSION=v1.25.0`, even when published as `v1.25.0-rc.1`.
- Release integration testing is amd64-only on a native runner. There is no arm64 test gate. Existing amd64/arm64 image builds may remain; each published platform must build successfully, but arm64 is not claimed as integration-tested.
- Each candidate records the actual source SHA. A different commit for the same target produces a new RC, never overwrites an existing RC.
- RC images, RC charts, RC Git tags, and GitHub prereleases are published only after the build and all required integration and packaging tests succeed. They may be published before final-release approval, never with pending or failed tests.
- Untested candidates remain local to CI runners or in access-controlled workflow artifacts. No untested image is pushed to an external registry, even under a temporary or commit-scoped tag.
- Failed, cancelled, skipped, or incomplete tests leave all published RCs and any CD-tracked RC pointer unchanged. Failure evidence remains in CI logs/reports, not in a published prerelease.
- Final publication requires successful integration tests and explicit approval of that candidate.
- Promote the tested image by digest without rebuilding. Never update `latest` during candidate creation.
- CD tools automatically track the latest published RC, so RC publication is a deployment-enabling action and must be strictly test-gated. The release workflow itself runs no deployment commands and holds no cluster credentials.

## Current files and gaps

| File | Current behavior | Required change |
|---|---|---|
| [testenv/run.sh](../testenv/run.sh) | Always builds a local API image and deletes it during cleanup | Accept a checksummed OCI image artifact/platform for pre-publication testing; retain digest input for already-published images; never rebuild the candidate |
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

- Extend `testenv/run.sh` with a candidate OCI archive input and `E2E_CANDIDATE_PLATFORM=linux/amd64|linux/arm64`. Retain `E2E_CANDIDATE_IMAGE=repository@sha256:...` for testing already-published images/baselines, not for staging untested releases.
- Load the archive into the isolated runner's image store, or use an ephemeral runner-local registry when digest-preserving import requires it. That registry must not be externally reachable or discoverable by CD.
- Keep `run`, `repeat`, `compare`, local source builds, and local snapshot mode working.
- In external-image mode, build only the test runner. Do not require sibling API/shared source checkouts.
- Record the archive checksum, OCI index/platform manifest digests, platform, harness SHA, suite fingerprint, and fixture hashes with the report. Verify that the executed platform image belongs to the recorded archive/index.
- Install cleanup handling before build/pull operations. Remove only images and containers owned by the run.
- Make the reusable release-test mode synthetic-only. Verify report completeness as well as process exit status.

Validation: build one local candidate, export it as an OCI archive, and run all
34 scenarios from that archive without external registry writes. Verify no API
rebuild occurs, input artifacts remain unchanged, and a bad checksum/digest or
failed assertion fails the run. Test digest-preserving import/export explicitly.
Keep ordinary current-source and snapshot regression tests green.

### 2. Expose the reusable test workflow

- Add `workflow_call` inputs for candidate artifact identity, expected archive/index/amd64 manifest digests, and optional baseline digest. Default platform to `linux/amd64` and reject other workflow platforms. Download artifacts only from the identified trusted build run and verify checksums before loading.
- Pin the called workflow in consumers to a literal reviewed `ror-test` commit SHA.
- Explicitly check out `NorskHelsenett/ror-test` at that same SHA; a reusable workflow's default checkout would otherwise check out its caller.
- Use a single native amd64 runner (`ubuntu-24.04`) with an explicit `x86_64` check. Do not add an arm64 integration matrix.
- Upload reports on success and failure, with unique names per amd64 candidate and attempt.
- Provide read-only access to private harness repositories, workflow artifacts, and baseline packages. Build/test jobs must have no external package-write credentials; only the gated publication job gets registry write access.

Validation: call the workflow from a temporary API validation workflow, not only
inside ror-test. Confirm that failed, cancelled, missing-report, and skipped test
jobs cannot satisfy a downstream success gate. Lint workflows and unit-test gate
logic. Publish the harness revision before referencing its SHA in API workflows.

### 3. Implement and test candidate identity/state helpers in ror-api

- Add small scripts under `.github/scripts/` for version validation, ref resolution, RC allocation, manifest validation, and promotion preflight; add unit tests beside them.
- Accept only supported final SemVer values (initially `vMAJOR.MINOR.PATCH`). Resolve the source ref once to a full commit SHA on the permitted release branch.
- Reserve attempt numbers in internal CI state under a serialized allocation step, not by creating RC Git tags or releases. Use the run ID as durable attempt identity; gaps in RC numbers are acceptable. Recheck uniqueness and create public RC tags only after the success gate.
- Associate the reservation with the workflow run ID so a job retry recovers the same attempt. A new manual run gets a new RC.
- Record a candidate manifest: target version, RC tag, source SHA, library version, harness SHA, image index/platform digests, toolchain, chart checksum, workflow run ID, and test report references.
- Treat manifests as records to verify, not authorization by themselves. Promotion must verify trusted workflow identity, run conclusion, and digests against GitHub/registry data.

Validation: reject invalid versions, unknown refs, refs outside allowed history,
existing final versions, conflicting RCs, mismatched digests and forged success
records. Test recovery from an internal reservation whose build never finished;
there must be no corresponding public RC tag, image, chart, or prerelease.

### 4. Build, test, then publish passing RCs before approval

- Add `.github/workflows/release-candidate.yml` in ror-api with manual inputs `target_version` and `api_ref`; keep the trusted harness revision pinned in workflow code.
- Use job sequence `prepare -> build-artifacts -> integration-and-chart-validation -> verify-results -> publish-rc`. Upload diagnostic reports separately on success/failure, without publication permissions.
- Reuse the existing amd64/arm64 build matrix and ldflags, but set Version to the target final version, Commit to the selected source SHA, and LibVer to the selected API dependency.
- Remove dependency-changing `go get` commands, pin generator/tool versions, use readonly dependency resolution, and reject unexpected module-file changes.
- Export built images into checksummed OCI artifacts, including the intended multi-platform index. Run the reusable integration workflow only on its amd64 manifest. Do not push externally yet. Record other platforms as built but not integration-tested.
- Build and validate RC/final chart packages before the success gate, as specified in step 5. If any published platform build, amd64 integration assertion, packaging check, or required report is missing or unsuccessful, do not enter publication. Arm64 integration reports are not required.
- `verify-results` requires every required job conclusion to equal `success`, complete expected scenario reports, and exact matching build/test digests. A job merely finishing is not sufficient; `always()` may collect reports but must never authorize publishing.
- Only `publish-rc` receives package/content write permissions. Copy the tested OCI manifests and blobs without rebuilding/recompressing, preserving their digests. Publish the validated RC chart, RC Git tag at the tested commit, and GitHub prerelease containing successful test evidence. No final tag or stable `latest` may be written by this path.
- Serialize RC publication. Recheck attempt ordering so a slower older run cannot replace a newer RC pointer. Retry partial publication only for the same successful candidate and matching digests/checksums.
- If CD uses a mutable RC alias, update it last, after all RC artifacts and evidence are published. If CD selects the highest immutable SemVer RC, assume it may act as soon as that tag/chart is visible; every artifact exposed at that point must already be tested. Cross-registry/GitHub publication is not atomic.
- Record failures/cancellations in CI only. A failed attempt publishes nothing; starting another commit for the same target builds a new internal attempt and requires fresh tests.

Validation: API version metadata inside the RC reports the target final version
and correct commit. Force an amd64 integration failure or a platform build failure, cancel
a job, skip a required job, omit a report, and fail chart validation. Each case
must produce zero external image pushes, RC charts, RC tags, or prereleases;
existing RC pointers, final artifacts, and stable `latest` remain untouched.
On success, verify published image/index digests equal the tested artifacts.

### 5. Define Helm candidate and final packaging

- Add optional `image.digest` to the API chart values/template; use `repository@digest` when set and preserve current tag fallback when absent.
- RC chart: version `X.Y.Z-rc.N`, appVersion `vX.Y.Z`, candidate image digest.
- Final chart: version `X.Y.Z`, same appVersion and image digest, from the identical source commit.
- Changing chart version changes its package digest. Do not claim the RC and final chart archives are byte-identical.
- Prepare both chart packages during the private build stage, before RC publication. Lint/render both and compare workload specifications, allowing only explicitly listed chart-version metadata differences. Packaging failure blocks RC publication too.
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
- Build the exact merged commit using its pinned dependencies, export a private CI image artifact, then invoke the same reusable test workflow. Do not push a commit-scoped image to an external registry.
- This path creates no RC/final Git tags, Helm releases, GitHub releases, or `latest` updates.
- Release-candidate tests remain mandatory even when this merge check was green: release build metadata and candidate digests differ.

Validation: merge a harmless change; confirm tests run for that merge SHA and
only private CI image/test artifacts are created. Nothing is published for CD
to discover, and no deployment or release is triggered by this path.

### 8. Configure GitHub controls and perform the cutover

1. Confirm organization/repository access for reusable workflows and private GHCR images. Use scoped GitHub App credentials where cross-repository reads are needed.
2. Configure the `release` environment with required reviewers, prevent self-review where supported, and restrict it to trusted workflow branches. Verify the repository plan supports these protections; naming an environment alone does not enforce approval.
3. Protect final release tags and workflow files from alternative publication paths. Review privileged workflow changes separately from ordinary API source refs.
4. Identify the exact image/chart repositories and RC selectors tracked by CD (highest SemVer RC and/or a mutable alias). Preserve that automatic RC behavior; ensure private CI artifacts and failed attempts are invisible to it. Publication after successful tests is the deliberate handoff to CD.
5. Merge tested ror-test image support and pin that revision in API workflows.
6. Replace/disable the old tag publisher before creating any RC tags: its current glob matches RC tags too.
7. Land candidate, promotion, and merge-test workflows with publication restricted to a disposable rehearsal image/chart location first.
8. Rehearse attempt 1 failure with no publication, attempt 2 from another commit for the same target, successful tests followed by RC publication, blocked final approval, approved promotion, and matching final digest. Use rehearsal repositories not watched by CD; never expose a deliberately failing candidate to real RC trackers.
9. Enable production publication only after the gate-failure tests and reviewer configuration are verified. Do not create a real release as part of implementation without authorization.

### README badges at cutover

Update [ror-api/README.md](../../ror-api/README.md) as each workflow becomes
available. Keep the existing Dependabot badge. The current test/build and release
workflow badges can be displayed now; do not add links to nonexistent workflows.

| Badge | Source and filter | Meaning and click destination |
|---|---|---|
| Test and build API | `testandbuild.yml`, `event=pull_request` | Latest matching PR workflow result; links to its workflow runs |
| Integration (main) | Planned `integration.yml`, `branch=main&event=push` | Latest matching post-merge integration workflow result; links to that workflow |
| RC publication | Planned `release-candidate.yml`, `branch=main&event=workflow_dispatch` | Full candidate workflow result, including all test gates and publication, not just build success; links to candidate runs and their reports |
| Final release publication | `release.yml`; after cutover filter `branch=main&event=workflow_dispatch` | Approved final promotion workflow result; links to promotion runs |

Use native GitHub Actions status badges with the pattern
`https://github.com/NorskHelsenett/ror-api/actions/workflows/<file>/badge.svg` and
the filters above. Each image must have meaningful alt text and a clickable link
to the corresponding workflow, not a static hand-maintained "passing" label.

Badges are informational, not release authorization. A workflow badge represents
the latest matching run, not necessarily the current HEAD or the currently
published RC, and it may be cached. A failed new candidate can make the RC badge
red while CD correctly continues to use the previous passing RC. Candidate-specific
eligibility must still come from verified run conclusions and artifact digests.
Do not label a generic latest tag badge "approved release" or "passing RC".

Validation: check badge URLs/filters against the implemented triggers, open each
workflow link, and verify rendering after the workflows are published. Confirm
failed candidates remain visible as failed workflow runs without creating a new
CD-visible RC. No deployment status badge is provided by this release pipeline.

## Operator runbook after implementation

1. Merge API changes. Inspect the integration result for the merge SHA.
2. Select **Release Candidate**, target `v1.25.0`, and commit A. Attempt 1 is built internally as `v1.25.0` and tested privately; no RC is visible yet.
3. If attempt 1 fails, no RC artifacts or aliases are published/updated. Fix the code and start a new candidate run for commit B and the same target. GitHub's rerun button cannot change the source commit.
4. When attempt 2 passes every required check, the workflow publishes `v1.25.0-rc.2` (numbering gaps are intentional). CD may now deploy that passing RC automatically. Select **Promote Release** for this candidate when a final release is desired.
5. Reviewer verifies commit/digests/results and approves publication.
6. Verify final image digest equals RC2's tested digest; inspect chart and GitHub release. Deployment remains owned by external CD, not this workflow; published RCs are automatically tracked.

## Completion criteria

- Prebuilt-image mode passes the existing 34 scenarios without rebuilding API source.
- The amd64 candidate has complete passing integration evidence from a native runner. Other published architectures require successful builds but no integration-test gate; their untested status is explicit.
- No untested or failed candidate is pushed to an external registry. Failed/cancelled/skipped/incomplete builds or tests create no RC chart, tag, or prerelease and never change a CD-tracked RC pointer.
- New commits can generate new immutable RCs for one target version.
- Final publication is impossible through the supported workflows without complete successful integration tests and reviewer approval.
- Approved publication preserves the tested image digest and uses the verified chart artifact.
- The old tag-triggered bypass is gone; stable `latest` is unchanged by candidate attempts, and RC selectors expose only successfully tested candidates.
- Failure, cancellation, supersession, concurrency, and partial-publication recovery have executable tests.
- Repository docs describe operator commands, permissions, evidence, and recovery. GitHub-side settings are verified, not merely documented.
- The API README links status badges for PR tests, main integration, RC publication, and final promotion; badge labels/filters match the real workflows and never replace the release gates.
- No deployment is performed by these workflows. External CD may automatically deploy a published, passing RC; this is intentional. The gate covers the implemented 34 scenarios, not unimplemented broker/agent/Kubernetes workflows.