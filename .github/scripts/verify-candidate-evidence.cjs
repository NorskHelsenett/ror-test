const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');

function verifyEvidence(root, env = process.env) {
  const runs = fs.readdirSync(path.join(root, 'artifacts')).filter(name => name.startsWith('e2e-'));
  assert.equal(runs.length, 1, 'expected exactly one run in clean CI checkout');
  const directory = path.join(root, 'artifacts', runs[0]);
  const read = name => fs.readFileSync(path.join(directory, name), 'utf8');
  const json = name => JSON.parse(read(name));
  const sha256 = data => 'sha256:' + crypto.createHash('sha256').update(data).digest('hex');
  assert.equal(read('exit-code.txt').trim(), '0', 'orchestration did not succeed');
  assert.equal(read('harness-commit.txt').trim(), env.EXPECTED_HARNESS, 'harness commit mismatch');
  assert.equal(read('harness-worktree.txt').trim(), '', 'dirty harness checkout');
  const image = json('candidate-image.json');
  assert.equal(image.archiveSha256, env.EXPECTED_ARCHIVE);
  assert.equal(image.indexDigest, env.EXPECTED_INDEX);
  assert.equal(image.manifestDigest, env.EXPECTED_MANIFEST);
  assert.equal(image.platform, env.EXPECTED_PLATFORM);
  const runtime = json('candidate-runtime.json');
  assert(runtime.Id === image.configDigest || runtime.Id === image.manifestDigest, 'loaded image differs from archive');
  assert.equal(`${runtime.Os}/${runtime.Architecture}`, image.platform);
  const suite = JSON.parse(fs.readFileSync(path.join(root, 'testenv/scenarios/acl.json'), 'utf8'));
  for (const label of env.BASELINE_IMAGE ? ['candidate', 'baseline'] : ['candidate']) {
    const report = json(`${label}.json`);
    const verified = json(`${label}-verified.json`);
    assert.equal(verified.schema, 1);
    assert.equal(verified.reportDigest, sha256(read(`${label}.json`)), 'report changed after verification');
    assert.equal(verified.suite, report.suite);
    assert.equal(verified.passed, suite.steps.length);
    assert.equal(report.expected, suite.steps.length);
    assert.equal(report.results.length, suite.steps.length);
    suite.steps.forEach((step, index) => {
      const result = report.results[index];
      assert.equal(result.name, step.name);
      assert.equal(result.status, step.status);
      assert.equal((result.failures || []).length, 0);
      assert.match(result.digest, /^[0-9a-f]{64}$/);
    });
  }
  if (env.BASELINE_IMAGE) {
    const diff = json('diff.json');
    assert(diff === null || (Array.isArray(diff) && diff.length === 0), 'baseline differs');
  }
  assert.match(env.SOURCE_SHA || '', /^[0-9a-f]{40}$/);
  const evidence = {
    schema: 1, candidate: image, sourceSHA: env.SOURCE_SHA,
    harnessSHA: env.EXPECTED_HARNESS, inputArtifactID: env.INPUT_ARTIFACT_ID,
    repository: env.GITHUB_REPOSITORY, runID: env.GITHUB_RUN_ID, runAttempt: env.GITHUB_RUN_ATTEMPT,
    suite: json('candidate.json').suite, passed: suite.steps.length,
    reportDigest: sha256(read('candidate.json')),
    suiteFileDigest: sha256(fs.readFileSync(path.join(root, 'testenv/scenarios/acl.json'))),
    fixtureFileDigest: sha256(fs.readFileSync(path.join(root, 'testenv/seed.js'))),
    issuerFileDigest: sha256(fs.readFileSync(path.join(root, 'testenv/oidc.json'))),
    baseline: env.BASELINE_IMAGE || null,
  };
  fs.writeFileSync(path.join(directory, 'release-evidence.json'), JSON.stringify(evidence, null, 2) + '\n', { mode: 0o600 });
  return evidence;
}

module.exports = verifyEvidence;
if (require.main === module) {
  verifyEvidence(process.argv[2] || '.');
  console.log('Candidate evidence matches the requested artifact, platform, harness and complete passing suite.');
}