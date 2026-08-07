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
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const prAuthor = context.payload.pull_request.user.login;
  const baseRef = context.payload.pull_request.base.ref;
  const { owner, repo } = context.repo;

  core.info(`Evaluating PR #${context.payload.pull_request.number} opened by @${prAuthor} against target branch '${baseRef}'`);

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

  const isRestricted = restrictedAuthors.some(
    (author) => author.toLowerCase() === prAuthor.toLowerCase()
  );

  if (isRestricted) {
    core.setFailed(
      `PR #${context.payload.pull_request.number} is opened by @${prAuthor}, who is listed in FEATURE_REQUEST_AUTHORS. ` +
      `This PR is treated as a Feature Request and is permanently blocked from merging.`
    );
  } else {
    core.info(`@${prAuthor} is not in FEATURE_REQUEST_AUTHORS. Merge block check passed.`);
  }
}

/**
 * Pure function: Parses FEATURE_REQUEST_AUTHORS string content into lowercase usernames.
 * Strips comments and trims whitespace.
 * @param {string} content - Raw string content of FEATURE_REQUEST_AUTHORS file.
 * @returns {string[]} Array of restricted author usernames.
 */
function parseRestrictedAuthors(content) {
  return (content || '')
    .split(/\r?\n/)
    .map((line) => line.split('#')[0].trim())
    .filter(Boolean);
}

module.exports = {
  run,
  parseRestrictedAuthors,
};
