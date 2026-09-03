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
const childProcess = require('child_process');
const {
  DEFAULT_MAX_OWNER_GRACE_LINES,
  getMaxOwnerGraceLines,
  isProtectedFile,
  checkProtectedFileChanges,
  evaluateIncrementalApproval,
  formatReapprovalReviewBody
} = require('./approval-policy');

test('isProtectedFile accurately identifies sensitive repository files', () => {
  assert.strictEqual(isProtectedFile('.github/workflows/unit-tests.yml'), true);
  assert.strictEqual(isProtectedFile('.github/scripts/incremental-approval.js'), true);
  assert.strictEqual(isProtectedFile('OWNERS'), true);
  assert.strictEqual(isProtectedFile('.githooks/pre-commit'), true);
  assert.strictEqual(isProtectedFile('pkg/service/data_engine.go'), false);
  assert.strictEqual(isProtectedFile('README.md'), false);
  assert.strictEqual(isProtectedFile(''), false);
  assert.strictEqual(isProtectedFile(null), false);
});

test('checkProtectedFileChanges detects sensitive modifications and handles git failure', () => {
  const originalExec = childProcess.execFileSync;

  // Sensitive files modified
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      return '.github/scripts/incremental-approval.js\ncmd/server/main.go\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  try {
    const hasProtected = checkProtectedFileChanges('sha1', 'sha2');
    assert.strictEqual(hasProtected, true);
  } finally {
    childProcess.execFileSync = originalExec;
  }

  // Non-sensitive files modified
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      return 'pkg/main.go\nREADME.md\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  try {
    const hasProtected = checkProtectedFileChanges('sha1', 'sha2');
    assert.strictEqual(hasProtected, false);
  } finally {
    childProcess.execFileSync = originalExec;
  }

  // Git failure fail-safe (with stderr)
  childProcess.execFileSync = () => {
    const err = new Error('Fatal git error');
    err.stderr = 'fatal: bad object sha1\n';
    throw err;
  };

  try {
    const hasProtected = checkProtectedFileChanges('sha1', 'sha2');
    assert.strictEqual(hasProtected, true);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('evaluateIncrementalApproval handles undefined arguments safely without throwing', () => {
  const result = evaluateIncrementalApproval();
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NO_PRIOR_APPROVAL');
});

test('formatReapprovalReviewBody formats markdown body accurately', () => {
  const body = formatReapprovalReviewBody('Clean rebase detected.', 'alice', 'abcdef1234567890');
  assert.match(body, /🤖 \*\*Incremental PR Approval\*\*/);
  assert.match(body, /Clean rebase detected\./);
  assert.match(body, /@alice/);
  assert.match(body, /abcdef1/);
});

test('evaluateIncrementalApproval rejects if no prior approval exists', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: false,
    approvedSha: null,
    currentSha: 'sha-12345',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NO_PRIOR_APPROVAL');
});

test('evaluateIncrementalApproval skips if PR is already approved at current SHA and not dismissed', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-12345',
    currentSha: 'sha-12345',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true,
    isDismissed: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'ALREADY_APPROVED');
});

test('evaluateIncrementalApproval reapproves if approval on current SHA was dismissed (stale approval)', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-12345',
    currentSha: 'sha-12345',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true,
    isDismissed: true
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'RESTORE_DISMISSED_APPROVAL');
});

test('evaluateIncrementalApproval rejects dismissed approval on same SHA if sensitive files modified', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-12345',
    currentSha: 'sha-12345',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true,
    hasProtectedFileChanges: true,
    isDismissed: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'PROTECTED_FILES_MODIFIED');
});

test('evaluateIncrementalApproval rejects removed commits even if deltaLines is 0', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: false,
    hasRemovedCommits: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'REMOVED_COMMITS');
});

test('evaluateIncrementalApproval rejects when sensitive CI/ownership files are modified', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 0,
    isCleanRebase: true,
    hasProtectedFileChanges: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'PROTECTED_FILES_MODIFIED');
});

test('evaluateIncrementalApproval auto-reapproves pure clean rebase with 0 delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 0,
    isCleanRebase: true,
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
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
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
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
    deltaLines: 10,
    isCleanRebase: false,
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
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
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
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
    deltaLines: 2,
    isCleanRebase: false,
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NON_OWNER_MODIFICATION');
});

test('evaluateIncrementalApproval rejects invalid delta inputs (negative, null, NaN)', () => {
  const resultNeg = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: -1,
    isCleanRebase: false
  });
  assert.strictEqual(resultNeg.shouldReapprove, false);
  assert.strictEqual(resultNeg.trigger, 'INVALID_DELTA');

  const resultNaN = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: NaN,
    isCleanRebase: false
  });
  assert.strictEqual(resultNaN.shouldReapprove, false);
  assert.strictEqual(resultNaN.trigger, 'INVALID_DELTA');

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

test('getMaxOwnerGraceLines resolves custom env or fallback default', () => {
  assert.strictEqual(getMaxOwnerGraceLines('25'), 25);
  assert.strictEqual(getMaxOwnerGraceLines(15), 15);
  assert.strictEqual(getMaxOwnerGraceLines('0'), 0);
  assert.strictEqual(getMaxOwnerGraceLines(''), DEFAULT_MAX_OWNER_GRACE_LINES);
  assert.strictEqual(getMaxOwnerGraceLines(undefined), DEFAULT_MAX_OWNER_GRACE_LINES);
  assert.strictEqual(getMaxOwnerGraceLines(null), DEFAULT_MAX_OWNER_GRACE_LINES);
  assert.strictEqual(getMaxOwnerGraceLines('invalid'), DEFAULT_MAX_OWNER_GRACE_LINES);
  assert.strictEqual(getMaxOwnerGraceLines('-5'), DEFAULT_MAX_OWNER_GRACE_LINES);
});
