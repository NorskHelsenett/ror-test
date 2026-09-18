module.exports = async function resolveRefs({ github, core }, env = process.env) {
  for (const [repo, ref, output] of [
    ['ror', env.SHARED_REF, 'shared_sha'],
    ['ror-api', env.API_REF, 'api_sha'],
  ]) {
    try {
      const { data } = await github.rest.repos.getCommit({
        owner: 'NorskHelsenett', repo, ref,
      });
      if (!/^[0-9a-f]{40}$/i.test(data.sha)) {
        throw new Error('GitHub returned an invalid commit SHA');
      }
      core.setOutput(output, data.sha);
      core.info(`${repo}: resolved commit ${data.sha}`);
    } catch (error) {
      core.setFailed(`Cannot resolve ${repo} ref ${JSON.stringify(ref)} (status ${error.status || 'unknown'}). Verify the ref exists in that repository and the token has read access.`);
      return;
    }
  }
};