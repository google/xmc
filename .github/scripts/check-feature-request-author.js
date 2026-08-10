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
 * Orchestrator: Checks if the PR or Merge Group author(s) are listed in FEATURE_REQUEST_AUTHORS.
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const { owner, repo } = context.repo;
  const eventDetails = resolveEventDetails(context);

  if (!eventDetails) {
    core.setFailed('This script must be run in the context of a pull_request or merge_group event.');
    return;
  }

  let prAuthors = eventDetails.prAuthors;

  if (eventDetails.isMergeGroup) {
    const mergeAuthors = await fetchMergeGroupAuthors({
      github,
      owner,
      repo,
      baseSha: eventDetails.baseSha,
      headSha: eventDetails.headSha,
      core,
    });

    if (!mergeAuthors) {
      return; // Failed closed inside helper
    }
    prAuthors = mergeAuthors;
  }

  core.info(`Evaluating ${eventDetails.contextDesc} authored by [${prAuthors.join(', ')}] against target branch '${eventDetails.baseRef}'`);

  const restrictedAuthors = await fetchRestrictedAuthors({
    github,
    owner,
    repo,
    baseRef: eventDetails.baseRef,
    core,
  });

  if (restrictedAuthors === null) {
    return; // Handled 404 (skipped) or API error (failed)
  }

  evaluateAuthorRestrictions({
    prAuthors,
    restrictedAuthors,
    contextDesc: eventDetails.contextDesc,
    baseRef: eventDetails.baseRef,
    core,
  });
}

/**
 * Helper: Resolves initial event details (authors, baseRef, context description) from GitHub event payload.
 * @param {Object} context - GitHub Actions execution context.
 * @returns {Object|null} Extracted event details or null if unhandled event type.
 */
function resolveEventDetails(context) {
  if (context.payload.pull_request) {
    return {
      isMergeGroup: false,
      prAuthors: [context.payload.pull_request.user.login],
      baseRef: context.payload.pull_request.base.ref,
      contextDesc: `PR #${context.payload.pull_request.number}`,
    };
  }

  if (context.eventName === 'merge_group' && context.payload.merge_group) {
    const headSha = context.payload.merge_group.head_sha;
    return {
      isMergeGroup: true,
      prAuthors: [],
      baseRef: (context.payload.merge_group.base_ref || 'main').replace(/^refs\/heads\//, ''),
      headSha,
      baseSha: context.payload.merge_group.base_sha,
      contextDesc: `Merge Group (commit ${headSha?.substring(0, 7)})`,
    };
  }

  return null;
}

/**
 * Helper: Fetches and resolves commit authors across a batched merge group via compareCommits.
 * @returns {Promise<string[]|null>} Array of author logins, or null if validation failed closed.
 */
async function fetchMergeGroupAuthors({ github, owner, repo, baseSha, headSha, core }) {
  try {
    const compareRes = await github.rest.repos.compareCommits({
      owner,
      repo,
      base: baseSha,
      head: headSha,
    });

    const commits = compareRes.data.commits || [];
    const totalCommits = compareRes.data.total_commits || 0;

    if (commits.length === 0) {
      core.setFailed(`No commits found in merge group between ${baseSha?.substring(0, 7)} and ${headSha?.substring(0, 7)}.`);
      return null;
    }

    if (totalCommits > commits.length) {
      core.setFailed(
        `Merge group contains ${totalCommits} commits, which exceeds the maximum of ${commits.length} commits evaluated in a single API call.`
      );
      return null;
    }

    const authorLogins = new Set();
    for (const commit of commits) {
      if (isSystemExemptCommit(commit)) {
        core.info(`Commit ${commit.sha?.substring(0, 7)} is an exempt system/bot commit (@${commit.author?.login}). Skipping restriction check.`);
        continue;
      }

      const login = commit.author?.login;
      if (!login) {
        core.setFailed(
          `Could not resolve GitHub username for commit ${commit.sha?.substring(0, 7)} in merge group. ` +
          `All commits must be made by a verified GitHub account to prevent security bypasses.`
        );
        return null;
      }
      authorLogins.add(login);
    }
    return Array.from(authorLogins);
  } catch (error) {
    core.setFailed(`Failed to fetch commits for merge group: ${error.message}`);
    return null;
  }
}

/**
 * Helper: Fetches and parses the FEATURE_REQUEST_AUTHORS file from the target base branch.
 * @returns {Promise<string[]|null>} Parsed restricted author logins, or null if skipped/errored.
 */
async function fetchRestrictedAuthors({ github, owner, repo, baseRef, core }) {
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
      return null;
    }
    core.setFailed(`Failed to fetch FEATURE_REQUEST_AUTHORS from branch '${baseRef}': ${error.message}`);
    return null;
  }

  const restrictedAuthors = parseRestrictedAuthors(fileContent);
  core.info(`Restricted authors on '${baseRef}': [${restrictedAuthors.join(', ')}]`);
  return restrictedAuthors;
}

/**
 * Helper: Evaluates PR/Merge Group authors against restricted authors and calls core logger/failure functions.
 */
function evaluateAuthorRestrictions({ prAuthors, restrictedAuthors, contextDesc, baseRef, core }) {
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
 * Pure function: Determines whether a commit is an auto-generated system or bot commit.
 * Rely strictly on verified GitHub author logins to prevent unverified Git header spoofing.
 * @param {Object} commit - Commit object from GitHub API.
 * @returns {boolean} True if system/bot exempt, false otherwise.
 */
function isSystemExemptCommit(commit) {
  if (!commit) return false;
  const login = (commit.author?.login || '').toLowerCase();
  return login.endsWith('[bot]') || login === 'web-flow' || login === 'actions-user';
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
  resolveEventDetails,
  fetchMergeGroupAuthors,
  fetchRestrictedAuthors,
  evaluateAuthorRestrictions,
  parseRestrictedAuthors,
  isSystemExemptCommit,
};
