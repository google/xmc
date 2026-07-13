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
 * Pure function: Parses all usernames under the `reviewers:` section of an OWNERS YAML string.
 * @param {string} content - Raw string content of the OWNERS file.
 * @returns {string[]} Array of reviewer candidate usernames.
 */
const parseReviewersFromOwners = (content) => {
  const lines = content.split(/\r?\n/);
  const reviewers = [];
  let inReviewersSection = false;

  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.startsWith('#') || !trimmed) continue;
    if (trimmed.startsWith('reviewers:')) {
      inReviewersSection = true;
      continue;
    }
    if (inReviewersSection && trimmed.startsWith('- ')) {
      reviewers.push(trimmed.substring(2).trim());
    } else if (inReviewersSection && trimmed.endsWith(':')) {
      inReviewersSection = false;
    }
  }
  return reviewers;
};

/**
 * Pure function: Filters out the PR author from the candidate reviewer list.
 * @param {string[]} candidates - Array of candidate usernames.
 * @param {string} prAuthor - Username of the PR author.
 * @returns {string[]} Filtered array of eligible reviewers.
 */
const filterEligibleReviewers = (candidates, prAuthor) => {
  const authorLower = (prAuthor || '').toLowerCase();
  return candidates.filter(r => (r || '').toLowerCase() !== authorLower);
};

/**
 * Pure function: Selects up to `count` random reviewers using the Fisher-Yates (Knuth) shuffle algorithm.
 * @param {string[]} candidates - Array of eligible candidate usernames.
 * @param {number} count - Maximum number of reviewers to select.
 * @returns {string[]} Randomly selected reviewers.
 */
const selectRandomReviewers = (candidates, count) => {
  const shuffled = [...candidates];
  for (let i = shuffled.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const temp = shuffled[i];
    shuffled[i] = shuffled[j];
    shuffled[j] = temp;
  }
  return shuffled.slice(0, Math.min(count, shuffled.length));
};

/**
 * Pure function: Checks whether existing reviewers or teams are already assigned to the PR.
 * @param {number} existingReviewersCount - Total count of requested reviewers and teams.
 * @returns {boolean} True if reviewers already assigned and auto-assignment should be skipped.
 */
const shouldSkipAssignment = (existingReviewersCount) => {
  return existingReviewersCount > 0;
};

/**
 * Orchestrator: Coordinates reading the OWNERS file, calling pure parsing/selection functions,
 * and calling the GitHub REST API to assign reviewers.
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
const run = async ({ github, context, core }) => {
  const { owner, repo } = context.repo;
  let prNumber;
  let prAuthor;
  let existingReviewersCount = 0;

  if (context.eventName === 'workflow_dispatch') {
    prNumber = parseInt(context.payload.inputs.pr_number, 10);
    core.info(`Workflow dispatch triggered for PR #${prNumber}`);
    const { data: pr } = await github.rest.pulls.get({
      owner: owner,
      repo: repo,
      pull_number: prNumber
    });
    prAuthor = pr.user.login;
    const requestedReviewers = pr.requested_reviewers || [];
    const requestedTeams = pr.requested_teams || [];
    existingReviewersCount = requestedReviewers.length + requestedTeams.length;
  } else {
    prNumber = context.payload.pull_request.number;
    prAuthor = context.payload.pull_request.user.login;
    const requestedReviewers = context.payload.pull_request.requested_reviewers || [];
    const requestedTeams = context.payload.pull_request.requested_teams || [];
    existingReviewersCount = requestedReviewers.length + requestedTeams.length;
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
};

module.exports = {
  NUM_REVIEWERS_TO_ASSIGN,
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers,
  shouldSkipAssignment,
  run
};
