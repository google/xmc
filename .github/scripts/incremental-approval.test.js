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
  validateGitRefs,
  isBotUser,
  extractDismissalContext,
  parseTimelineReviewEvent,
  findLatestApprovalFromTimeline,
  findLatestApprovalFromReviews,
  findLatestApproval,
  parseRangeDiff,
  evaluateIncrementalApproval,
  resolveCommitRanges,
  runGitRangeDiff,
  MAX_OWNER_GRACE_LINES
} = require('./incremental-approval');

test('validateGitRefs accepts valid git refs and commit SHAs', () => {
  assert.doesNotThrow(() => {
    validateGitRefs('main', 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e', 'fa0f5830b0e8a78119efefeba44bd75939f20eee');
  });
  assert.doesNotThrow(() => {
    validateGitRefs('feat/new-feature', 'cfe1eb7', 'fa0f583');
  });
});

test('validateGitRefs rejects invalid base refs and malicious option injection', () => {
  assert.throws(() => validateGitRefs('--upload-pack=evil', 'cfe1eb7', 'fa0f583'), /Invalid base reference/);
  assert.throws(() => validateGitRefs('main..evil', 'cfe1eb7', 'fa0f583'), /Invalid base reference/);
  assert.throws(() => validateGitRefs('', 'cfe1eb7', 'fa0f583'), /Invalid base reference/);
  assert.throws(() => validateGitRefs('main', 'not-a-sha', 'fa0f583'), /Invalid approved commit SHA/);
  assert.throws(() => validateGitRefs('main', 'cfe1eb7', '--exec=evil'), /Invalid current commit SHA/);
});

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
    { event: 'dismissed_review', review_id: 101 },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      review: { databaseId: 202 }
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'CHANGES_REQUESTED',
      review: { databaseId: 303 }
    }
  ];
  const ctx = extractDismissalContext(events);
  assert.strictEqual(ctx.manualDismissalIds.has(101), true);
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(202), 'APPROVED');
  assert.strictEqual(ctx.dismissedReviewPreviousStates.get(303), 'CHANGES_REQUESTED');
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
  assert.strictEqual(manualDismissed, null);

  // Stale dismissed with APPROVED previous state
  const staleApproved = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 202,
    user: { login: 'reviewer1' },
    state: 'dismissed',
    commit_id: 'sha1'
  }, context);
  assert.strictEqual(staleApproved.state, 'APPROVED');

  // Stale dismissed with CHANGES_REQUESTED previous state
  const staleChanges = parseTimelineReviewEvent({
    event: 'reviewed',
    id: 303,
    user: { login: 'reviewer1' },
    state: 'dismissed',
    commit_id: 'sha1'
  }, context);
  assert.strictEqual(staleChanges.state, 'CHANGES_REQUESTED');

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
  const result = findLatestApproval(reviews, 'charlie');
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
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T11:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
  assert.deepStrictEqual(result, {
    approver: 'alice',
    approvedSha: 'sha-111111',
    submittedAt: '2026-08-25T10:00:00Z'
  });
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
      id: 5032149088,
      user: { login: 'andrzejfudro' },
      state: 'DISMISSED',
      commit_id: '2a56f774b68ac7347a854ba276c7c6898f47282a',
      submitted_at: '2026-08-26T15:12:24Z'
    }
  ];
  const timelineEvents = [
    {
      __typename: 'PullRequestReview',
      databaseId: 5032149088,
      author: { login: 'andrzejfudro' },
      state: 'DISMISSED',
      commit: { oid: '2a56f774b68ac7347a854ba276c7c6898f47282a' },
      submittedAt: '2026-08-26T15:12:24Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'APPROVED',
      review: {
        databaseId: 5032149088
      }
    }
  ];
  const result = findLatestApproval(reviews, 'VLukiantsev', timelineEvents);
  assert.deepStrictEqual(result, {
    approver: 'andrzejfudro',
    approvedSha: '2a56f774b68ac7347a854ba276c7c6898f47282a',
    submittedAt: '2026-08-26T15:12:24Z'
  });
});

test('findLatestApproval blocks auto-approval when dismissed review had CHANGES_REQUESTED previous state', () => {
  const reviews = [
    {
      id: 5032149088,
      user: { login: 'andrzejfudro' },
      state: 'DISMISSED',
      commit_id: '2a56f774b68ac7347a854ba276c7c6898f47282a',
      submitted_at: '2026-08-26T15:12:24Z'
    }
  ];
  const timelineEvents = [
    {
      __typename: 'PullRequestReview',
      databaseId: 5032149088,
      author: { login: 'andrzejfudro' },
      state: 'DISMISSED',
      commit: { oid: '2a56f774b68ac7347a854ba276c7c6898f47282a' },
      submittedAt: '2026-08-26T15:12:24Z'
    },
    {
      __typename: 'ReviewDismissedEvent',
      previousReviewState: 'CHANGES_REQUESTED',
      review: {
        databaseId: 5032149088
      }
    }
  ];
  const result = findLatestApproval(reviews, 'VLukiantsev', timelineEvents);
  assert.strictEqual(result, null);
});

test('findLatestApproval ignores manually dismissed reviews in timeline', () => {
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
      state: 'approved',
      commit_id: 'sha-111111',
      submitted_at: '2026-08-25T10:00:00Z'
    },
    {
      event: 'dismissed_review',
      review_id: 1,
      dismissal_message: 'Dismissed by author due to security bug'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie', timelineEvents);
  assert.strictEqual(result, null);
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

test('parseRangeDiff identifies pure rebase with zero delta', () => {
  const sampleOutput = [
    '1:  a1b2c3d = 1:  e5f6g7h First commit',
    '2:  1234567 = 2:  89abcdef Second commit'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, true);
  assert.strictEqual(result.hasUnmatchedCommits, false);
  assert.strictEqual(result.totalDelta, 0);
  assert.strictEqual(result.additions, 0);
  assert.strictEqual(result.deletions, 0);
});

test('parseRangeDiff ignores unchanged commit additions and deletions in Column 2', () => {
  // Column 1 is ' ', Column 2 is '+' or '-' -> Unchanged in this revision
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit title',
    '    @@ src/main.go:10,5 @@',
    '      context line',
    '     +existing addition from v1',
    '     -existing deletion from v1',
    '    ++new addition in v2',
    '    -+removed addition in v2'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, false);
  assert.strictEqual(result.additions, 1); // Only ++ is added in v2
  assert.strictEqual(result.deletions, 1); // Only -+ is removed in v2
  assert.strictEqual(result.totalDelta, 2);
});

test('parseRangeDiff ignores Author/Commit keywords in code comments', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit title',
    '    @@ src/main.go:10,5 @@',
    '      // Author: Alice',
    '      // Commit: Initial implementation',
    '    ++actualNewCodeLine()'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.additions, 1);
  assert.strictEqual(result.totalDelta, 1);
});

test('parseRangeDiff ignores Commit message hunks', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit title',
    '    ## Commit message ##',
    '    @@ -1,3 +1,4 @@',
    '     -Old commit message line',
    '     +New commit message line',
    '    ## src/main.go ##',
    '    @@ src/main.go:1,3 @@',
    '    ++newActualCodeLine()'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.additions, 1);
  assert.strictEqual(result.deletions, 0);
  assert.strictEqual(result.totalDelta, 1);
});

test('parseRangeDiff identifies added, removed, or squashed commits as unmatched', () => {
  const sampleAdded = [
    '1:  a1b2c3d = 1:  e5f6g7h First commit',
    '-:  ------- > 2:  89abcdef Added new commit'
  ].join('\n');

  const resultAdded = parseRangeDiff(sampleAdded);
  assert.strictEqual(resultAdded.isCleanRebase, false);
  assert.strictEqual(resultAdded.hasUnmatchedCommits, true);
  assert.strictEqual(resultAdded.totalDelta, 0);

  const sampleRemoved = [
    '1:  a1b2c3d < -:  ------- Removed commit'
  ].join('\n');

  const resultRemoved = parseRangeDiff(sampleRemoved);
  assert.strictEqual(resultRemoved.isCleanRebase, false);
  assert.strictEqual(resultRemoved.hasUnmatchedCommits, true);
});

test('evaluateIncrementalApproval rejects if no prior approval exists', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: false,
    approvedSha: null,
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NO_PRIOR_APPROVAL');
});

test('evaluateIncrementalApproval skips if PR is already approved at current SHA', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-222',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'ALREADY_APPROVED');
});

test('evaluateIncrementalApproval rejects unmatched/added commits even if deltaLines is 0', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 0,
    isCleanRebase: false,
    hasUnmatchedCommits: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'UNMATCHED_COMMITS');
});

test('evaluateIncrementalApproval auto-reapproves pure clean rebase with 0 delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 0,
    isCleanRebase: true,
    hasUnmatchedCommits: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'CLEAN_REBASE');
});

test('evaluateIncrementalApproval auto-reapproves code owner within 10-line grace delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 8,
    isCleanRebase: false,
    hasUnmatchedCommits: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
});

test('evaluateIncrementalApproval auto-reapproves code owner with exactly 10 lines delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: MAX_OWNER_GRACE_LINES,
    isCleanRebase: false,
    hasUnmatchedCommits: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
});

test('evaluateIncrementalApproval rejects code owner when delta exceeds 10 lines', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 11,
    isCleanRebase: false,
    hasUnmatchedCommits: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'EXCEEDS_OWNER_GRACE');
});

test('evaluateIncrementalApproval rejects non-owner when delta > 0', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 1,
    isCleanRebase: false,
    hasUnmatchedCommits: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NON_OWNER_MODIFICATION');
});

test('evaluateIncrementalApproval rejects invalid delta inputs (negative, null, NaN)', () => {
  const resultNegative = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: -1,
    isCleanRebase: false
  });
  assert.strictEqual(resultNegative.shouldReapprove, false);
  assert.strictEqual(resultNegative.trigger, 'INVALID_DELTA');

  const resultNull = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: null,
    isCleanRebase: false
  });
  assert.strictEqual(resultNull.shouldReapprove, false);
  assert.strictEqual(resultNull.trigger, 'INVALID_DELTA');
});

test('resolveCommitRanges uses standard ranges when commit count matches', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (args[0] === 'rev-list' && args[2] === 'origin/main..head2') {
      return '3\n';
    }
    if (args[0] === 'rev-parse') {
      return 'head1~3\n';
    }
    if (args[0] === 'rev-list' && args[2] === 'origin/main..head1') {
      return '3\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./incremental-approval')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./incremental-approval');

  try {
    const res = mockedResolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./incremental-approval')];
  }
});

test('resolveCommitRanges scopes approved range when parent branch was rebased in stacked PRs', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (args[0] === 'rev-list' && args[2] === 'origin/feat/base..head2') {
      return '6\n';
    }
    if (args[0] === 'rev-parse' && args[2] === 'head1~6') {
      return 'ok\n';
    }
    if (args[0] === 'rev-list' && args[2] === 'origin/feat/base..head1') {
      return '17\n'; // 11 rebased base commits + 6 PR commits
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./incremental-approval')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./incremental-approval');

  try {
    const res = mockedResolveCommitRanges('feat/base', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/feat/base..head2');
    assert.strictEqual(res.approvedRange, 'head1~6..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./incremental-approval')];
  }
});

test('resolveCommitRanges preserves standard range when developer adds new commits to regular PR', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (args[0] === 'rev-list' && args[2] === 'origin/main..head2') {
      return '4\n'; // PR now has 4 commits
    }
    if (args[0] === 'rev-parse' && args[2] === 'head1~4') {
      return 'ok\n';
    }
    if (args[0] === 'rev-list' && args[2] === 'origin/main..head1') {
      return '3\n'; // PR previously had 3 commits (approvedBaseCount < prCommitCount)
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./incremental-approval')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./incremental-approval');

  try {
    const res = mockedResolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./incremental-approval')];
  }
});

test('runGitRangeDiff executes range-diff without usage errors', () => {
  const headSha = require('child_process').execSync('git rev-parse HEAD', { encoding: 'utf8' }).trim();
  const output = runGitRangeDiff('main', headSha, headSha);
  assert.strictEqual(typeof output, 'string');
});
