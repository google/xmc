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

const test = require('node:test');
const assert = require('node:assert');
const {
  isBotUser,
  extractDismissalContext,
  parseTimelineReviewEvent,
  resolveActiveApproval,
  findLatestApprovalFromTimeline,
  findLatestApprovalFromReviews,
  findLatestApproval,
  GRAPHQL_TIMELINE_QUERY,
  MAX_TIMELINE_PAGES,
  fetchGraphQLTimeline,
  fetchRestTimeline,
  fetchTimelineEvents
} = require('./pr-approval-timeline');

test('isBotUser identifies bot usernames correctly', () => {
  assert.strictEqual(isBotUser('github-actions[bot]'), true);
  assert.strictEqual(isBotUser('dependabot[bot]'), true);
  assert.strictEqual(isBotUser('github-actions'), true);
  assert.strictEqual(isBotUser('alice'), false);
  assert.strictEqual(isBotUser(''), false);
  assert.strictEqual(isBotUser(null), false);
});

test('extractDismissalContext extracts manual dismissals and GraphQL previousReviewState', () => {
  const events = [
    // Manual dismissal via REST (no trigger commit, dismissal message present)
    { event: 'dismissed_review', review_id: 101, dismissal_message: 'Security issue' },
    // Automatic stale push dismissal via GraphQL (trigger commit present, no message)
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      pullRequestCommit: { commit: { oid: 'sha-stale' } },
      review: { databaseId: 202 }
    },
    // Automatic stale push dismissal of changes requested via GraphQL
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'CHANGES_REQUESTED',
      pullRequestCommit: { commit: { oid: 'sha-stale' } },
      review: { databaseId: 303 }
    },
    // Automatic stale push dismissal via REST (dismissal_commit_id present)
    {
      event: 'review_dismissed',
      dismissed_review: {
        review_id: 404,
        state: 'approved',
        dismissal_commit_id: 'sha-rest-commit'
      }
    },
    // Manual dismissal via GraphQL (no trigger commit, dismissal message present)
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: 'Rejected by security auditor',
      review: { databaseId: 505 }
    }
  ];
  const ctx = extractDismissalContext(events);
  assert.strictEqual(ctx.manualDismissalIds.has(101), true);
  assert.strictEqual(ctx.manualDismissalIds.has(202), false);
  assert.strictEqual(ctx.manualDismissalIds.has(303), false);
  assert.strictEqual(ctx.manualDismissalIds.has(404), false);
  assert.strictEqual(ctx.manualDismissalIds.has(505), true);
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(202), 'APPROVED');
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(303), 'CHANGES_REQUESTED');
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(404), 'APPROVED');
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(505), 'APPROVED');
});

test('parseTimelineReviewEvent parses valid review and respects dismissal context', () => {
  const context = {
    authorLower: 'author1',
    manualDismissalIds: new Set([101]),
    dismissedReviewPreviousStates: new Map([[202, 'APPROVED'], [303, 'CHANGES_REQUESTED']])
  };

  // Valid review
  const valid = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 100,
    user: { login: 'reviewer1' },
    state: 'approved',
    commit_id: 'sha1'
  }, context);
  assert.deepStrictEqual(valid, {
    approver: 'reviewer1',
    state: 'APPROVED',
    isDismissed: false,
    approvedSha: 'sha1',
    submittedAt: undefined
  });

  // Manually dismissed review
  const manualDismissed = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 101,
    user: { login: 'reviewer1' },
    state: 'approved',
    commit_id: 'sha1'
  }, context);
  assert.strictEqual(manualDismissed.state, 'DISMISSED');
  assert.strictEqual(manualDismissed.isDismissed, true);

  // Stale dismissed with APPROVED previous state
  const staleApproved = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 202,
    user: { login: 'reviewer1' },
    state: 'dismissed',
    commit_id: 'sha1'
  }, context);
  assert.strictEqual(staleApproved.state, 'APPROVED');
  assert.strictEqual(staleApproved.isDismissed, true);

  // Stale dismissed with CHANGES_REQUESTED previous state
  const staleChanges = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 303,
    user: { login: 'reviewer1' },
    state: 'dismissed',
    commit_id: 'sha1'
  }, context);
  assert.strictEqual(staleChanges.state, 'CHANGES_REQUESTED');
  assert.strictEqual(staleChanges.isDismissed, true);

  // Ignored bot review and PR author self-review
  assert.strictEqual(parseTimelineReviewEvent({
    event: 'reviewed',
    user: { login: 'bot[bot]' },
    state: 'approved',
    commit_id: 'sha1'
  }, context), null);
  assert.strictEqual(parseTimelineReviewEvent({
    event: 'reviewed',
    user: { login: 'author1' },
    state: 'approved',
    commit_id: 'sha1'
  }, context), null);
});

test('resolveActiveApproval returns latest approval and blocks on CHANGES_REQUESTED', () => {
  const reviewerMap = new Map();
  reviewerMap.set('alice', { approver: 'alice', state: 'APPROVED', approvedSha: 'sha1', submittedAt: '2026-08-20T10:00:00Z' });
  reviewerMap.set('bob', { approver: 'bob', state: 'APPROVED', approvedSha: 'sha2', submittedAt: '2026-08-21T10:00:00Z' });

  const latest = resolveActiveApproval(reviewerMap);
  assert.strictEqual(latest.approver, 'bob');
  assert.strictEqual(latest.approvedSha, 'sha2');

  // Blocking change request from another reviewer
  reviewerMap.set('charlie', { approver: 'charlie', state: 'CHANGES_REQUESTED', approvedSha: 'sha3', submittedAt: '2026-08-22T10:00:00Z' });
  assert.strictEqual(resolveActiveApproval(reviewerMap), null);

  // Empty map returns null
  assert.strictEqual(resolveActiveApproval(new Map()), null);
});

test('findLatestApproval ignores approvals from bot users', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'github-actions[bot]' },
      state: 'APPROVED',
      commit_id: 'sha-bot-approved',
      submitted_at: '2026-08-25T12:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'author');
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores approvals when another reviewer has active CHANGES_REQUESTED', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'APPROVED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      id: 2,
      user: { login: 'bob' },
      state: 'CHANGES_REQUESTED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T11:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores COMMENTED reviews from the same reviewer', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'APPROVED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      id: 2,
      user: { login: 'alice' },
      state: 'COMMENTED',
      commit_id: 'sha-222222',
      submitted_at: '2026-08-25T12:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
  assert.ok(result);
  assert.strictEqual(result.approver, 'alice');
  assert.strictEqual(result.approvedSha, 'sha-111111');
});

test('findLatestApproval rejects DISMISSED reviews when no timeline context is provided', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
  assert.strictEqual(result, null);
});

test('findLatestApproval finds prior approval from timeline when state is dismissed by stale push policy with APPROVED previous state', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: null,
      pullRequestCommit: { commit: { oid: 'sha-222222' } },
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.ok(result);
  assert.strictEqual(result.approver, 'alice');
  assert.strictEqual(result.approvedSha, 'sha-111111');
  assert.strictEqual(result.isDismissed, true);
});

test('findLatestApproval blocks auto-approval when dismissed review had CHANGES_REQUESTED previous state', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'CHANGES_REQUESTED',
      dismissalMessage: null,
      pullRequestCommit: { commit: { oid: 'sha-222222' } },
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval recovers prior approval when dismissed due to merge-base changed (GraphQL)', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: 'The merge-base changed after approval.',
      pullRequestCommit: null,
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.ok(result);
  assert.strictEqual(result.approver, 'alice');
  assert.strictEqual(result.approvedSha, 'sha-111111');
  assert.strictEqual(result.isDismissed, true);
});

test('findLatestApproval recovers prior approval when dismissed due to merge-base changed (REST)', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      event: 'reviewed',
      user: { login: 'alice' },
      state: 'dismissed',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      event: 'review_dismissed',
      dismissed_review: {
        review_id: 1,
        state: 'approved',
        dismissal_message: 'The merge-base changed after approval.'
      }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.ok(result);
  assert.strictEqual(result.approver, 'alice');
  assert.strictEqual(result.approvedSha, 'sha-111111');
  assert.strictEqual(result.isDismissed, true);
});

test('findLatestApproval ignores manually dismissed reviews in timeline (REST)', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      event: 'reviewed',
      user: { login: 'alice' },
      state: 'dismissed',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      event: 'review_dismissed',
      dismissed_review: {
        review_id: 1,
        state: 'approved',
        dismissal_message: 'Manual dismissal by user'
      }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores manually dismissed reviews in timeline (GraphQL)', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: 'Manually dismissed via UI',
      pullRequestCommit: null,
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores manual dismissals containing merge-base text in custom message', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: 'The merge-base changed, please re-review before merging.',
      pullRequestCommit: null,
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores manual dismissals without dismissal message when trigger commit is absent', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const timelineEvents = [
    {
      id: 1,
      __typename: 'PullRequestReview',
      databaseId: 1,
      author: { login: 'alice' },
      state: 'DISMISSED',
      commit: { oid: 'sha-111111' },
      submittedAt: '2026-08-25T10:00:00Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: null,
      pullRequestCommit: null,
      review: { databaseId: 1 }
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval ensures later manual dismissal overrides earlier stale approval by same reviewer', () => {
  const reviews = [
    { id: 101, user: { login: 'alice' }, state: 'DISMISSED', commit_id: 'sha1', submitted_at: '2026-08-25T10:00:00Z' },
    { id: 102, user: { login: 'alice' }, state: 'DISMISSED', commit_id: 'sha2', submitted_at: '2026-08-25T12:00:00Z' }
  ];
  const timelineEvents = [
    { id: 101, __typename: 'PullRequestReview', databaseId: 101, author: { login: 'alice' }, state: 'DISMISSED', commit: { oid: 'sha1' }, submittedAt: '2026-08-25T10:00:00Z' },
    { __typename: 'ReviewDismissedEvent', previousReviewState: 'APPROVED', pullRequestCommit: { commit: { oid: 'sha2' } }, review: { databaseId: 101 }, createdAt: '2026-08-25T11:00:00Z' },
    { id: 102, __typename: 'PullRequestReview', databaseId: 102, author: { login: 'alice' }, state: 'APPROVED', commit: { oid: 'sha2' }, submittedAt: '2026-08-25T12:00:00Z' },
    { __typename: 'ReviewDismissedEvent', previousReviewState: 'APPROVED', dismissalMessage: 'Security revocation', pullRequestCommit: null, review: { databaseId: 102 }, createdAt: '2026-08-25T13:00:00Z' }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
});

test('GRAPHQL_TIMELINE_QUERY queries pullRequestCommit and dismissalMessage', () => {
  assert.match(GRAPHQL_TIMELINE_QUERY, /pullRequestCommit/);
  assert.match(GRAPHQL_TIMELINE_QUERY, /dismissalMessage/);
});

test('findLatestApproval ignores self-approval from PR author', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'charlie' },
      state: 'APPROVED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
  assert.strictEqual(result, null);
});

test('MAX_TIMELINE_PAGES defaults to 10', () => {
  assert.strictEqual(MAX_TIMELINE_PAGES, 10);
});

test('fetchTimelineEvents caps GraphQL pagination to maxPages', async () => {
  let callCount = 0;
  const mockGithub = {
    graphql: async () => {
      callCount++;
      return {
        repository: {
          pullRequest: {
            timelineItems: {
              nodes: [{ id: callCount, __typename: 'PullRequestReview' }],
              pageInfo: {
                hasNextPage: true,
                endCursor: `cursor-${callCount}`
              }
            }
          }
        }
      };
    }
  };

  const infoMessages = [];
  const mockCore = {
    info: (msg) => infoMessages.push(msg)
  };

  const nodes = await fetchTimelineEvents(mockGithub, 'google', 'xmc', 123, mockCore, 3);
  assert.strictEqual(callCount, 3);
  assert.strictEqual(nodes.length, 3);
  assert.ok(infoMessages.some(m => m.includes('Reached maximum timeline pages limit (3)')));
});

test('fetchTimelineEvents falls back to REST when GraphQL throws', async () => {
  const mockGithub = {
    graphql: async () => {
      throw new Error('GraphQL 500 error');
    },
    paginate: async (endpoint, params) => {
      assert.strictEqual(params.issue_number, 123);
      return [{ id: 999, event: 'reviewed' }];
    },
    rest: {
      issues: {
        listEventsForTimeline: 'dummy-fn'
      }
    }
  };

  const infoMessages = [];
  const mockCore = {
    info: (msg) => infoMessages.push(msg)
  };

  const nodes = await fetchTimelineEvents(mockGithub, 'google', 'xmc', 123, mockCore);
  assert.strictEqual(nodes.length, 1);
  assert.strictEqual(nodes[0].id, 999);
  assert.ok(infoMessages.some(m => m.includes('GraphQL timeline query failed')));
});

test('fetchTimelineEvents returns empty array when REST fallback also throws', async () => {
  const mockGithub = {
    graphql: async () => {
      throw new Error('GraphQL 500 error');
    },
    paginate: async () => {
      throw new Error('REST 404 error');
    },
    rest: {
      issues: {
        listEventsForTimeline: 'dummy-fn'
      }
    }
  };

  const infoMessages = [];
  const mockCore = {
    info: (msg) => infoMessages.push(msg)
  };

  const nodes = await fetchTimelineEvents(mockGithub, 'google', 'xmc', 123, mockCore);
  assert.deepStrictEqual(nodes, []);
  assert.ok(infoMessages.some(m => m.includes('REST timeline fetch failed')));
});

test('fetchTimelineEvents returns empty array when GraphQL succeeds with no events without falling back to REST', async () => {
  let restCalled = false;
  const mockGithub = {
    graphql: async () => {
      return {
        repository: {
          pullRequest: {
            timelineItems: {
              nodes: [],
              pageInfo: {
                hasNextPage: false,
                endCursor: null
              }
            }
          }
        }
      };
    },
    paginate: async () => {
      restCalled = true;
      return [{ id: 999 }];
    }
  };

  const mockCore = { info: () => {} };
  const nodes = await fetchTimelineEvents(mockGithub, 'google', 'xmc', 123, mockCore);
  assert.deepStrictEqual(nodes, []);
  assert.strictEqual(restCalled, false);
});

test('fetchGraphQLTimeline iterates pages and respects maxPages limit', async () => {
  let callCount = 0;
  const mockGithub = {
    graphql: async () => {
      callCount++;
      return {
        repository: {
          pullRequest: {
            timelineItems: {
              nodes: [{ id: callCount }],
              pageInfo: {
                hasNextPage: true,
                endCursor: `cursor-${callCount}`
              }
            }
          }
        }
      };
    }
  };

  const logs = [];
  const mockCore = { info: (msg) => logs.push(msg) };
  const nodes = await fetchGraphQLTimeline(mockGithub, 'google', 'xmc', 123, mockCore, 2);
  assert.strictEqual(callCount, 2);
  assert.strictEqual(nodes.length, 2);
  assert.ok(logs.some(l => l.includes('Reached maximum timeline pages limit (2)')));
});

test('fetchGraphQLTimeline defaults maxPages to MAX_TIMELINE_PAGES when omitted', async () => {
  let callCount = 0;
  const mockGithub = {
    graphql: async () => {
      callCount++;
      return {
        repository: {
          pullRequest: {
            timelineItems: {
              nodes: [{ id: callCount }],
              pageInfo: {
                hasNextPage: callCount < 5,
                endCursor: `cursor-${callCount}`
              }
            }
          }
        }
      };
    }
  };

  const nodes = await fetchGraphQLTimeline(mockGithub, 'google', 'xmc', 123, {});
  assert.strictEqual(callCount, 5);
  assert.strictEqual(nodes.length, 5);
});

test('fetchRestTimeline paginates events via REST and catches errors', async () => {
  const mockGithub = {
    rest: {
      issues: {
        listEventsForTimeline: () => {}
      }
    },
    paginate: async () => [{ id: 101 }, { id: 102 }]
  };
  const nodes = await fetchRestTimeline(mockGithub, 'google', 'xmc', 123, {});
  assert.strictEqual(nodes.length, 2);

  const failingGithub = {
    rest: {
      issues: {
        listEventsForTimeline: () => {}
      }
    },
    paginate: async () => { throw new Error('API Rate Limit'); }
  };
  const emptyNodes = await fetchRestTimeline(failingGithub, 'google', 'xmc', 123, {});
  assert.deepStrictEqual(emptyNodes, []);
});


