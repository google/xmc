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
 * Pure function: Checks whether a user is a known bot user.
 * @param {string} username - GitHub login name.
 * @returns {boolean} True if user is a bot.
 */
function isBotUser(username) {
  if (!username) {
    return false;
  }
  const lower = username.toLowerCase();
  return lower.endsWith('[bot]') || lower === 'github-actions';
}

/**
 * Pure function: Extracts dismissed review context from timeline events.
 * Distinguishes manual dismissals by human reviewers from automatic stale-push dismissals.
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

const GRAPHQL_TIMELINE_QUERY = `
  query($owner: String!, $repo: String!, $prNumber: Int!, $cursor: String) {
    repository(owner: $owner, name: $repo) {
      pullRequest(number: $prNumber) {
        timelineItems(first: 100, after: $cursor, itemTypes: [PULL_REQUEST_REVIEW, REVIEW_DISMISSED_EVENT]) {
          pageInfo {
            hasNextPage
            endCursor
          }
          nodes {
            __typename
            ... on PullRequestReview {
              databaseId
              author {
                login
              }
              state
              commit {
                oid
              }
              submittedAt
            }
            ... on ReviewDismissedEvent {
              createdAt
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
  }
`;

/**
 * Fetches timeline items via GraphQL API with fallback to REST issues.listEventsForTimeline.
 * @param {Object} github - Octokit instance.
 * @param {string} owner - Repository owner.
 * @param {string} repo - Repository name.
 * @param {number} prNumber - Pull request number.
 * @param {Object} core - GitHub Actions core toolkit.
 * @returns {Promise<Array<Object>>}
 */
async function fetchTimelineEvents(github, owner, repo, prNumber, core) {
  try {
    let hasNextPage = true;
    let cursor = null;
    const allNodes = [];

    // TODO: Cap pagination loop to MAX_PAGES to prevent unbounded API consumption in CI workflows (addressed in stacked PR #754).
    while (hasNextPage) {
      const response = await github.graphql(GRAPHQL_TIMELINE_QUERY, {
        owner,
        repo,
        prNumber,
        cursor
      });

      const timelineItems = response?.repository?.pullRequest?.timelineItems;
      if (!timelineItems) {
        break;
      }

      if (Array.isArray(timelineItems.nodes)) {
        allNodes.push(...timelineItems.nodes);
      }

      hasNextPage = timelineItems.pageInfo?.hasNextPage;
      cursor = timelineItems.pageInfo?.endCursor;
    }

    if (allNodes.length > 0) {
      return allNodes;
    }
  } catch (error) {
    core.info(`[incremental-approval] GraphQL timeline query failed (${error.message}). Falling back to REST timeline.`);
  }

  try {
    return await github.paginate(github.rest.issues.listEventsForTimeline, {
      owner,
      repo,
      issue_number: prNumber,
      per_page: 100
    });
  } catch (restError) {
    core.info(`[incremental-approval] REST timeline fetch failed: ${restError.message}`);
    return [];
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
  fetchTimelineEvents
};
