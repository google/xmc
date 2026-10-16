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
  hasProtectedFile,
  checkProtectedFileChanges,
  checkAddedCommitsForProtectedFiles,
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

test('isProtectedFile protects only the exact protobuf breaking-change baseline path', () => {
  assert.strictEqual(isProtectedFile('api/testdata/protos_baseline.binpb'), true);
  assert.strictEqual(isProtectedFile('api\\testdata\\protos_baseline.binpb'), true);
  assert.strictEqual(isProtectedFile('api/testdata/other.binpb'), false);
  assert.strictEqual(isProtectedFile('api/testdata/protos_baseline.binpb.orig'), false);
  assert.strictEqual(isProtectedFile('third_party/api/testdata/protos_baseline.binpb'), false);
});

test('isProtectedFile protects the protobuf breaking-check configuration but not nested BUILD files', () => {
  assert.strictEqual(isProtectedFile('api/buf.yaml'), true);
  assert.strictEqual(isProtectedFile('api/BUILD.bazel'), true);
  assert.strictEqual(isProtectedFile('api/BUILD'), true);
  assert.strictEqual(isProtectedFile('api/protos/xid/BUILD.bazel'), false);
  assert.strictEqual(isProtectedFile('api/filter/go/queryfilterbase/BUILD.bazel'), false);
  assert.strictEqual(isProtectedFile('api/buf.lock'), false);
  assert.strictEqual(isProtectedFile('api/buf.yaml.orig'), false);
});

test('hasProtectedFile checks arrays of paths and handles null/undefined inputs safely', () => {
  assert.strictEqual(hasProtectedFile(['pkg/main.go', '.github/workflows/test.yml']), true);
  assert.strictEqual(hasProtectedFile(['pkg/main.go', 'README.md']), false);
  assert.strictEqual(hasProtectedFile([]), false);
  assert.strictEqual(hasProtectedFile(null), false);
  assert.strictEqual(hasProtectedFile(undefined), false);
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

test('checkProtectedFileChanges uses 3-dot diff against baseRef and avoids false positives from base branch (PR #559 incident fix)', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      if (args[2] === 'origin/feat/tools-dev-env-helm-deployer...c40cf0f') {
        return 'tools/dev_env/local_runner.py\ntools/dev_env/local_runner_test.py\n';
      }
      if (args[2] === '8fa2009' && args[3] === 'c40cf0f') {
        return '.github/scripts/incremental-approval.js\ntools/dev_env/local_runner.py\n';
      }
    }
    if (cmd === 'git' && args[0] === 'merge-base' && args[1] === '--is-ancestor') {
      const err = new Error('not ancestor after rebase');
      err.status = 1;
      throw err;
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const hasProtected = checkProtectedFileChanges('feat/tools-dev-env-helm-deployer', '8fa2009', 'c40cf0f');
    assert.strictEqual(hasProtected, false);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('checkProtectedFileChanges detects sensitive files in 3-dot diff', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff' && args[2] === 'origin/main...c40cf0f') {
      return '.github/workflows/incremental-pr-approval.yml\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const hasProtected = checkProtectedFileChanges('main', '8fa2009', 'c40cf0f');
    assert.strictEqual(hasProtected, true);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('checkProtectedFileChanges respects 3-argument signature even when execOptions includes encoding property', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff' && args[2] === 'origin/main...c40cf0f') {
      return 'pkg/foo.go\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const hasProtected = checkProtectedFileChanges('main', '8fa2009', 'c40cf0f', { encoding: 'utf8' });
    assert.strictEqual(hasProtected, false);
  } finally {
    childProcess.execFileSync = originalExec;
  }
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
  assert.match(result.reason, /protobuf breaking-change baseline or its configuration \(.*api\/testdata\/protos_baseline\.binpb, api\/buf\.yaml, api\/BUILD\.bazel\)/);
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

test('evaluateIncrementalApproval auto-reapproves code owner within 50-line grace delta', () => {
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

test('evaluateIncrementalApproval auto-reapproves code owner with exactly 50 lines delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 50,
    isCleanRebase: false,
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
});

test('evaluateIncrementalApproval rejects code owner when delta exceeds 50 lines', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 51,
    isCleanRebase: false,
    hasRemovedCommits: false,
    hasProtectedFileChanges: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'EXCEEDS_OWNER_GRACE');
});

test('evaluateIncrementalApproval excludes test file lines from code owner grace budget', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 550,
    testDeltaLines: 500,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
  assert.match(result.reason, /50 lines, excluding 500 test file lines/);
});

test('evaluateIncrementalApproval rejects code owner when non-test lines exceed budget despite test lines', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 251,
    testDeltaLines: 200,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'EXCEEDS_OWNER_GRACE');
});

test('evaluateIncrementalApproval rejects non-owner even for test-only changes', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: false,
    deltaLines: 1,
    testDeltaLines: 1,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NON_OWNER_MODIFICATION');
});

test('evaluateIncrementalApproval keeps protected file lockout for code owner test-only changes', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha-111',
    currentSha: 'sha-222',
    isAuthorOwner: true,
    deltaLines: 5,
    testDeltaLines: 5,
    isCleanRebase: false,
    hasProtectedFileChanges: true
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'PROTECTED_FILES_MODIFIED');
});

test('evaluateIncrementalApproval rejects invalid test delta inputs', () => {
  for (const testDeltaLines of [-1, NaN, null, 11]) {
    const result = evaluateIncrementalApproval({
      hasPriorApproval: true,
      approvedSha: 'sha-111',
      currentSha: 'sha-222',
      isAuthorOwner: true,
      deltaLines: 10,
      testDeltaLines,
      isCleanRebase: false
    });
    assert.strictEqual(result.shouldReapprove, false, String(testDeltaLines));
    assert.strictEqual(result.trigger, 'INVALID_DELTA', String(testDeltaLines));
  }
});

test('DEFAULT_MAX_OWNER_GRACE_LINES is 50', () => {
  assert.strictEqual(DEFAULT_MAX_OWNER_GRACE_LINES, 50);
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

test('checkAddedCommitsForProtectedFiles detects protected file in added commits', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      assert.ok(args.includes('--no-commit-id'), 'should pass --no-commit-id to diff-tree');
      const sha = args[args.length - 1];
      if (sha === 'sha-normal') {
        return 'pkg/foo.go\n';
      }
      if (sha === 'sha-sensitive') {
        return '.github/workflows/ci.yml\n';
      }
      if (sha === 'sha-baseline-refresh') {
        return 'api/testdata/protos_baseline.binpb\n';
      }
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  try {
    assert.strictEqual(checkAddedCommitsForProtectedFiles([]), false);
    assert.strictEqual(checkAddedCommitsForProtectedFiles(null), false);
    assert.strictEqual(checkAddedCommitsForProtectedFiles(['sha-normal']), false);
    assert.strictEqual(checkAddedCommitsForProtectedFiles(['sha-normal', 'sha-sensitive']), true);
    assert.strictEqual(checkAddedCommitsForProtectedFiles(['sha-normal', 'sha-baseline-refresh']), true);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('checkAddedCommitsForProtectedFiles returns true on git error to fail-safe', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = () => {
    throw new Error('git diff-tree failed: corrupt commit');
  };

  try {
    assert.strictEqual(checkAddedCommitsForProtectedFiles(['sha-corrupt']), true);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});
