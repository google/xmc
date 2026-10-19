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
const { isAuthorCodeOwner } = require('./auto-approve-absence');
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

  if (pr.draft) {
    core.info(`PR #${pr.number} is in draft mode. Skipping auto-reapproval.`);
    return;
  }

  const { owner, repo } = context.repo;
  const prNumber = pr.number;
  const prAuthor = pr.user ? pr.user.login : '';
  const currentSha = pr.head ? pr.head.sha : '';
  const baseRef = pr.base ? pr.base.ref : '';

  if (!currentSha || !baseRef) {
    core.setFailed('PR payload missing head SHA or base ref.');
    return;
  }

  core.info(`Evaluating incremental approval for PR #${prNumber} (Author: @${prAuthor}, Base: ${baseRef}, HEAD: ${currentSha.slice(0, 7)})`);

  let reviews = [];
  try {
    reviews = await github.paginate(github.rest.pulls.listReviews, {
      owner,
      repo,
      pull_number: prNumber,
      per_page: 100
    });
  } catch (reviewErr) {
    core.setFailed(`Failed to fetch PR reviews: ${describeError(reviewErr)}`);
    return;
  }

  const timelineEvents = await fetchTimelineEvents(github, owner, repo, prNumber, core);
  const latestApproval = findLatestApproval(reviews, prAuthor, timelineEvents);
  if (!latestApproval) {
    core.info(`PR #${prNumber} does not have any prior active approvals or has blocking change requests. Skipping auto-reapproval.`);
    return;
  }

  core.info(`Found previous approval by @${latestApproval.approver} on commit ${latestApproval.approvedSha.slice(0, 7)}`);

  let refStatus;
  try {
    refStatus = ensureGitRefs(baseRef, latestApproval.approvedSha, currentSha);
  } catch (error) {
    core.warning(`Failed to verify git references (${describeError(error)}). Requiring manual review.`);
    return;
  }
  if (!refStatus.success) {
    core.warning(`Previously approved commit ${latestApproval.approvedSha.slice(0, 7)} is no longer reachable on remote (likely pruned by Git GC after force-push). Requiring manual review.`);
    return;
  }

  if (!fs.existsSync('OWNERS')) {
    core.setFailed('OWNERS file not found in repository root.');
    return;
  }

  const ownersList = parseReviewersFromOwners(fs.readFileSync('OWNERS', 'utf8'));
  const isAuthorOwner = isAuthorCodeOwner(prAuthor, ownersList);
  core.info(`PR Author @${prAuthor} is code owner: ${isAuthorOwner}`);

  let hasProtectedFileChanges;
  try {
    hasProtectedFileChanges = checkProtectedFileChanges(baseRef, latestApproval.approvedSha, currentSha);
  } catch (error) {
    core.warning(`Unable to determine files changed by PR #${prNumber} (${describeError(error)}). Requiring manual review.`);
    return;
  }
  if (hasProtectedFileChanges) {
    core.info(`PR #${prNumber} modifies sensitive CI workflows, scripts, ownership files, or the protobuf breaking-change baseline or its configuration (.github/, OWNERS, .githooks/, api/testdata/protos_baseline.binpb, api/buf.yaml, api/BUILD.bazel). Skipping auto-reapproval.`);
  }

  let rangeDiffOutput = '';
  try {
    rangeDiffOutput = runGitRangeDiff(baseRef, latestApproval.approvedSha, currentSha);
  } catch (error) {
    core.warning(`Unable to compute git range-diff (${describeError(error)}). Requiring manual review.`);
    return;
  }

  const {
    totalDelta: hunkDelta,
    testDelta: hunkTestDelta,
    isCleanRebase,
    hasRemovedCommits,
    addedCommitShas
  } = parseRangeDiff(rangeDiffOutput);

  if (!hasProtectedFileChanges && checkAddedCommitsForProtectedFiles(addedCommitShas)) {
    hasProtectedFileChanges = true;
    core.info(`Newly added commit modifies sensitive files. Manual review required.`);
  }

  let addedDelta;
  try {
    addedDelta = computeAddedCommitsDelta(addedCommitShas);
  } catch (error) {
    core.warning(`Unable to compute line delta for added commits (${describeError(error)}). Requiring manual review.`);
    return;
  }
  const totalDelta = hunkDelta + addedDelta.totalDelta;
  const testDelta = hunkTestDelta + addedDelta.testDelta;
  core.info(`Computed range-diff: isCleanRebase=${isCleanRebase}, hunkDelta=${hunkDelta}, addedCommitsDelta=${addedDelta.totalDelta} (commits: ${addedCommitShas.length}), totalDelta=${totalDelta} lines (test files: ${testDelta}), hasRemovedCommits=${hasRemovedCommits}, hasProtectedFileChanges=${hasProtectedFileChanges}`);

  const decision = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: latestApproval.approvedSha,
    currentSha,
    isAuthorOwner,
    deltaLines: totalDelta,
    testDeltaLines: testDelta,
    isCleanRebase,
    hasRemovedCommits,
    hasProtectedFileChanges,
    isDismissed: latestApproval.isDismissed
  });

  if (!decision.shouldReapprove) {
    core.info(`Decision: ${decision.reason}`);
    return;
  }

  core.info(`Decision: ${decision.reason} Auto-reapproving PR #${prNumber}...`);

  try {
    await github.rest.pulls.createReview({
      owner,
      repo,
      pull_number: prNumber,
      commit_id: currentSha,
      event: 'APPROVE',
      body: formatReapprovalReviewBody(decision.reason, latestApproval.approver, latestApproval.approvedSha)
    });
    core.info(`Successfully auto-reapproved PR #${prNumber}`);
  } catch (error) {
    core.setFailed(`Failed to submit re-approval review for PR #${prNumber}: ${describeError(error)}`);
  }
}

module.exports = {
  describeError,
  run
};
