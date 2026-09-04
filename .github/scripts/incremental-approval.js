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
const { parseRangeDiff } = require('./range-diff-parser');
const {
  runGitRangeDiff,
  computeAddedCommitsDelta
} = require('./git-range-diff');
const {
  checkProtectedFileChanges,
  evaluateIncrementalApproval,
  formatReapprovalReviewBody
} = require('./approval-policy');



/**
 * Pure function: Checks whether a username belongs to an automated bot.
 * @param {string} username - GitHub username.
 * @returns {boolean} True if the user is a bot.
 */
function isBotUser(username) {
  if (!username || typeof username !== 'string') {
    return false;
  }
  const lower = username.toLowerCase();
  return lower.endsWith('[bot]') || lower === 'github-actions';
}

/**
 * Pure function: Extracts manual dismissal IDs and pre-dismissal review states from timeline events.
 * @param {Array<Object>} sortedEvents - Chronologically sorted timeline events.
 * @returns {{ manualDismissalIds: Set<string|number>, dismissedReviewPreviousStates: Map<string|number, string> }}
 */
function extractDismissalContext(sortedEvents) {
  const dismissedReviewPreviousStates = new Map();
  const manualDismissalIds = new Set();

  for (const event of sortedEvents) {
    const isRestDismissal = event.event === 'dismissed_review' || event.event === 'review_dismissed';
    const isGraphqlDismissal = event.__typename === 'ReviewDismissedEvent';

    if (!isRestDismissal && !isGraphqlDismissal && !event.previousReviewState) {
      continue;
    }

    const reviewId =
      (event.review && (event.review.databaseId || event.review.id)) ||
      (event.dismissed_review && event.dismissed_review.review_id) ||
      event.review_id;

    if (!reviewId) {
      continue;
    }

    const reviewIdStr = String(reviewId);
    const prevState =
      event.previousReviewState ||
      (event.dismissed_review && event.dismissed_review.state);
    if (prevState) {
      const upperState = String(prevState).toUpperCase();
      dismissedReviewPreviousStates.set(reviewId, upperState);
      dismissedReviewPreviousStates.set(reviewIdStr, upperState);
    }

    // Automatic stale push dismissals reference the new commit that triggered the dismissal
    // (dismissal_commit_id in REST, or pullRequestCommit/dismissalCommit in GraphQL)
    // and have no human dismissal message. Manual dismissals have no trigger commit or have a dismissal message.
    const hasTriggerCommit = Boolean(
      event.dismissal_commit_id ||
      (event.dismissed_review && event.dismissed_review.dismissal_commit_id) ||
      event.pullRequestCommit ||
      event.dismissalCommit
    );

    const hasDismissalMessage = Boolean(
      event.dismissalMessage ||
      event.dismissal_message ||
      (event.dismissed_review && event.dismissed_review.dismissal_message)
    );

    const isManual = !hasTriggerCommit || hasDismissalMessage;
    if (isManual) {
      manualDismissalIds.add(reviewId);
      manualDismissalIds.add(reviewIdStr);
    }
  }

  return { manualDismissalIds, dismissedReviewPreviousStates };
}

/**
 * Pure function: Extracts and normalizes a review record from an individual timeline event.
 * @param {Object} event - Individual timeline event from REST or GraphQL.
 * @param {Object} context - Metadata context for filtering and state resolution.
 * @param {string} context.authorLower - Lowercased PR author username.
 * @param {Set<string|number>} context.manualDismissalIds - Manually dismissed review IDs.
 * @param {Map<string|number, string>} context.dismissedReviewPreviousStates - Pre-dismissal state map.
 * @returns {{ approver: string, state: string, approvedSha: string, submittedAt: string, isDismissed?: boolean } | null}
 */
function parseTimelineReviewEvent(event, { authorLower, manualDismissalIds, dismissedReviewPreviousStates }) {
  const isReview = event.event === 'reviewed' || event.__typename === 'PullRequestReview';
  if (!isReview) {
    return null;
  }

  const reviewer = (event.user && event.user.login) || (event.author && event.author.login);
  const commitSha = event.commit_id || (event.commit && event.commit.oid);
  if (!reviewer || !commitSha || reviewer.toLowerCase() === authorLower || isBotUser(reviewer)) {
    return null;
  }

  let state = (event.state || '').toUpperCase();
  if (state === 'COMMENTED') {
    return null;
  }

  const reviewId = event.databaseId || event.id || event.review_id;
  const reviewIdStr = reviewId ? String(reviewId) : '';
  const isManuallyDismissed = Boolean(
    reviewId && (manualDismissalIds.has(reviewId) || manualDismissalIds.has(reviewIdStr))
  );

  if (isManuallyDismissed) {
    return {
      approver: reviewer,
      state: 'DISMISSED',
      isDismissed: true,
      approvedSha: commitSha,
      submittedAt: event.submitted_at || event.submittedAt || event.created_at || event.createdAt
    };
  }

  let isDismissed = false;
  if (state === 'DISMISSED') {
    const prevState = reviewId
      ? (dismissedReviewPreviousStates.get(reviewId) || dismissedReviewPreviousStates.get(reviewIdStr))
      : null;
    if (prevState === 'APPROVED') {
      state = 'APPROVED';
      isDismissed = true;
    } else if (prevState === 'CHANGES_REQUESTED') {
      state = 'CHANGES_REQUESTED';
      isDismissed = true;
    } else {
      return null;
    }
  }

  return {
    approver: reviewer,
    state,
    isDismissed,
    approvedSha: commitSha,
    submittedAt: event.submitted_at || event.submittedAt || event.created_at || event.createdAt
  };
}

/**
 * Pure function: Selects the latest active approval from a map of normalized reviewer states.
 * Returns null if any reviewer has an active CHANGES_REQUESTED or no approvals exist.
 * @param {Map<string, Object>} latestByReviewer - Map of reviewer username to latest review state.
 * @returns {{ approver: string, approvedSha: string, submittedAt: string, isDismissed: boolean } | null}
 */
function resolveActiveApproval(latestByReviewer) {
  const activeReviews = Array.from(latestByReviewer.values());
  if (activeReviews.some(r => r.state === 'CHANGES_REQUESTED')) {
    return null;
  }

  const approvals = activeReviews.filter(r => r.state === 'APPROVED' && r.approvedSha);
  if (approvals.length === 0) {
    return null;
  }

  approvals.sort((a, b) => new Date(b.submittedAt || 0) - new Date(a.submittedAt || 0));
  return {
    approver: approvals[0].approver,
    approvedSha: approvals[0].approvedSha,
    submittedAt: approvals[0].submittedAt,
    isDismissed: !!approvals[0].isDismissed
  };
}

/**
 * Pure function: Reconstructs latest active approval from timeline events.
 * Honors dismissed reviews and checks for active blocking CHANGES_REQUESTED.
 * @param {Array<Object>} timelineEvents - List of timeline events from GitHub API.
 * @param {string} [prAuthor=''] - Username of the PR author.
 * @returns {{ approver: string, approvedSha: string, submittedAt: string } | null}
 */
function findLatestApprovalFromTimeline(timelineEvents, prAuthor = '') {
  if (!Array.isArray(timelineEvents) || timelineEvents.length === 0) {
    return null;
  }

  const authorLower = (prAuthor || '').toLowerCase();
  const sortedEvents = [...timelineEvents].sort(
    (a, b) => new Date(a.submitted_at || a.created_at || a.submittedAt || a.createdAt || 0) -
              new Date(b.submitted_at || b.created_at || b.submittedAt || b.createdAt || 0)
  );

  const dismissalContext = extractDismissalContext(sortedEvents);
  const reviewContext = { authorLower, ...dismissalContext };
  const latestByReviewer = new Map();

  for (const event of sortedEvents) {
    const reviewRecord = parseTimelineReviewEvent(event, reviewContext);
    if (reviewRecord) {
      latestByReviewer.set(reviewRecord.approver.toLowerCase(), reviewRecord);
    }
  }

  return resolveActiveApproval(latestByReviewer);
}

/**
 * Pure function: Finds latest active approval from PR reviews list.
 * Excludes bot reviews, self-approvals, and checks for active CHANGES_REQUESTED.
 * @param {Array<Object>} reviews - List of review objects from GitHub API.
 * @param {string} [prAuthor=''] - Username of the PR author.
 * @returns {{ approver: string, approvedSha: string, submittedAt: string } | null}
 */
function findLatestApprovalFromReviews(reviews, prAuthor = '') {
  if (!Array.isArray(reviews) || reviews.length === 0) {
    return null;
  }

  const authorLower = (prAuthor || '').toLowerCase();
  const sortedReviews = [...reviews].sort(
    (a, b) => new Date(a.submitted_at || a.created_at || 0) - new Date(b.submitted_at || b.created_at || 0)
  );

  const latestByReviewer = new Map();
  for (const review of sortedReviews) {
    if (!review.user || !review.commit_id) {
      continue;
    }
    const reviewer = review.user.login;
    if (!reviewer || reviewer.toLowerCase() === authorLower || isBotUser(reviewer)) {
      continue;
    }
    const state = (review.state || '').toUpperCase();
    if (state === 'COMMENTED' || state === 'DISMISSED') {
      continue;
    }
    latestByReviewer.set(reviewer.toLowerCase(), {
      approver: reviewer,
      state,
      isDismissed: false,
      approvedSha: review.commit_id,
      submittedAt: review.submitted_at || review.created_at
    });
  }

  return resolveActiveApproval(latestByReviewer);
}

/**
 * Pure function: Dispatches approval lookup to timeline reconstruction with reviews fallback.
 * @param {Array<Object>} reviews - List of review objects.
 * @param {string} [prAuthor=''] - Username of the PR author.
 * @param {Array<Object>} [timelineEvents=[]] - List of timeline events.
 * @returns {{ approver: string, approvedSha: string, submittedAt: string } | null}
 */
function findLatestApproval(reviews, prAuthor = '', timelineEvents = []) {
  if (Array.isArray(timelineEvents) && timelineEvents.length > 0) {
    return findLatestApprovalFromTimeline(timelineEvents, prAuthor);
  }
  return findLatestApprovalFromReviews(reviews, prAuthor);
}






const GRAPHQL_TIMELINE_QUERY = `query($owner: String!, $repo: String!, $prNumber: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $prNumber) {
      timelineItems(first: 100, itemTypes: [PULL_REQUEST_REVIEW, REVIEW_DISMISSED_EVENT]) {
        nodes {
          __typename
          ... on PullRequestReview {
            databaseId
            author { login }
            state
            commit { oid }
            submittedAt
          }
          ... on ReviewDismissedEvent {
            previousReviewState
            dismissalMessage
            pullRequestCommit {
              commit {
                oid
              }
            }
            review {
              databaseId
            }
          }
        }
      }
    }
  }
}`;

/**
 * GitHub API Helper: Fetches timeline items via GraphQL, falling back to REST timeline API.
 * @param {Object} github - Octokit instance.
 * @param {string} owner - Repository owner.
 * @param {string} repo - Repository name.
 * @param {number} prNumber - Pull request number.
 * @param {Object} core - Actions core logger.
 * @returns {Promise<Array<Object>>}
 */
async function fetchTimelineEvents(github, owner, repo, prNumber, core) {
  try {
    const graphqlResponse = await github.graphql(GRAPHQL_TIMELINE_QUERY, { owner, repo, prNumber });
    return graphqlResponse?.repository?.pullRequest?.timelineItems?.nodes || [];
  } catch (graphqlErr) {
    try {
      return await github.paginate(github.rest.issues.listEventsForTimeline, {
        owner,
        repo,
        issue_number: prNumber,
        per_page: 100
      });
    } catch (timelineError) {
      core.info(`Timeline API not available (${timelineError.message}), falling back to reviews list.`);
      return [];
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
    core.setFailed(`Failed to fetch PR reviews: ${reviewErr.message}`);
    return;
  }

  const timelineEvents = await fetchTimelineEvents(github, owner, repo, prNumber, core);
  const latestApproval = findLatestApproval(reviews, prAuthor, timelineEvents);
  if (!latestApproval) {
    core.info(`PR #${prNumber} does not have any prior active approvals or has blocking change requests. Skipping auto-reapproval.`);
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

  const hasProtectedFileChanges = checkProtectedFileChanges(latestApproval.approvedSha, currentSha);
  if (hasProtectedFileChanges) {
    core.info(`PR #${prNumber} modifies sensitive CI workflows, scripts, or ownership files (.github/, OWNERS). Skipping auto-reapproval.`);
  }

  let rangeDiffOutput = '';
  try {
    rangeDiffOutput = runGitRangeDiff(baseRef, latestApproval.approvedSha, currentSha);
  } catch (error) {
    core.warning(`Unable to compute git range-diff (${error.message}). Requiring manual review.`);
    return;
  }

  const {
    totalDelta: hunkDelta,
    isCleanRebase,
    hasRemovedCommits,
    addedCommitShas
  } = parseRangeDiff(rangeDiffOutput);

  const addedDelta = computeAddedCommitsDelta(addedCommitShas);
  const totalDelta = hunkDelta + addedDelta.totalDelta;
  core.info(`Computed range-diff: isCleanRebase=${isCleanRebase}, hunkDelta=${hunkDelta}, addedCommitsDelta=${addedDelta.totalDelta} (commits: ${addedCommitShas.length}), totalDelta=${totalDelta} lines, hasRemovedCommits=${hasRemovedCommits}, hasProtectedFileChanges=${hasProtectedFileChanges}`);

  const decision = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: latestApproval.approvedSha,
    currentSha,
    isAuthorOwner,
    deltaLines: totalDelta,
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
    core.setFailed(`Failed to submit re-approval review for PR #${prNumber}: ${error.message}`);
  }
}

module.exports = {
  isBotUser,
  extractDismissalContext,
  parseTimelineReviewEvent,
  resolveActiveApproval,
  findLatestApprovalFromTimeline,
  findLatestApprovalFromReviews,
  findLatestApproval,
  GRAPHQL_TIMELINE_QUERY,
  fetchTimelineEvents,
  run
};
