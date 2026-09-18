const assert = require('node:assert/strict');
const test = require('node:test');
const resolveRefs = require('./resolve-refs.cjs');

const sharedSHA = 'f13b9a5' + '0'.repeat(33);
const apiSHA = 'a'.repeat(40);

function harness(getCommit) {
  const outputs = {};
  const failures = [];
  return {
    outputs, failures,
    github: { rest: { repos: { getCommit } } },
    core: {
      setOutput: (name, value) => { outputs[name] = value; },
      setFailed: message => failures.push(message),
      info: () => {},
    },
  };
}

test('resolves short SHAs, branches, tags and full SHAs in the correct repositories', async () => {
  for (const ref of ['f13b9a5', 'feature/acl', 'v1.2.3', sharedSHA]) {
    const calls = [];
    const state = harness(async args => {
      calls.push(args);
      return { data: { sha: args.repo === 'ror' ? sharedSHA : apiSHA } };
    });
    await resolveRefs(state, { SHARED_REF: ref, API_REF: ref });
    assert.deepEqual(calls, [
      { owner: 'NorskHelsenett', repo: 'ror', ref },
      { owner: 'NorskHelsenett', repo: 'ror-api', ref },
    ]);
    assert.deepEqual(state.outputs, { shared_sha: sharedSHA, api_sha: apiSHA });
    assert.deepEqual(state.failures, []);
  }
});

test('fails closed when GitHub rejects a missing, inaccessible or ambiguous ref', async () => {
  for (const status of [404, 403, 422]) {
    const state = harness(async () => { throw Object.assign(new Error('API failure'), { status }); });
    await resolveRefs(state, { SHARED_REF: 'missing', API_REF: 'main' });
    assert.deepEqual(state.outputs, {});
    assert.equal(state.failures.length, 1);
    assert.match(state.failures[0], new RegExp(String(status)));
  }
});

test('rejects malformed SHA responses', async () => {
  const state = harness(async () => ({ data: { sha: 'f13b9a5' } }));
  await resolveRefs(state, { SHARED_REF: 'main', API_REF: 'main' });
  assert.deepEqual(state.outputs, {});
  assert.equal(state.failures.length, 1);
});