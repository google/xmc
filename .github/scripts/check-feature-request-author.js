// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * Checks if the PR author is listed in FEATURE_REQUEST_AUTHORS on the target base branch.
 * Supports both pull_request and merge_group event contexts.
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const { owner, repo } = context.repo;
  let prAuthors = [];
  let baseRef = '';
  let contextDesc = '';

  if (context.payload.pull_request) {
    prAuthors = [context.payload.pull_request.user.login];
    baseRef = context.payload.pull_request.base.ref;
    contextDesc = `PR #${context.payload.pull_request.number}`;
  } else if (context.eventName === 'merge_group' && context.payload.merge_group) {
    baseRef = (context.payload.merge_group.base_ref || 'main').replace(/^refs\/heads\//, '');
    const headSha = context.payload.merge_group.head_sha;
    const baseSha = context.payload.merge_group.base_sha;
    contextDesc = `Merge Group (commit ${headSha?.substring(0, 7)})`;

    try {
      const compareRes = await github.rest.repos.compareCommits({
        owner,
        repo,
        base: baseSha,
        head: headSha,
      });

      const commits = compareRes.data.commits || [];
      const authorLogins = new Set();
      for (const commit of commits) {
        const login = commit.author?.login;
        if (!login) {
          core.setFailed(`Could not resolve GitHub username for commit ${commit.sha?.substring(0, 7)} in merge group.`);
          return;
        }
        authorLogins.add(login);
      }
      prAuthors = Array.from(authorLogins);
      if (prAuthors.length === 0) {
        core.setFailed(`No commits found in merge group between ${baseSha?.substring(0, 7)} and ${headSha?.substring(0, 7)}.`);
        return;
      }
    } catch (error) {
      core.setFailed(`Failed to fetch commits for merge group: ${error.message}`);
      return;
    }
  } else {
    core.setFailed('This script must be run in the context of a pull_request or merge_group event.');
    return;
  }

  core.info(`Evaluating ${contextDesc} authored by [${prAuthors.join(', ')}] against target branch '${baseRef}'`);

  let fileContent = '';
  try {
    const response = await github.rest.repos.getContent({
      owner,
      repo,
      path: 'FEATURE_REQUEST_AUTHORS',
      ref: baseRef,
    });

    if (response.data.content) {
      fileContent = Buffer.from(response.data.content, 'base64').toString('utf8');
    }
  } catch (error) {
    if (error.status === 404) {
      core.info(`FEATURE_REQUEST_AUTHORS file not found on branch '${baseRef}'. Skipping merge block.`);
      return;
    }
    core.setFailed(`Failed to fetch FEATURE_REQUEST_AUTHORS from branch '${baseRef}': ${error.message}`);
    return;
  }

  const restrictedAuthors = parseRestrictedAuthors(fileContent);
  core.info(`Restricted authors on '${baseRef}': [${restrictedAuthors.join(', ')}]`);

  const restrictedMatches = prAuthors.filter((author) =>
    restrictedAuthors.includes(author.toLowerCase())
  );

  if (restrictedMatches.length > 0) {
    core.setFailed(
      `${contextDesc} contains commit(s) authored by restricted author(s): [${restrictedMatches.map(a => `@${a}`).join(', ')}], ` +
      `who are listed in FEATURE_REQUEST_AUTHORS. This change is treated as a Feature Request and is permanently blocked from merging.`
    );
  } else {
    core.info(`No authors in [${prAuthors.join(', ')}] are listed in FEATURE_REQUEST_AUTHORS. Merge block check passed.`);
  }
}

/**
 * Pure function: Parses FEATURE_REQUEST_AUTHORS string content into lowercase usernames.
 * Strips comments and trims whitespace.
 * @param {string} content - Raw string content of FEATURE_REQUEST_AUTHORS file.
 * @returns {string[]} Array of restricted author usernames in lowercase.
 */
function parseRestrictedAuthors(content) {
  return (content || '')
    .split(/\r?\n/)
    .map((line) => line.split('#')[0].trim().toLowerCase())
    .filter(Boolean);
}

module.exports = {
  run,
  parseRestrictedAuthors,
};
