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

const { execFileSync } = require('child_process');
const fs = require('fs');
const { parseReviewersFromOwners } = require('./assign-reviewers');
const { isAuthorCodeOwner } = require('./auto-approve-absence');

const MAX_OWNER_GRACE_LINES = 10;

/**
 * Pure function: Finds the latest active approved review on a PR from eligible reviewers.
 * Tracks the latest review per reviewer so approvals superseded by changes requested are ignored.
 * @param {Array<Object>} reviews - List of review objects from GitHub API.
 * @param {string} [prAuthor=''] - Username of the PR author to ignore self-reviews.
 * @returns {{ approver: string, approvedSha: string, submittedAt: string } | null}
 */
function findLatestApproval(reviews, prAuthor = '') {
  if (!Array.isArray(reviews) || reviews.length === 0) {
    return null;
  }

  const authorLower = (prAuthor || '').toLowerCase();
  const latestReviewByReviewer = new Map();

  for (const review of reviews) {
    const reviewer = review.user ? review.user.login : '';
    if (!reviewer || reviewer.toLowerCase() === authorLower) {
      continue;
    }
    latestReviewByReviewer.set(reviewer.toLowerCase(), review);
  }

  const activeApprovals = Array.from(latestReviewByReviewer.values()).filter(
    r => r.state === 'APPROVED' && r.commit_id
  );

  if (activeApprovals.length === 0) {
    return null;
  }

  activeApprovals.sort((a, b) => new Date(b.submitted_at) - new Date(a.submitted_at));
  const latest = activeApprovals[0];

  return {
    approver: latest.user.login,
    approvedSha: latest.commit_id,
    submittedAt: latest.submitted_at
  };
}

/**
 * Pure function: Parses git range-diff output to extract changed patch lines (additions + deletions).
 * @param {string} output - Raw output of `git range-diff`.
 * @returns {{ additions: number, deletions: number, totalDelta: number, isCleanRebase: boolean }}
 */
function parseRangeDiff(output) {
  if (!output || typeof output !== 'string' || output.trim() === '') {
    return { additions: 0, deletions: 0, totalDelta: 0, isCleanRebase: true };
  }

  const lines = output.split(/\r?\n/);
  let additions = 0;
  let deletions = 0;
  let inDiffHunk = false;
  let hasModifiedCommits = false;

  for (const line of lines) {
    const commitMatch = line.match(/^\s*(?:\d+|-):\s+([a-f0-9-]+)\s+([=!<>])\s+(?:\d+|-):\s+([a-f0-9-]+)/);
    if (commitMatch) {
      if (commitMatch[2] !== '=') {
        hasModifiedCommits = true;
      }
      inDiffHunk = false;
      continue;
    }

    if (line.trim().startsWith('@@ Metadata') || line.trim().startsWith('Author:') || line.trim().startsWith('Commit:')) {
      inDiffHunk = false;
      continue;
    }

    if (line.trim().startsWith('## ') && line.trim().endsWith(' ##')) {
      inDiffHunk = true;
      continue;
    }

    if (line.trim().startsWith('@@') && line.trim().endsWith('@@')) {
      inDiffHunk = true;
      continue;
    }

    if (inDiffHunk) {
      const trimmedLeading = line.replace(/^\s{0,4}/, '');
      if (trimmedLeading.startsWith('+') && !trimmedLeading.startsWith('+++')) {
        additions++;
      } else if (trimmedLeading.startsWith('-') && !trimmedLeading.startsWith('---')) {
        deletions++;
      }
    }
  }

  const totalDelta = additions + deletions;
  const isCleanRebase = !hasModifiedCommits && totalDelta === 0;

  return { additions, deletions, totalDelta, isCleanRebase };
}

/**
 * Pure function: Evaluates whether a synchronized PR should be auto-reapproved.
 * @param {Object} params
 * @param {boolean} params.hasPriorApproval
 * @param {string} params.approvedSha
 * @param {string} params.currentSha
 * @param {boolean} params.isAuthorOwner
 * @param {number} params.deltaLines
 * @param {boolean} params.isCleanRebase
 * @param {number} [params.maxGraceLines=10]
 * @returns {{ shouldReapprove: boolean, reason: string, trigger: string }}
 */
function evaluateIncrementalApproval({
  hasPriorApproval,
  approvedSha,
  currentSha,
  isAuthorOwner,
  deltaLines,
  isCleanRebase,
  maxGraceLines = MAX_OWNER_GRACE_LINES
}) {
  if (!hasPriorApproval || !approvedSha) {
    return {
      shouldReapprove: false,
      reason: 'No prior active approval found on this PR.',
      trigger: 'NO_PRIOR_APPROVAL'
    };
  }

  if (approvedSha === currentSha) {
    return {
      shouldReapprove: false,
      reason: 'PR HEAD is already approved at this commit.',
      trigger: 'ALREADY_APPROVED'
    };
  }

  if (isCleanRebase || deltaLines === 0) {
    return {
      shouldReapprove: true,
      reason: 'Clean rebase detected with zero semantic patch lines changed.',
      trigger: 'CLEAN_REBASE'
    };
  }

  if (isAuthorOwner && deltaLines <= maxGraceLines) {
    return {
      shouldReapprove: true,
      reason: `PR author is a code owner and changes since last approval (${deltaLines} line${deltaLines === 1 ? '' : 's'}) are within the ${maxGraceLines}-line grace delta.`,
      trigger: 'OWNER_GRACE_DELTA'
    };
  }

  if (isAuthorOwner) {
    return {
      shouldReapprove: false,
      reason: `Changes since last approval (${deltaLines} lines) exceed the ${maxGraceLines}-line grace delta for code owners. Manual review required.`,
      trigger: 'EXCEEDS_OWNER_GRACE'
    };
  }

  return {
    shouldReapprove: false,
    reason: `Non-owner author pushed ${deltaLines} changed line${deltaLines === 1 ? '' : 's'} since last approval. Manual review required.`,
    trigger: 'NON_OWNER_MODIFICATION'
  };
}

/**
 * Shell Helper: Executes git range-diff between approved commit and current commit relative to base.
 * @param {string} baseRef - Target base branch name.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @returns {string} Output of git range-diff.
 */
function runGitRangeDiff(baseRef, approvedSha, currentSha) {
  try {
    return execFileSync(
      'git',
      [
        'range-diff',
        `origin/${baseRef}...${approvedSha}`,
        `origin/${baseRef}...${currentSha}`
      ],
      { encoding: 'utf8', maxBuffer: 10 * 1024 * 1024 }
    );
  } catch (initialError) {
    try {
      execFileSync(
        'git',
        ['fetch', 'origin', baseRef, approvedSha, currentSha],
        { encoding: 'utf8', stdio: 'ignore' }
      );
      return execFileSync(
        'git',
        [
          'range-diff',
          `origin/${baseRef}...${approvedSha}`,
          `origin/${baseRef}...${currentSha}`
        ],
        { encoding: 'utf8', maxBuffer: 10 * 1024 * 1024 }
      );
    } catch (fallbackError) {
      throw new Error(`Failed to execute git range-diff: ${fallbackError.message}`);
    }
  }
}

/**
 * Orchestrator: Fetches PR reviews, calculates semantic delta, evaluates grace policy, and auto-reapproves.
 * @param {Object} params - Injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const pr = context.payload ? context.payload.pull_request : null;
  if (!pr) {
    core.setFailed('This workflow must run in the context of a pull request synchronize event.');
    return;
  }

  const { owner, repo } = context.repo;
  const prNumber = pr.number;
  const prAuthor = pr.user ? pr.user.login : '';
  const currentSha = pr.head.sha;
  const baseRef = pr.base.ref;

  core.info(`Evaluating incremental approval for PR #${prNumber} (Author: @${prAuthor}, Base: ${baseRef}, HEAD: ${currentSha.slice(0, 7)})`);

  const { data: reviews } = await github.rest.pulls.listReviews({
    owner,
    repo,
    pull_number: prNumber,
    per_page: 100
  });

  const latestApproval = findLatestApproval(reviews, prAuthor);
  if (!latestApproval) {
    core.info(`PR #${prNumber} does not have any prior active approvals. Skipping auto-reapproval.`);
    return;
  }

  core.info(`Found previous approval by @${latestApproval.approver} on commit ${latestApproval.approvedSha.slice(0, 7)}`);

  if (!fs.existsSync('OWNERS')) {
    core.setFailed('OWNERS file not found in repository root.');
    return;
  }

  const ownersList = parseReviewersFromOwners(fs.readFileSync('OWNERS', 'utf8'));
  const isAuthorOwner = isAuthorCodeOwner(prAuthor, ownersList);
  core.info(`PR Author @${prAuthor} is code owner: ${isAuthorOwner}`);

  let rangeDiffOutput = '';
  try {
    rangeDiffOutput = runGitRangeDiff(baseRef, latestApproval.approvedSha, currentSha);
  } catch (error) {
    core.warning(`Unable to compute git range-diff (${error.message}). Requiring manual review.`);
    return;
  }

  const { totalDelta, isCleanRebase } = parseRangeDiff(rangeDiffOutput);
  core.info(`Computed range-diff: isCleanRebase=${isCleanRebase}, totalDelta=${totalDelta} lines`);

  const decision = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: latestApproval.approvedSha,
    currentSha,
    isAuthorOwner,
    deltaLines: totalDelta,
    isCleanRebase
  });

  if (!decision.shouldReapprove) {
    core.info(`Decision: ${decision.reason}`);
    return;
  }

  core.info(`Decision: ${decision.reason} Auto-reapproving PR #${prNumber}...`);

  const reviewBody = `🤖 **Incremental PR Approval**\n\n${decision.reason}\n\n*Preserving prior approval by @${latestApproval.approver} on commit \`${latestApproval.approvedSha.slice(0, 7)}\`.*`;

  try {
    await github.rest.pulls.createReview({
      owner,
      repo,
      pull_number: prNumber,
      commit_id: currentSha,
      event: 'APPROVE',
      body: reviewBody
    });
    core.info(`Successfully auto-reapproved PR #${prNumber}`);
  } catch (error) {
    core.setFailed(`Failed to submit re-approval review for PR #${prNumber}: ${error.message}`);
  }
}

module.exports = {
  MAX_OWNER_GRACE_LINES,
  findLatestApproval,
  parseRangeDiff,
  evaluateIncrementalApproval,
  runGitRangeDiff,
  run
};
