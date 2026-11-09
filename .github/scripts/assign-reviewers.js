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

const NUM_REVIEWERS_TO_ASSIGN = 1;

const USERNAME_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9_-]*[A-Za-z0-9])?$/;
const DATE_PATTERN = /^(\d{2})\.(\d{2})\.(\d{4})$/;
const DATE_RANGE_PATTERN = /^([^-\s]+)-([^-\s]+)$/;

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

  let absentUsers = [];
  if (fs.existsSync('ABSENCE.md')) {
    absentUsers = parseAbsenceFile(fs.readFileSync('ABSENCE.md', 'utf8'));
    core.info(`Parsed absent reviewers from ABSENCE.md: ${absentUsers.join(', ')}`);
  }

  const candidates = filterEligibleReviewers(rawReviewers, prAuthor, absentUsers);
  core.info(`Available reviewer candidates (excluding author & absent): ${candidates.join(', ')}`);

  if (candidates.length === 0) {
    core.warning('No eligible reviewers available after excluding PR author and absent reviewers.');
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
 * Pure function: Strictly parses a DD.MM.YYYY calendar date string.
 * Rejects impossible calendar dates (e.g., 31.02.2026 or 29.02.2026).
 * @param {string} dateStr - Date string in DD.MM.YYYY format.
 * @returns {Date|null} Parsed local midnight Date or null if invalid.
 */
function parseDdMmYyyy(dateStr) {
  const match = (dateStr || '').match(DATE_PATTERN);
  if (!match) {
    return null;
  }
  const day = parseInt(match[1], 10);
  const month = parseInt(match[2], 10);
  const year = parseInt(match[3], 10);
  const date = new Date(year, month - 1, day);
  date.setFullYear(year, month - 1, day);
  if (
    date.getFullYear() !== year ||
    date.getMonth() !== month - 1 ||
    date.getDate() !== day
  ) {
    return null;
  }
  return date;
}

/**
 * Pure function: Parses and validates a DD.MM.YYYY-DD.MM.YYYY date range token.
 * @param {string} rangeStr - Date range token.
 * @param {number} lineNumber - 1-based line number for error reporting.
 * @returns {{ valid: boolean, startDate?: Date, endDate?: Date, reason?: string }}
 */
function parseAbsenceDateRange(rangeStr, lineNumber) {
  const match = (rangeStr || '').match(DATE_RANGE_PATTERN);
  if (!match) {
    return {
      valid: false,
      reason: `Line ${lineNumber} has invalid date range format "${rangeStr}" (expected DD.MM.YYYY-DD.MM.YYYY)`
    };
  }
  const startDate = parseDdMmYyyy(match[1]);
  if (!startDate) {
    return {
      valid: false,
      reason: `Line ${lineNumber} has invalid start date "${match[1]}" (expected valid DD.MM.YYYY calendar date)`
    };
  }
  const endDate = parseDdMmYyyy(match[2]);
  if (!endDate) {
    return {
      valid: false,
      reason: `Line ${lineNumber} has invalid end date "${match[2]}" (expected valid DD.MM.YYYY calendar date)`
    };
  }
  if (startDate > endDate) {
    return {
      valid: false,
      reason: `Line ${lineNumber} has start date "${match[1]}" after end date "${match[2]}"`
    };
  }
  return { valid: true, startDate, endDate };
}

/**
 * Pure function: Parses and validates a single line from ABSENCE.md.
 * Supports either bare "NICK" or "NICK DD.MM.YYYY-DD.MM.YYYY" (inclusive).
 * @param {string} rawLine - Raw line from ABSENCE.md.
 * @param {number} [lineNumber=1] - 1-based line number for error reporting.
 * @returns {{ empty: boolean, valid: boolean, username?: string, startDate?: Date|null, endDate?: Date|null, reason?: string }}
 */
function parseAbsenceLine(rawLine, lineNumber = 1) {
  const cleaned = (rawLine || '').split('#')[0].trim().replace(/^[-*+@\s]+/, '').trim();
  if (!cleaned) {
    return { empty: true, valid: true };
  }
  const parts = cleaned.split(/\s+/);
  if (parts.length > 2) {
    return {
      empty: false,
      valid: false,
      reason: `Line ${lineNumber} has invalid format "${cleaned}" (expected "NICK" or "NICK DD.MM.YYYY-DD.MM.YYYY")`
    };
  }
  const username = parts[0];
  if (!USERNAME_PATTERN.test(username)) {
    return {
      empty: false,
      valid: false,
      reason: `Line ${lineNumber} has invalid username "${username}"`
    };
  }
  if (parts.length === 1) {
    return { empty: false, valid: true, username, startDate: null, endDate: null };
  }
  const rangeResult = parseAbsenceDateRange(parts[1], lineNumber);
  if (!rangeResult.valid) {
    return { empty: false, valid: false, reason: rangeResult.reason };
  }
  return {
    empty: false,
    valid: true,
    username,
    startDate: rangeResult.startDate,
    endDate: rangeResult.endDate
  };
}

/**
 * Pure function: Parses ABSENCE.md content and returns usernames absent on `currentDate`.
 * Bare "NICK" entries are unconditionally absent; "NICK X-Y" entries are absent when
 * `currentDate` falls within `[X, Y]` inclusive (with valid DD.MM.YYYY dates and X <= Y).
 * @param {string} content - Raw string content of ABSENCE.md.
 * @param {Date} [currentDate=new Date()] - Date to evaluate scheduled absences against.
 * @returns {string[]} Array of currently absent usernames.
 */
function parseAbsenceFile(content, currentDate = new Date()) {
  const today = new Date(
    currentDate.getFullYear(),
    currentDate.getMonth(),
    currentDate.getDate()
  );
  today.setFullYear(
    currentDate.getFullYear(),
    currentDate.getMonth(),
    currentDate.getDate()
  );
  const absentUsers = [];
  const lines = (content || '').split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const entry = parseAbsenceLine(lines[i], i + 1);
    if (entry.empty || !entry.valid) {
      continue;
    }
    if (!entry.startDate || (today >= entry.startDate && today <= entry.endDate)) {
      absentUsers.push(entry.username);
    }
  }
  return absentUsers;
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
 * Pure function: Filters out the PR author and absent reviewers from the candidate reviewer list.
 * @param {string[]} candidates - Array of candidate usernames.
 * @param {string} prAuthor - Username of the PR author.
 * @param {string[]} [absentUsers=[]] - Array of absent reviewer usernames.
 * @returns {string[]} Filtered array of eligible reviewers.
 */
function filterEligibleReviewers(candidates, prAuthor, absentUsers = []) {
  const authorLower = (prAuthor || '').toLowerCase();
  const absentSet = new Set((absentUsers || []).map(u => (u || '').toLowerCase()));
  return candidates.filter(r => {
    const lower = (r || '').toLowerCase();
    return lower !== authorLower && !absentSet.has(lower);
  });
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
  parseDdMmYyyy,
  parseAbsenceLine,
  parseAbsenceFile,
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers,
  isPullRequestOpen,
  shouldSkipAssignment,
  getPullRequestMetadata,
  run
};
