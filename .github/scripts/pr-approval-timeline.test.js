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
  GRAPHQL_TIMELINE_QUERY
} = require('./pr-approval-timeline');

test('isBotUser identifies bot usernames correctly', () => {
  assert.strictEqual(isBotUser('github-actions'), true);
  assert.strictEqual(isBotUser('google-cla[bot]'), true);
  assert.strictEqual(isBotUser('dependabot[bot]'), true);
  assert.strictEqual(isBotUser('VLukiantsev'), false);
  assert.strictEqual(isBotUser('alice'), false);
  assert.strictEqual(isBotUser(''), false);
  assert.strictEqual(isBotUser(null), false);
});

test('extractDismissalContext extracts manual dismissals and GraphQL previousReviewState', () => {
  const events = [
    {
      event: 'review_dismissed',
      dismissed_review: {
        review_id: 101,
        state: 'approved',
        dismissal_message: 'Manual rejection'
      }
    },
    {
      event: 'review_dismissed',
      dismissal_commit_id: 'commit-sha-stale',
      dismissed_review: {
        review_id: 202,
        state: 'approved'
      }
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'CHANGES_REQUESTED',
      pullRequestCommit: { commit: { oid: 'commit-sha-303' } },
      review: { databaseId: 303 }
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      dismissalMessage: 'Manual revocation',
      review: { databaseId: 404 }
    }
  ];

  const ctx = extractDismissalContext(events);
  assert.strictEqual(ctx.manualDismissalIds.has(101) || ctx.manualDismissalIds.has('101'), true);
  assert.strictEqual(ctx.manualDismissalIds.has(202), false);
  assert.strictEqual(ctx.manualDismissalIds.has(303), false);
  assert.strictEqual(ctx.manualDismissalIds.has(404) || ctx.manualDismissalIds.has('404'), true);
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(202), 'APPROVED');
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(303), 'CHANGES_REQUESTED');
});

test('parseTimelineReviewEvent parses valid review and respects dismissal context', () => {
  const context = {
    authorLower: 'author1',
    manualDismissalIds: new Set([101, '101']),
    dismissedReviewPreviousStates: new Map([[202, 'APPROVED'], ['202', 'APPROVED'], [303, 'CHANGES_REQUESTED'], ['303', 'CHANGES_REQUESTED']])
  };

  // Valid active review
  const valid = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 99,
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
});

test('resolveActiveApproval returns latest approval and blocks on CHANGES_REQUESTED', () => {
  const mapWithChanges = new Map([
    ['alice', { approver: 'alice', state: 'APPROVED', approvedSha: 'sha1', submittedAt: '2026-08-25T10:00:00Z' }],
    ['bob', { approver: 'bob', state: 'CHANGES_REQUESTED', approvedSha: 'sha2', submittedAt: '2026-08-25T11:00:00Z' }]
  ]);
  assert.strictEqual(resolveActiveApproval(mapWithChanges), null);

  const mapOnlyApprovals = new Map([
    ['alice', { approver: 'alice', state: 'APPROVED', approvedSha: 'sha1', submittedAt: '2026-08-25T10:00:00Z' }],
    ['bob', { approver: 'bob', state: 'APPROVED', approvedSha: 'sha2', submittedAt: '2026-08-25T11:00:00Z' }]
  ]);
  assert.deepStrictEqual(resolveActiveApproval(mapOnlyApprovals), {
    approver: 'bob',
    approvedSha: 'sha2',
    submittedAt: '2026-08-25T11:00:00Z',
    isDismissed: false
  });
});

test('findLatestApproval ignores approvals from bot users', () => {
  const reviews = [
    {
      id: 1,
      user: { login: 'github-actions[bot]' },
      state: 'APPROVED',
      commit_id: 'sha-bot-123456',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      id: 2,
      user: { login: 'janzak-dev' },
      state: 'APPROVED',
      commit_id: 'sha-human-789012',
      submitted_at: '2026-08-25T09:00:00Z'
    }
  ];

  const result = findLatestApproval(reviews, 'author');
  assert.strictEqual(result.approver, 'janzak-dev');
  assert.strictEqual(result.approvedSha, 'sha-human-789012');
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
      submitted_at: '2026-08-25T11:00:00Z'
    }
  ];

  const result = findLatestApproval(reviews, 'charlie');
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
      review: { databaseId: 1 }
    }
  ];

  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
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
      event: 'reviewed',
      id: 1,
      user: { login: 'alice' },
      state: 'DISMISSED',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      event: 'review_dismissed',
      dismissed_review: {
        review_id: 1,
        state: 'APPROVED',
        dismissal_message: 'Dismissed manually by human reviewer'
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
      dismissalMessage: 'Security concern raised by reviewer',
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
