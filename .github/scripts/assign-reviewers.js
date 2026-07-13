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

const fs = require('fs');

const NUM_REVIEWERS_TO_ASSIGN = 2;

/**
 * Orchestrator: Coordinates reading the OWNERS file, calling pure parsing/selection functions,
 * and calling the GitHub REST API to assign reviewers.
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const { owner, repo } = context.repo;
  const {
    prNumber,
    prAuthor,
    prState,
    existingReviewersCount
  } = await getPullRequestMetadata(github, context, core);

  if (!isPullRequestOpen(prState)) {
    core.info(`PR #${prNumber} is not open (state: ${prState}). Skipping reviewer assignment.`);
    return;
  }

  if (shouldSkipAssignment(existingReviewersCount)) {
    core.info(`PR #${prNumber} already has ${existingReviewersCount} reviewer(s) or team(s) assigned. Skipping auto-assignment.`);
    return;
  }

  core.info(`Processing PR #${prNumber} opened by @${prAuthor}`);

  if (!fs.existsSync('OWNERS')) {
    core.setFailed('OWNERS file not found in repository root.');
    return;
  }

  const rawReviewers = parseReviewersFromOwners(fs.readFileSync('OWNERS', 'utf8'));
  if (rawReviewers.length === 0) {
    core.warning('No reviewers defined in OWNERS file.');
    return;
  }

  const candidates = filterEligibleReviewers(rawReviewers, prAuthor);
  core.info(`Available reviewer candidates (excluding author): ${candidates.join(', ')}`);

  if (candidates.length === 0) {
    core.warning('No eligible reviewers available after excluding PR author.');
    return;
  }

  const selectedReviewers = selectRandomReviewers(candidates, NUM_REVIEWERS_TO_ASSIGN);
  core.info(`Selected reviewers to assign: ${selectedReviewers.join(', ')}`);

  try {
    await github.rest.pulls.requestReviewers({
      owner: owner,
      repo: repo,
      pull_number: prNumber,
      reviewers: selectedReviewers
    });
    core.info(`Successfully requested reviews from: ${selectedReviewers.join(', ')}`);
  } catch (error) {
    core.setFailed(`Failed to assign reviewers via GitHub API: ${error.message}`);
  }
}

/**
 * Pure function: Parses a flat text OWNERS file containing one username per line.
 * Strips comments and trims whitespace.
 * @param {string} content - Raw string content of the OWNERS file.
 * @returns {string[]} Array of reviewer candidate usernames.
 */
function parseReviewersFromOwners(content) {
  return (content || '')
    .split(/\r?\n/)
    .map(line => line.split('#')[0].trim())
    .filter(Boolean);
}

/**
 * Pure function: Filters out the PR author from the candidate reviewer list.
 * @param {string[]} candidates - Array of candidate usernames.
 * @param {string} prAuthor - Username of the PR author.
 * @returns {string[]} Filtered array of eligible reviewers.
 */
function filterEligibleReviewers(candidates, prAuthor) {
  const authorLower = (prAuthor || '').toLowerCase();
  return candidates.filter(r => (r || '').toLowerCase() !== authorLower);
}

/**
 * Pure function: Selects up to `count` random reviewers using the Fisher-Yates (Knuth) shuffle algorithm.
 * @param {string[]} candidates - Array of eligible candidate usernames.
 * @param {number} count - Maximum number of reviewers to select.
 * @returns {string[]} Randomly selected reviewers.
 */
function selectRandomReviewers(candidates, count) {
  const shuffled = [...candidates];
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const temp = shuffled[i];
    shuffled[i] = shuffled[j];
    shuffled[j] = temp;
  }
  return shuffled.slice(0, Math.min(count, shuffled.length));
}

/**
 * Pure function: Checks whether the PR is in an open state where reviewers can be assigned.
 * @param {string} prState - State of the pull request (e.g., 'open', 'closed', 'merged').
 * @returns {boolean} True if the PR is open, false otherwise.
 */
function isPullRequestOpen(prState) {
  return (prState || '').toLowerCase() === 'open';
}

/**
 * Pure function: Checks whether existing reviewers or teams are already assigned to the PR.
 * @param {number} existingReviewersCount - Total count of requested reviewers and teams.
 * @returns {boolean} True if reviewers already assigned and auto-assignment should be skipped.
 */
function shouldSkipAssignment(existingReviewersCount) {
  return existingReviewersCount > 0;
}

/**
 * Helper to extract PR details based on the trigger event.
 * @param {Object} github - GitHub REST API client.
 * @param {Object} context - Actions workflow execution context.
 * @param {Object} core - Actions core logger/utilities.
 * @returns {Promise<Object>} Extracted metadata object.
 */
async function getPullRequestMetadata(github, context, core) {
  const isDispatch = context.eventName === 'workflow_dispatch';
  const prNumber = isDispatch
    ? parseInt(context.payload.inputs.pr_number, 10)
    : context.payload.pull_request.number;

  let prData = context.payload.pull_request;

  if (isDispatch) {
    core.info(`Workflow dispatch triggered for PR #${prNumber}`);
    const { data } = await github.rest.pulls.get({
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: prNumber
    });
    prData = data;
  }

  const requestedReviewers = prData.requested_reviewers || [];
  const requestedTeams = prData.requested_teams || [];

  return {
    prNumber,
    prAuthor: prData.user.login,
    prState: prData.state || 'open',
    existingReviewersCount: requestedReviewers.length + requestedTeams.length
  };
}

module.exports = {
  NUM_REVIEWERS_TO_ASSIGN,
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers,
  isPullRequestOpen,
  shouldSkipAssignment,
  getPullRequestMetadata,
  run
};
