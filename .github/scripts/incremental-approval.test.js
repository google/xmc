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
  findLatestApproval,
  parseRangeDiff,
  evaluateIncrementalApproval,
  MAX_OWNER_GRACE_LINES
} = require('./incremental-approval');

test('findLatestApproval returns null when reviews list is empty or null', () => {
  assert.strictEqual(findLatestApproval([]), null);
  assert.strictEqual(findLatestApproval(null), null);
});

test('findLatestApproval finds latest approved review correctly', () => {
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

test('findLatestApproval ignores approvals superseded by CHANGES_REQUESTED', () => {
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
      state: 'CHANGES_REQUESTED',
      commit_id: 'sha-222222',
      submitted_at: '2026-08-25T12:00:00Z'
    }
  ];
  const result = findLatestApproval(reviews, 'charlie');
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
  const sampleOutput = `
1:  a1b2c3d = 1:  e5f6g7h First commit
2:  1234567 = 2:  89abcdef Second commit
`;
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, true);
  assert.strictEqual(result.totalDelta, 0);
  assert.strictEqual(result.additions, 0);
  assert.strictEqual(result.deletions, 0);
});

test('parseRangeDiff handles empty or blank output as clean rebase', () => {
  const result = parseRangeDiff('');
  assert.strictEqual(result.isCleanRebase, true);
  assert.strictEqual(result.totalDelta, 0);
});

test('parseRangeDiff counts modified patch additions and deletions accurately', () => {
  const sampleOutput = `
1:  a1b2c3d = 1:  e5f6g7h First commit
2:  1234567 ! 2:  89abcdef Second commit
    @@ Metadata
    Author: Alice <alice@google.com>

    ## src/service.go ##
    @@ src/service.go:10,4 +10,6 @@
     func Handle() {
    +    validateContext()
    +    logTrace()
    -    oldMethod()
     }
`;
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, false);
  assert.strictEqual(result.additions, 2);
  assert.strictEqual(result.deletions, 1);
  assert.strictEqual(result.totalDelta, 3);
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

test('evaluateIncrementalApproval auto-reapproves clean rebase for any author', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 0,
    isCleanRebase: true
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
    isCleanRebase: false
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
    isCleanRebase: false
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
    isCleanRebase: false
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
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NON_OWNER_MODIFICATION');
});
