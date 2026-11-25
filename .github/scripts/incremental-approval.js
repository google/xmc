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
const { parseReviewersFromOwners, isAuthorCodeOwner } = require('./assign-reviewers');
const { parseRangeDiff } = require('./range-diff-parser');
const {
  ensureGitRefs,
  runGitRangeDiff,
  computeAddedCommitsDelta
} = require('./git-range-diff');
const {
  checkProtectedFileChanges,
  checkAddedCommitsForProtectedFiles,
  evaluateIncrementalApproval,
  formatReapprovalReviewBody
} = require('./approval-policy');
const {
  findLatestApproval,
  fetchTimelineEvents
} = require('./pr-approval-timeline');

/**
 * Pure function: Formats a caught value for logging, tolerating non-Error throws (null, strings, objects).
 * Appends trimmed stderr when present (e.g. from git subprocess failures).
 * @param {*} error - Caught value.
 * @returns {string}
 */
function describeError(error) {
  const message = error?.message || String(error);
  const stderrInfo = error?.stderr ? ` | Stderr: ${String(error.stderr).trim()}` : '';
  return `${message}${stderrInfo}`;
}


function validatePullRequestContext(context, core) {
  const pr = context.payload ? context.payload.pull_request : null;
  if (!pr) {
    core.setFailed('This workflow must run in the context of a pull request synchronize event.');
    return null;
  }
  if (pr.draft) {
    core.info(`PR #${pr.number} is in draft mode. Skipping auto-reapproval.`);
    return null;
  }
  const currentSha = pr.head ? pr.head.sha : '';
  const baseRef = pr.base ? pr.base.ref : '';
  if (!currentSha || !baseRef) {
    core.setFailed('PR payload missing head SHA or base ref.');
    return null;
  }
  return {
    owner: context.repo.owner,
    repo: context.repo.repo,
    prNumber: pr.number,
    prAuthor: pr.user ? pr.user.login : '',
    currentSha,
    baseRef,
  };
}

async function fetchActiveApproval({ github, core, owner, repo, prNumber, prAuthor }) {
  let reviews = [];
  try {
    reviews = await github.paginate(github.rest.pulls.listReviews, { owner, repo, pull_number: prNumber, per_page: 100 });
  } catch (reviewErr) {
    core.setFailed(`Failed to fetch PR reviews: ${describeError(reviewErr)}`);
    return null;
  }

  const timelineEvents = await fetchTimelineEvents(github, owner, repo, prNumber, core);
  const latestApproval = findLatestApproval(reviews, prAuthor, timelineEvents);
  if (!latestApproval) {
    core.info(`PR #${prNumber} does not have any prior active approvals or has blocking change requests. Skipping auto-reapproval.`);
    return null;
  }
  core.info(`Found previous approval by @${latestApproval.approver} on commit ${latestApproval.approvedSha.slice(0, 7)}`);
  return latestApproval;
}

/**
 * Verifies that baseRef, approvedSha, and currentSha are reachable locally (fetching if missing).
 * @param {Object} params
 * @returns {boolean}
 */
function verifyGitRefs({ core, baseRef, approvedSha, currentSha, ensureRefs = ensureGitRefs }) {
  let refStatus;
  try {
    refStatus = ensureRefs(baseRef, approvedSha, currentSha);
  } catch (error) {
    core.warning(`Failed to verify git references (${describeError(error)}). Requiring manual review.`);
    return false;
  }
  if (!refStatus || !refStatus.success) {
    core.warning(`Previously approved commit ${approvedSha.slice(0, 7)} is no longer reachable on remote (likely pruned by Git GC after force-push). Requiring manual review.`);
    return false;
  }
  return true;
}

/**
 * Resolves whether OWNERS exists and whether prAuthor is listed as a code owner.
 * @param {Object} core
 * @param {string} prAuthor
 * @returns {boolean|null}
 */
function resolveAuthorOwnership(core, prAuthor) {
  if (!fs.existsSync('OWNERS')) {
    core.setFailed('OWNERS file not found in repository root.');
    return null;
  }
  const ownersList = parseReviewersFromOwners(fs.readFileSync('OWNERS', 'utf8'));
  const isAuthorOwner = isAuthorCodeOwner(prAuthor, ownersList);
  core.info(`PR Author @${prAuthor} is code owner: ${isAuthorOwner}`);
  return isAuthorOwner;
}

function detectBaseProtectedChanges({ core, prNumber, baseRef, approvedSha, currentSha }) {
  try {
    const hasProtectedFileChanges = checkProtectedFileChanges(baseRef, approvedSha, currentSha);
    if (hasProtectedFileChanges) {
      core.info(`PR #${prNumber} modifies sensitive CI workflows, scripts, ownership files, or the protobuf breaking-change baseline or its configuration (.github/, OWNERS, .githooks/, api/testdata/protos_baseline.binpb, api/buf.yaml, api/BUILD.bazel). Skipping auto-reapproval.`);
    }
    return hasProtectedFileChanges;
  } catch (error) {
    core.warning(`Unable to determine files changed by PR #${prNumber} (${describeError(error)}). Requiring manual review.`);
    return null;
  }
}

function computeRangeDiffAndAdded({ core, baseRef, approvedSha, currentSha }) {
  let rangeDiffOutput = '';
  try {
    rangeDiffOutput = runGitRangeDiff(baseRef, approvedSha, currentSha);
  } catch (error) {
    core.warning(`Unable to compute git range-diff (${describeError(error)}). Requiring manual review.`);
    return null;
  }

  const parsed = parseRangeDiff(rangeDiffOutput);
  let addedDelta;
  try {
    addedDelta = computeAddedCommitsDelta(parsed.addedCommitShas);
  } catch (error) {
    core.warning(`Unable to compute line delta for added commits (${describeError(error)}). Requiring manual review.`);
    return null;
  }
  return { ...parsed, addedDelta };
}

/**
 * Computes protected-file status and semantic range-diff line deltas between approvedSha and currentSha.
 * @param {Object} params
 * @param {Object} params.core - GitHub Actions core toolkit.
 * @param {number} params.prNumber - Pull request number.
 * @param {string} params.baseRef - Target base branch name.
 * @param {string} params.approvedSha - Previously approved commit SHA.
 * @param {string} params.currentSha - Current HEAD commit SHA.
 * @returns {{ totalDelta: number, testDelta: number, isCleanRebase: boolean, hasRemovedCommits: boolean, hasProtectedFileChanges: boolean } | null}
 */
function computeDeltaSummary({ core, prNumber, baseRef, approvedSha, currentSha }) {
  let hasProtectedFileChanges = detectBaseProtectedChanges({ core, prNumber, baseRef, approvedSha, currentSha });
  if (hasProtectedFileChanges === null) {
    return null;
  }

  const diffStats = computeRangeDiffAndAdded({ core, baseRef, approvedSha, currentSha });
  if (!diffStats) {
    return null;
  }

  const { totalDelta: hunkDelta, testDelta: hunkTestDelta, isCleanRebase, hasRemovedCommits, addedCommitShas, addedDelta } = diffStats;
  if (!hasProtectedFileChanges && checkAddedCommitsForProtectedFiles(addedCommitShas)) {
    hasProtectedFileChanges = true;
    core.info('Newly added commit modifies sensitive files. Manual review required.');
  }

  const totalDelta = hunkDelta + addedDelta.totalDelta;
  const testDelta = hunkTestDelta + addedDelta.testDelta;
  core.info(`Computed range-diff: isCleanRebase=${isCleanRebase}, hunkDelta=${hunkDelta}, addedCommitsDelta=${addedDelta.totalDelta} (commits: ${addedCommitShas.length}), totalDelta=${totalDelta} lines (test files: ${testDelta}), hasRemovedCommits=${hasRemovedCommits}, hasProtectedFileChanges=${hasProtectedFileChanges}`);
  return { totalDelta, testDelta, isCleanRebase, hasRemovedCommits, hasProtectedFileChanges };
}

async function submitReapprovalReview({ github, core, owner, repo, prNumber, currentSha, latestApproval, reason }) {
  try {
    await github.rest.pulls.createReview({
      owner,
      repo,
      pull_number: prNumber,
      commit_id: currentSha,
      event: 'APPROVE',
      body: formatReapprovalReviewBody(reason, latestApproval.approver, latestApproval.approvedSha)
    });
    core.info(`Successfully auto-reapproved PR #${prNumber}`);
  } catch (error) {
    core.setFailed(`Failed to submit re-approval review for PR #${prNumber}: ${describeError(error)}`);
  }
}

async function applyIncrementalDecision({ github, core, owner, repo, prNumber, currentSha, latestApproval, isAuthorOwner, deltaSummary }) {
  const decision = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: latestApproval.approvedSha,
    currentSha,
    isAuthorOwner,
    deltaLines: deltaSummary.totalDelta,
    testDeltaLines: deltaSummary.testDelta,
    isCleanRebase: deltaSummary.isCleanRebase,
    hasRemovedCommits: deltaSummary.hasRemovedCommits,
    hasProtectedFileChanges: deltaSummary.hasProtectedFileChanges,
    isDismissed: latestApproval.isDismissed
  });

  if (!decision.shouldReapprove) {
    core.info(`Decision: ${decision.reason}`);
    return;
  }

  core.info(`Decision: ${decision.reason} Auto-reapproving PR #${prNumber}...`);
  await submitReapprovalReview({ github, core, owner, repo, prNumber, currentSha, latestApproval, reason: decision.reason });
}

/**
 * Orchestrator: Fetches PR reviews, calculates semantic delta, evaluates grace policy, and auto-reapproves.
 * @param {Object} params - Injected by actions/github-script.
 */
async function run({ github, context, core }) {
  const prCtx = validatePullRequestContext(context, core);
  if (!prCtx) {
    return;
  }
  const { owner, repo, prNumber, prAuthor, currentSha, baseRef } = prCtx;
  core.info(`Evaluating incremental approval for PR #${prNumber} (Author: @${prAuthor}, Base: ${baseRef}, HEAD: ${currentSha.slice(0, 7)})`);

  const latestApproval = await fetchActiveApproval({ github, core, owner, repo, prNumber, prAuthor });
  if (!latestApproval) {
    return;
  }

  const approvedSha = latestApproval.approvedSha;
  if (!verifyGitRefs({ core, baseRef, approvedSha, currentSha })) {
    return;
  }

  const isAuthorOwner = resolveAuthorOwnership(core, prAuthor);
  if (isAuthorOwner === null) {
    return;
  }

  const deltaSummary = computeDeltaSummary({ core, prNumber, baseRef, approvedSha, currentSha });
  if (!deltaSummary) {
    return;
  }
  await applyIncrementalDecision({ github, core, owner, repo, prNumber, currentSha, latestApproval, isAuthorOwner, deltaSummary });
}

module.exports = {
  describeError,
  validatePullRequestContext,
  verifyGitRefs,
  resolveAuthorOwnership,
  computeDeltaSummary,
  applyIncrementalDecision,
  run
};
