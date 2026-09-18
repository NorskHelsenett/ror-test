# ROR end-to-end tests

The E2E harness drives public HTTP endpoints, asserts expected behavior, and
compares independently executed runs. It does not use the current ROR client
library to interpret responses. Existing scratch programs are unchanged.

## Prerequisites

- Docker with Compose v2, running Linux containers.
- Go matching the source repositories and Bash.
- Sibling `ror`, `ror-api`, and `ror-test` directories. For current cross-repo
  changes, use the existing parent Go workspace; do not add module replacements.
- Disk and memory for the API cross-build and dependency containers. A cold
  Microsoft Graph SDK build can take several minutes. Do not interrupt it merely
  because output pauses.

## Synthetic run

From `ror-test`:

```sh
bash testenv/run.sh run
```

Alternatively, run the VS Code task **e2e: synthetic stack**. It keeps the build
in its own terminal. The script compiles current local API and shared-library
sources, including uncommitted changes, then starts MongoDB, RabbitMQ, Valkey,
Vault, a mock OIDC issuer, and the API. Vault issues real dependency credentials;
the API's real authentication middleware validates signed test tokens.

Each run has a unique Compose project and fresh database. No host service ports
are exposed, and the runtime network is internal. Dependencies do not inherit
development `.env` files. Synthetic credentials must never be used outside this
disposable environment. Containers and volumes are removed on exit.

Results are under `artifacts/<run-id>/candidate.json` and `candidate.xml`.
Any assertion, startup, transport, or comparison failure returns a nonzero exit
code. A dependency failure is not a skipped test. Reports contain response
digests and assertion paths, not response bodies or tokens. Treat all local
artifacts as private regardless.

## Compare with a release

```sh
bash testenv/run.sh compare ghcr.io/norskhelsenett/ror-api@sha256:<digest>
```

The candidate is built from current source. The baseline is the supplied image,
so it cannot accidentally use current local modules. Each stack is run
sequentially with an independent fresh copy of the same fixtures. Never point
both versions at the same database. Use immutable image digests for reproducible
comparisons; older images must support the suite's endpoints and configuration.

Comparisons fail on failed assertions in either version, missing/duplicate
results, different suites, incomplete reports, status differences, or normalized
response differences. Matching failures do not count as equivalence. At present,
intentional differences require reviewing and updating the versioned suite;
there is no blanket ignore-regressions option.

Use `bash testenv/run.sh repeat` to run the current image twice on fresh data
and verify A-vs-A determinism. Image IDs, binary build information, and fixture
hashes are saved alongside reports. Startup failure logs stay local because they
can contain credentials from dependency startup; CI uploads only selected reports.

## Sanitized production snapshots

```sh
E2E_SNAPSHOT=/absolute/path/sanitized.archive.gz \
E2E_SNAPSHOT_SANITIZED=yes bash testenv/run.sh compare <baseline-image>
```

This accepts a gzip-compressed `mongodump --archive` file, not a database URI.
The source archive is mounted read-only. Only `nhn-ror` collections `resourcesv2`,
`acl`, `projects`, `workspaces`, and `datacenters` are restored. API keys, users,
tasks, and database users are not restored. Each version gets its own fresh
restore, with the same synthetic ACL/identity overlay. The `e2e.invalid` identity
realm is reserved: collisions fail seeding. Snapshots are rejected when `CI=true`.

**Sanitize before running.** The acknowledgement is not a sanitizer. Remove
personal data and secrets embedded in resource payloads; consistently rewrite
identity/group references and external endpoints. Do not supply a raw production
dump. The internal Docker network blocks external service calls, but does not
make sensitive data safe to distribute. Neither snapshots nor startup logs belong
in Git or CI artifacts. The runner cannot prove that a local forwarded port is
not connected to production.

This mode exercises the deterministic fixture contracts in a restored inventory;
it does not yet assert every pre-existing production identity's permissions or
automatically apply version-specific database migrations.

## CI

`.github/workflows/e2e.yml` provides PR and manual workflow entrypoints.
It checks out sibling API/shared-library sources, creates an isolated Go
workspace, runs harness race tests, then repeats the synthetic suite or compares
against the requested baseline image. API/shared refs are configurable; pin
commits when reproducibility matters. No production credentials are required.

The repository is [NorskHelsenett/ror-test](https://github.com/NorskHelsenett/ror-test).
Private sibling repositories require a read-only
`CROSS_REPO_READ_TOKEN`; private baseline images additionally require a registry
login in the calling CI environment. GitHub CI execution is not implied by a
successful local run. Existing API/auth repository workflows are unchanged.

Run `bash testenv/snapshot-test.sh --restore` to verify archive restore with
synthetic data, excluded credential collections, and source immutability. Add
`--e2e` instead to also run two complete API stacks against separate restores.
The latter is local-only; CI runs the synthetic restore check and fresh-data
API comparison separately.

## Existing isolated local target

```sh
go run ./cmd/e2e run -suite testenv/scenarios/acl.json \
  -target http://127.0.0.1:10000 -label local -out artifacts/local.json
go run ./cmd/e2e compare -baseline artifacts/baseline.json \
  -candidate artifacts/local.json -out artifacts/diff.json
```

Supply `ADMIN_TOKEN`, `READER_TOKEN`, and `OUTSIDER_TOKEN` in the environment,
or use `-oidc http://127.0.0.1:<port>` with the supplied mock issuer configuration.
Tokens are never command-line arguments. The runner only permits loopback origins
or the explicit `api` container name with `-container`. It disables proxies and
redirect following. This is an accidental-target guard, not proof that a local
port is not forwarded to production: only use disposable local targets.

## Scenarios and fixtures

[testenv/seed.js](testenv/seed.js) contains deterministic bootstrap ACL data.
[testenv/oidc.json](testenv/oidc.json) defines synthetic admin, reader, and outsider
identities. [testenv/scenarios/acl.json](testenv/scenarios/acl.json) is the
executable source of truth for the following expectations.

### Test identities and data

The admin, reader, and outsider are OIDC **users**. In particular, the "reader"
is a human test principal, not an agent or cluster. The mock issuer signs their
tokens; the real API middleware validates them and derives group names using the
user's email domain. Separate API keys authenticate two real **cluster** identities
through the production `X-API-KEY` middleware, which sets `IdentityTypeCluster`.

| Identity | API group | Seeded permissions |
|---|---|---|
| `admin@e2e.invalid` | `admins@e2e.invalid` | Global `ror:read/create/update/delete` and `ror:config:read/write` grants |
| `reader@e2e.invalid` | `readers@e2e.invalid` | Only `ror:read` on KubernetesCluster A |
| `outsider@e2e.invalid` | `outsiders@e2e.invalid` | No fixture grant |
| `e2e-cluster-a` (cluster identity) | `11111111-1111-4111-8111-111111111111@cluster.ror.system` | `ror:read/create/update` on KubernetesCluster A |
| `e2e-cluster-b` (cluster identity) | `22222222-2222-4222-8222-222222222222@cluster.ror.system` | `ror:read/create/update` on KubernetesCluster B |

Cluster A is `11111111-1111-4111-8111-111111111111`; cluster B is
`22222222-2222-4222-8222-222222222222`. Each has a seeded KubernetesCluster
resource with a self-referencing owner, plus a Pod and Deployment directly owned
by that cluster. All six resources initially have label `e2e-update=original`.
These are real stored ROR resources, not running Kubernetes clusters. API keys
are synthetic, hashed with the test salt, and must never be used in production.
The self-access permissions are ACL data; no test bypasses authentication or
injects a cluster identity into the request context. The API also executes its
normal non-development startup seeding. Snapshot mode rejects collisions with
these reserved cluster UIDs, child UIDs, API-key identifiers, or principal groups.

### API scenario matrix

Run `bash testenv/run.sh run` from this directory. The matrix is grouped by acting
user/identity; the **# column is the execution order** in the JSON suite, not the
display order below. Requests use the real API, MongoDB, Valkey, Vault, and RabbitMQ.
The upstream OIDC provider is mocked; no production IdP or ror-auth broker is used.

For rows 1-6, the lookup endpoint is
`HEAD /v2/acl/lookup/KubernetesCluster/<subject>/<access>`.
`ACL_ID` is captured from the create response and reused in subsequent requests.

#### Unauthenticated callers

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 1 | `anonymous denied` | Request `ror:read` on A without authorization | HTTP **401** |
| 2 | `invalid token denied` | Request `ror:read` on A with `Bearer invalid` | HTTP **401** |

#### Reader user (`reader@e2e.invalid`)

Authenticated with `READER_TOKEN`; read-only permission on cluster A.

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 3 | `reader own cluster` | Reader requests `ror:read` on A | HTTP **200**; `Cache-Control` exactly `no-store, no-cache, must-revalidate` |
| 4 | `reader other cluster denied` | Reader requests `ror:read` on B | HTTP **403** |
| 5 | `reader write denied` | Reader requests `ror:update` on A | HTTP **403** |

#### Outsider user (`outsider@e2e.invalid`)

Authenticated with `OUTSIDER_TOKEN`; no fixture grant.

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 6 | `outsider denied` | Outsider requests `ror:read` on A | HTTP **403** |

#### Admin user (`admin@e2e.invalid`)

Authenticated with `ADMIN_TOKEN`; global ACL data permits reads and updates across
both cluster ownership boundaries. Existing ACL CRUD checks are supplemented by
six resource PATCH/readback pairs. Resource updates run last (steps 23-34), after
B's unchanged-state assertions, so they cannot invalidate the isolation checks.

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 7 | `create V3 grant` | Admin sends `POST /v2/acl` for `created@e2e.invalid`, scope `ror`, subject `Config`, access `["ror:config:read"]` | HTTP **200**; `/version` is `3`; `/access` exactly matches; `/id` is a nonempty string captured as `ACL_ID` |
| 8 | `read V3 grant` | Admin sends `GET /v2/acl/${ACL_ID}` | HTTP **200**; `/id` matches the captured ID; `/access` is exactly `["ror:config:read"]` |
| 9 | `update V3 grant` | Admin sends `PUT /v2/acl/${ACL_ID}`, adding `ror:config:write` | HTTP **200**; `/access` is exactly `["ror:config:read", "ror:config:write"]` |
| 10 | `delete V3 grant` | Admin sends `DELETE /v2/acl/${ACL_ID}` | HTTP **200**; the whole JSON response is boolean `true` |
| 23 | `admin updates cluster A` | PATCH A's KubernetesCluster with label `e2e-update=updated-by-admin` | HTTP **200** |
| 24 | `admin reads persisted cluster A update` | GET A's KubernetesCluster | HTTP **200**; kind `KubernetesCluster`, A's UID, and label `updated-by-admin` |
| 25 | `admin updates cluster A Pod` | PATCH A's Pod with the admin label | HTTP **200** |
| 26 | `admin reads persisted cluster A Pod update` | GET A's Pod | HTTP **200**; kind `Pod`, correct UID, and label `updated-by-admin` |
| 27 | `admin updates cluster A Deployment` | PATCH A's Deployment with the admin label | HTTP **200** |
| 28 | `admin reads persisted cluster A Deployment update` | GET A's Deployment | HTTP **200**; kind `Deployment`, correct UID, and label `updated-by-admin` |
| 29 | `admin updates cluster B` | PATCH B's KubernetesCluster with the admin label | HTTP **200** |
| 30 | `admin reads persisted cluster B update` | GET B's KubernetesCluster | HTTP **200**; kind `KubernetesCluster`, B's UID, and label `updated-by-admin` |
| 31 | `admin updates cluster B Pod` | PATCH B's Pod with the admin label | HTTP **200** |
| 32 | `admin reads persisted cluster B Pod update` | GET B's Pod | HTTP **200**; kind `Pod`, correct UID, and label `updated-by-admin` |
| 33 | `admin updates cluster B Deployment` | PATCH B's Deployment with the admin label | HTTP **200** |
| 34 | `admin reads persisted cluster B Deployment update` | GET B's Deployment | HTTP **200**; kind `Deployment`, correct UID, and label `updated-by-admin` |

#### Cluster A (`e2e-cluster-a`)

Authenticated with `CLUSTER_A_KEY` in `X-API-KEY`, not a user bearer token.

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 11 | `cluster A updates itself` | A's API key sends `PATCH /v2/resources/uid/${CLUSTER_A}` with label `e2e-update=updated-by-cluster-a` | HTTP **200** |
| 12 | `cluster A reads its persisted update` | A's API key sends GET for the same UID | HTTP **200**; first resource has kind `KubernetesCluster`, A's UID, and the updated label |
| 13 | `cluster A updates its Pod` | A's API key sends PATCH for A's Pod with the updated label | HTTP **200** |
| 14 | `cluster A reads its persisted Pod update` | A's API key sends GET for A's Pod | HTTP **200**; kind `Pod`, correct UID, and the updated label |
| 15 | `cluster A updates its Deployment` | A's API key sends PATCH for A's Deployment with the updated label | HTTP **200** |
| 16 | `cluster A reads its persisted Deployment update` | A's API key sends GET for A's Deployment | HTTP **200**; kind `Deployment`, correct UID, and the updated label |
| 17 | `cluster A cannot update cluster B` | A's API key sends PATCH for B's KubernetesCluster with label `e2e-update=unauthorized-change` | HTTP **404**, because the resource is hidden by ACL filtering |
| 19 | `cluster A cannot update cluster B Pod` | A's API key sends PATCH for B's Pod with the unauthorized label | HTTP **404** |
| 21 | `cluster A cannot update cluster B Deployment` | A's API key sends PATCH for B's Deployment with the unauthorized label | HTTP **404** |

#### Cluster B (`e2e-cluster-b`)

Authenticated with `CLUSTER_B_KEY`. Each readback executes immediately after A's
denied write to that object, before the admin is allowed to modify it.

| # | Scenario | How it is tested | Expected result |
|---|---|---|---|
| 18 | `cluster B remains unchanged` | B's API key sends GET for B's KubernetesCluster | HTTP **200**; correct kind/UID and label still `original` |
| 20 | `cluster B Pod remains unchanged` | B's API key sends GET for B's Pod | HTTP **200**; correct kind/UID and label still `original` |
| 22 | `cluster B Deployment remains unchanged` | B's API key sends GET for B's Deployment | HTTP **200**; correct kind/UID and label still `original` |

### Shared assertions and limits

For steps 11-34 all resource operations use `/v2/resources/uid/<uid>`. PATCH bodies
set `metadata.labels.e2e-update`; readback assertions check `/0/kind`,
`/0/metadata/uid`, and `/0/metadata/labels/e2e-update`. B's successful readbacks
also prove that the rejected targets really exist. The cross-cluster **404** is
the current API contract: the read-filtered lookup prevents PATCH from seeing
B's object before it reaches the explicit write-permission check. It is not
treated as interchangeable with **403** in the suite.

The single-run pass condition is all **34/34** steps executed without failures
and command exit code **0**. Any unexpected status, failed assertion, failed
capture, or transport error makes the run fail. Execution stops at the first
failed step, so later dependent steps are **not tested**, not implicitly passed.

The ACL CRUD tests demonstrate preservation of the requested V3 capabilities.
They do **not** exercise Config resource reads/writes, prove that the new grant
changes access, read the entry again after update, or verify absence after delete.
Those require additional scenarios. The HEAD denials check authorization
decisions, not filtering of resource-list responses. The cluster and admin tests exercise
actual persisted metadata changes via PATCH, not full PUT replacement, spec/status
updates, ownership changes, resource creation/deletion, or all Kubernetes kinds.
They cover direct ownership and self-owned cluster objects, not multi-level
ownership inheritance or clusters whose owner is a workspace/project.

Every step declares a method, relative path, and expected HTTP status. Optional
`equals` assertions use JSON pointers; the empty pointer selects the whole body.
`capture` stores string response fields for subsequent `${VARIABLE}` references.
Captured IDs are normalized to logical names for comparison. Bodies are expanded
as structured JSON, not string-concatenated JSON. `ignore` and `unordered` apply
only to explicitly listed JSON paths (`*` matches one segment). Do not normalize
authorization fields or pagination order. Selected response headers can also be
asserted. Steps stop on the first failure to avoid misleading dependent failures.

## Harness tests

```sh
go test -race ./internal/e2e ./cmd/e2e
```

These tests validate the runner and comparator, not the deployed API. A successful
unit test run is not evidence that the real stack passed.

| Test | How it is tested | Expected result |
|---|---|---|
| `TestRunner` | An in-process HTTP server requires a known bearer token, returns a generated ID, and serves a subsequent request using that ID | Three steps pass; token and raw captured ID do not appear in the JSON report; changing the expected first status causes failure after one step |
| `TestTargetAndRedirectSafety` | Try external/credential-bearing/non-HTTP/path-containing targets, then return an external HTTP redirect from a loopback server | Invalid targets are rejected; container name `api` is accepted only with opt-in; the redirect is returned as **302**, not followed |
| `TestCompare` | Compare manufactured reports with equal responses, changed statuses/digests/names, invalid schema/suite, empty/incomplete results, and failures | Equal complete passing reports have no differences; the invalid/regression examples produce differences, including two identically truncated reports |
| `TestNormalizationIsExplicit` | Normalize JSON with declared timestamp ignores, array ordering, and generated-ID aliases; parse trailing JSON and an escaped pointer | Explicitly normalized documents match; without the declared ordering/ID normalization they differ; trailing JSON is rejected and `/a~1b/0` resolves correctly |
| `TestCompareSameFile` | Invoke the comparison command with the same file as baseline and candidate, then increase its declared expected count | Complete report succeeds; truncated report returns an error |
| `TestJUnitAndArtifactPermissions` | Write and parse JUnit for a deliberately failing result; inspect the output file's permissions | `tests=1`, `failures=1`, and file mode `0600` |

Sources: [runner_test.go](internal/e2e/runner_test.go),
[compare_test.go](internal/e2e/compare_test.go), and
[main_test.go](cmd/e2e/main_test.go). With `-race`, any detected data race is also
a failure. This does not run the containerized API under the Go race detector.

## Run modes and expected outcomes

Run these commands from `ror-test`. A negative test deliberately rejects invalid
input; the containing test command still exits **0** when that rejection occurs.

| Command | How it runs | Expected successful result |
|---|---|---|
| `go test -race ./internal/e2e ./cmd/e2e` | Local in-process tests above, no Docker stack | Both packages pass; exit **0** |
| `bash testenv/run.sh run` | Build current source, seed fresh data, start dependencies, wait for API readiness, obtain mock-issued tokens, run all steps with user tokens or cluster API keys | `candidate: 34/34 steps`; no result failures; exit **0** |
| `bash testenv/run.sh repeat` | Run the same current image twice, removing containers and volumes between runs | Candidate and baseline each pass **34/34**; `comparison: 0 differences`; exit **0** |
| `bash testenv/run.sh compare <image>` | Run current source and the selected baseline image independently with identical fixtures | Both must satisfy the scenario assertions and have matching normalized responses; zero differences and exit **0** only if equivalent |
| `bash testenv/snapshot-test.sh` | Validate merged Compose configuration and attempt a nonexistent snapshot | Configuration is valid; the nonexistent snapshot is rejected; overall exit **0** |
| `bash testenv/snapshot-test.sh --restore` | Create a synthetic MongoDB archive, discard the source database, and restore into a fresh one | Exactly one `resourcesv2` document with `uid=snapshot-probe`; zero `apikeys` documents; identical archive SHA-256 before/after; exit **0** |
| `bash testenv/snapshot-test.sh --e2e` | Run the restore checks, then the full API suite twice using separate restores of the synthetic archive | Restore checks pass; both API runs pass **34/34**; zero differences; exit **0** |

The snapshot fixture deliberately includes an API-key record to prove that the
restore excludes it. This is not a comprehensive sanitizer test, nor proof that
every allowed inventory collection is free of embedded credentials. The restore
check samples `resourcesv2` and `apikeys`; it does not individually verify all
allowed/excluded collections. API assertions after restore still concern the
synthetic identities, not every identity present in the source inventory.

Readiness is polled for up to 180 seconds before scenario execution; ordinary
scenario HTTP requests have a 15-second client timeout. Startup failure must
fail the command, not skip the suite. These are safety timeouts, not performance
SLAs. Scenario duration is recorded but is not a pass/fail threshold.

### Comparison rules

Assertions run on the original responses before normalization. For this ACL
suite, only `/created` on create/read/update is ignored, and exact string values
matching captured IDs are replaced by logical aliases. No unordered arrays are
declared in this suite; access-array ordering therefore matters.

The comparator checks report schema, suite fingerprint, declared/completed step
counts, named results, assertion failures, HTTP status, and the digest of each
normalized response plus explicitly selected headers. It rejects missing or
duplicate named results. It does not compare timing, undeclared headers, database
contents, broker messages, or other side effects. A matching digest alone never
overrides failed assertions. Fixture hashes and image/build information are saved
for inspection, but the standalone report comparator does not validate those
separate provenance files.

A real baseline can legitimately differ or lack these endpoints. That is a
reported failure requiring review, not an automatic exemption. A-vs-A success
demonstrates repeatability, not backward compatibility with a released version.

### Reading the results

After stack execution, inspect `artifacts/<run-id>/`:

| Artifact | Expected content and interpretation |
|---|---|
| `candidate.json`, `baseline.json` | `expected: 34`, 34 named `results`, and no nonempty `failures` arrays on success; includes status, normalized digest, and elapsed milliseconds |
| `candidate.xml`, `baseline.xml` | On a full pass, JUnit has `tests="34"` and `failures="0"`; only executed steps are represented, so do not treat absent steps as passes |
| `diff.json` | No differences currently serializes as JSON `null`; a mismatch produces entries with `name` and `reason`, not raw response bodies |
| `exit-code.txt` | `0` for a completed successful orchestration; nonzero for failure after the cleanup trap was installed |
| `startup.log` | Local dependency/application diagnostics retained on failure; may contain sensitive information and is not uploaded by CI |
| `*-image.txt`, `build.txt`, `fixtures.sha256`, optional `snapshot.sha256` | Image identity, Go build metadata, and input hashes for reproducing a run; not correctness assertions |

The `34/34 steps` console line counts executed steps, not passing assertions:
the last step can fail after all steps have executed. Always check command exit
status and report failures. Startup/configuration errors may occur before JSON
or JUnit reports are written. Preflight/build errors can also occur before the
cleanup trap exists, leaving no `exit-code.txt`. Missing reports are not a pass.

## RabbitMQ regression coverage

The real-stack tests exercise normal API startup with its concurrent RabbitMQ
listeners. Before the channel-isolation change, runs exited while declaring the
SSE queue with `unexpected command received`; afterward the API became ready and
completed the ACL scenarios. This is an observed E2E regression check, not a
dedicated deterministic concurrency/stress test or an assertion about channel IDs.

The existing shared-library tests can be run separately:

```sh
go -C ../ror test ./pkg/clients/rabbitmqclient
```

Those tests check the startup health placeholder, delegation after initialization,
and host/port/credential configuration validation. They use no real broker and do
not prove listener channel isolation. Reconnect, credential rotation, channel-only
failure recovery, sustained concurrency, and message delivery/acknowledgement
correctness remain untested by this suite.

## Verified locally

- Harness tests, including race detection, passed.
- All 34 user ACL, cluster isolation, and admin resource-update scenarios passed against current API.
- Two fresh-data runs passed with zero comparison differences.
- Two independent synthetic archive restores passed the same scenarios with
  zero differences; excluded API keys were not restored and source bytes stayed
  unchanged.

The suite exposed an API startup defect in the shared legacy RabbitMQ client:
concurrent listeners used one AMQP RPC channel. The companion `ror` change gives
each listener its own channel. Baseline images predating that fix may fail at
startup; the harness intentionally reports that failure rather than retrying it
into a pass. No production snapshot or remote GitHub CI run was performed.

## Coverage boundary

The suite covers user ACL contracts and cluster API-key authentication with
self/direct-child update isolation. It is not yet full platform E2E coverage:
ror-auth broker flows, cluster/service API-key provisioning through registration,
multi-level ownership graphs, real agent ingestion, CLI workflows, Kubernetes RBAC, upgrade
migrations, and dependency rotation/recovery need additional suites. Planned
broker features cannot be marked passing before their production implementation
exists. Keep explicit positive and negative authorization assertions when adding
coverage; comparing two versions alone is not a correctness oracle.