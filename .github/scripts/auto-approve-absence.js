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
const { parseReviewersFromOwners } = require('./assign-reviewers');
const { validateAbsenceContent } = require('./absence-schedule');

/**
 * Pure function: Checks if the list of modified files contains strictly ABSENCE.md and nothing else.
 * @param {string[]} modifiedFileNames - Array of file paths modified in the PR.
 * @returns {boolean} True if only ABSENCE.md was modified.
 */
function isOnlyAbsenceFileModified(modifiedFileNames) {
  if (!Array.isArray(modifiedFileNames) || modifiedFileNames.length !== 1) {
    return false;
  }
  return modifiedFileNames[0] === 'ABSENCE.md';
}

/**
 * Pure function: Checks if the PR author is listed in the code owners array (case-insensitively).
 * @param {string} prAuthor - Username of the PR author.
 * @param {string[]} ownersList - Array of code owner usernames.
 * @returns {boolean} True if author is in code owners list.
 */
function isAuthorCodeOwner(prAuthor, ownersList) {
  if (!prAuthor || !Array.isArray(ownersList)) {
    return false;
  }
  const authorLower = prAuthor.toLowerCase();
  return ownersList.some(owner => (owner || '').toLowerCase() === authorLower);
}

/**
 * Pure function: Evaluates whether a PR modifying ABSENCE.md should be auto-approved.
 * @param {Object} params
 * @param {string[]} params.modifiedFileNames - Array of file paths modified in the PR.
 * @param {string} params.prAuthor - Username of the PR author.
 * @param {string[]} params.ownersList - Array of code owner usernames.
 * @param {string} [params.absenceContent=''] - Raw content of ABSENCE.md.
 * @returns {{ canApprove: boolean, reason: string }} Decision result object.
 */
function shouldAutoApproveAbsencePR({
  modifiedFileNames,
  prAuthor,
  ownersList,
  absenceContent = ''
}) {
  if (!isOnlyAbsenceFileModified(modifiedFileNames)) {
    return {
      canApprove: false,
      reason: `PR modifies files other than ABSENCE.md: ${(modifiedFileNames || []).join(', ')}`
    };
  }

  if (!isAuthorCodeOwner(prAuthor, ownersList)) {
    return {
      canApprove: false,
      reason: `PR author @${prAuthor} is not listed in OWNERS.`
    };
  }

  const validation = validateAbsenceContent(absenceContent);
  if (!validation.valid) {
    return {
      canApprove: false,
      reason: `Invalid ABSENCE.md content: ${validation.reason}`
    };
  }

  return {
    canApprove: true,
    reason: `PR only modifies ABSENCE.md and author @${prAuthor} is an authorized code owner.`
  };
}

/**
 * Fetches and decodes ABSENCE.md from the pull request head commit via GitHub REST API.
 * @param {Object} github - GitHub REST API client.
 * @param {string} owner - Repository owner.
 * @param {string} repo - Repository name.
 * @param {string} ref - Pull request head commit SHA.
 * @returns {Promise<string>} Decoded UTF-8 content of ABSENCE.md.
 */
async function fetchPrAbsenceContent(github, owner, repo, ref) {
  const { data } = await github.rest.repos.getContent({
    owner,
    repo,
    path: 'ABSENCE.md',
    ref
  });
  if (!data || Array.isArray(data) || typeof data.content !== 'string') {
    throw new Error('Unexpected response format for ABSENCE.md');
  }
  return Buffer.from(data.content, data.encoding || 'base64').toString('utf8');
}

/**
 * Orchestrator: Fetches PR files and head ABSENCE.md via REST API, reads trusted base-branch
 * OWNERS from disk, evaluates eligibility, and posts approval review.
 * @param {Object} params - Context and tools injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const pr = context.payload ? context.payload.pull_request : null;
  if (!pr) {
    core.setFailed('This workflow can only be run in the context of a pull request.');
    return;
  }

  const prNumber = pr.number;
  const prAuthor = pr.user ? pr.user.login : '';
  const headSha = pr.head ? pr.head.sha : '';
  if (!headSha) {
    core.setFailed('Pull request payload is missing head SHA.');
    return;
  }

  const { data: files } = await github.rest.pulls.listFiles({
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: prNumber
  });

  const modifiedFileNames = files.map(f => f.filename);

  if (!fs.existsSync('OWNERS')) {
    core.setFailed('OWNERS file not found in repository root.');
    return;
  }

  const ownersList = parseReviewersFromOwners(fs.readFileSync('OWNERS', 'utf8'));
  if (!isOnlyAbsenceFileModified(modifiedFileNames) || !isAuthorCodeOwner(prAuthor, ownersList)) {
    const precheck = shouldAutoApproveAbsencePR({ modifiedFileNames, prAuthor, ownersList });
    core.info(`${precheck.reason} Skipping auto-approval.`);
    return;
  }

  let absenceContent;
  try {
    absenceContent = await fetchPrAbsenceContent(
      github,
      context.repo.owner,
      context.repo.repo,
      headSha
    );
  } catch (error) {
    core.info(`Failed to fetch ABSENCE.md at ${headSha}: ${error.message}. Skipping auto-approval.`);
    return;
  }

  const decision = shouldAutoApproveAbsencePR({
    modifiedFileNames,
    prAuthor,
    ownersList,
    absenceContent
  });

  if (!decision.canApprove) {
    core.info(`${decision.reason} Skipping auto-approval.`);
    return;
  }

  core.info(`${decision.reason} Auto-approving PR #${prNumber}.`);

  try {
    await github.rest.pulls.createReview({
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: prNumber,
      event: 'APPROVE',
      body: 'Auto-approved: ABSENCE.md update by authorized code owner.'
    });
    core.info(`Successfully auto-approved PR #${prNumber}`);
  } catch (error) {
    core.setFailed(`Failed to approve PR #${prNumber}: ${error.message}`);
  }
}

module.exports = {
  fetchPrAbsenceContent,
  validateAbsenceContent,
  isOnlyAbsenceFileModified,
  isAuthorCodeOwner,
  shouldAutoApproveAbsencePR,
  run
};
