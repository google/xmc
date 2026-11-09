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
 * @returns {{ canApprove: boolean, reason: string }} Decision result object.
 */

/**
 * Validates the contents of ABSENCE.md.
 * @param {string} content
 * @returns {{valid: boolean, reason?: string}}
 */
function validateAbsenceContent(content) {
  const lines = (content || '').split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    let line = lines[i].split('#')[0].trim().replace(/^[-*+@\s]+/, '').trim();
    if (!line) continue;

    const parts = line.split(/\s+/);
    if (parts.length > 2) {
      return { valid: false, reason: `Line ${i + 1} has invalid format` };
    }
    if (parts.length === 2) {
      const dateRangeStr = parts[1];
      const match = dateRangeStr.match(/^(\d{2})\.(\d{2})\.(\d{4})-(\d{2})\.(\d{2})\.(\d{4})$/);
      if (!match) {
        return { valid: false, reason: `Line ${i + 1} has invalid date range format` };
      }

      const checkDate = (dd, mm, yyyy) => {
        const d = parseInt(dd, 10);
        const m = parseInt(mm, 10);
        const y = parseInt(yyyy, 10);
        const dateObj = new Date(y, m - 1, d);
        return dateObj.getFullYear() === y && dateObj.getMonth() === m - 1 && dateObj.getDate() === d;
      };

      if (!checkDate(match[1], match[2], match[3])) {
        return { valid: false, reason: `Line ${i + 1} has invalid start date` };
      }
      if (!checkDate(match[4], match[5], match[6])) {
        return { valid: false, reason: `Line ${i + 1} has invalid end date` };
      }

      const dStart = new Date(match[3], match[2] - 1, match[1]);
      const dEnd = new Date(match[6], match[5] - 1, match[4]);
      if (dStart > dEnd) {
        return { valid: false, reason: `Line ${i + 1} start date is after end date` };
      }
    }
  }
  return { valid: true };
}

function shouldAutoApproveAbsencePR({ modifiedFileNames, prAuthor, ownersList, absenceContent }) {
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

  if (absenceContent !== undefined) {
    const validation = validateAbsenceContent(absenceContent);
    if (!validation.valid) {
      return {
        canApprove: false,
        reason: `Invalid ABSENCE.md content: ${validation.reason}`
      };
    }
  }

  return {
    canApprove: true,
    reason: `PR only modifies ABSENCE.md and author @${prAuthor} is an authorized code owner.`
  };
}

/**
 * Orchestrator: Fetches PR files, reads OWNERS, evaluates eligibility, and posts approval review via REST API.
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
  const absenceContent = fs.existsSync('ABSENCE.md') ? fs.readFileSync('ABSENCE.md', 'utf8') : undefined;
  const decision = shouldAutoApproveAbsencePR({ modifiedFileNames, prAuthor, ownersList, absenceContent });

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
  validateAbsenceContent,
  isOnlyAbsenceFileModified,
  isAuthorCodeOwner,
  shouldAutoApproveAbsencePR,
  run
};
