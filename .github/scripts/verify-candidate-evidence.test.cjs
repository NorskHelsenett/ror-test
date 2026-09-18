const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const verify = require('./verify-candidate-evidence.cjs');

test('candidate evidence binds a complete report to the requested build inputs', () => {
  for (const variant of ['valid', 'failed', 'missing', 'altered', 'wrong platform', 'wrong manifest', 'dirty harness', 'wrong status', 'baseline differs']) {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-evidence-'));
    try {
      const directory = path.join(root, 'artifacts/e2e-test');
      fs.mkdirSync(directory, { recursive: true });
      fs.mkdirSync(path.join(root, 'testenv/scenarios'), { recursive: true });
      const write = (name, data) => fs.writeFileSync(path.join(directory, name), data);
      const sha = 'sha256:' + 'a'.repeat(64);
      const env = { EXPECTED_ARCHIVE: sha, EXPECTED_INDEX: sha, EXPECTED_MANIFEST: sha, EXPECTED_PLATFORM: 'linux/arm64', EXPECTED_HARNESS: 'b'.repeat(40), SOURCE_SHA: 'c'.repeat(40), INPUT_ARTIFACT_ID: '123' };
      const suite = { steps: [{ name: 'deny', status: 403 }] };
      const report = JSON.stringify({ suite: 'suite', expected: 1, results: [{ name: 'deny', status: variant === 'wrong status' ? 200 : 403, digest: 'a'.repeat(64) }] });
      const evidence = { schema: 1, passed: 1, suite: 'suite', reportDigest: 'sha256:' + crypto.createHash('sha256').update(report).digest('hex') };
      fs.writeFileSync(path.join(root, 'testenv/scenarios/acl.json'), JSON.stringify(suite));
      fs.writeFileSync(path.join(root, 'testenv/seed.js'), 'fixture');
      fs.writeFileSync(path.join(root, 'testenv/oidc.json'), '{}');
      write('candidate.json', report);
      write('candidate-verified.json', JSON.stringify(evidence));
      write('exit-code.txt', variant === 'failed' ? '1' : '0');
      write('harness-commit.txt', env.EXPECTED_HARNESS);
      write('harness-worktree.txt', variant === 'dirty harness' ? 'modified' : '');
      write('candidate-image.json', JSON.stringify({ archiveSha256: sha, indexDigest: sha, manifestDigest: variant === 'wrong manifest' ? 'other' : sha, configDigest: sha, platform: env.EXPECTED_PLATFORM }));
      write('candidate-runtime.json', JSON.stringify({ Id: sha, Os: 'linux', Architecture: variant === 'wrong platform' ? 'amd64' : 'arm64' }));
      if (variant === 'missing') fs.unlinkSync(path.join(directory, 'candidate.json'));
      if (variant === 'altered') write('candidate.json', report + ' ');
      if (variant === 'baseline differs') {
        env.BASELINE_IMAGE = 'registry/baseline@' + sha;
        write('baseline.json', report);
        write('baseline-verified.json', JSON.stringify(evidence));
        write('diff.json', JSON.stringify([{ reason: 'response differs' }]));
      }
      if (variant === 'valid') {
        assert.equal(verify(root, env).passed, 1);
        assert(fs.existsSync(path.join(directory, 'release-evidence.json')));
      } else {
        assert.throws(() => verify(root, env), { name: /Error/ }, variant);
        assert(!fs.existsSync(path.join(directory, 'release-evidence.json')));
      }
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  }
});