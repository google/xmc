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
  DEFAULT_MAX_OWNER_GRACE_LINES,
  MAX_OWNER_GRACE_LINES,
  getMaxOwnerGraceLines,
  isProtectedFile,
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

test('checkProtectedFileChanges detects sensitive modifications and handles git failure', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  // Sensitive file modified
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      return 'pkg/main.go\n.github/workflows/ci.yml\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheckProtectedFileChanges } = require('./approval-policy');

  try {
    assert.strictEqual(mockedCheckProtectedFileChanges('sha1', 'sha2'), true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }

  // Safe files modified
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      return 'pkg/main.go\nREADME.md\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheckSafe } = require('./approval-policy');

  try {
    assert.strictEqual(mockedCheckSafe('sha1', 'sha2'), false);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }

  // Subprocess error fail-safe
  childProcess.execFileSync = () => {
    throw new Error('Fatal git error');
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheckError } = require('./approval-policy');

  try {
    assert.strictEqual(mockedCheckError('sha1', 'sha2'), true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('checkProtectedFileChanges uses 3-dot diff against baseRef and avoids false positives from base branch (PR #559 incident fix)', () => {
  const childProcess = require('child_process');
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

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheck } = require('./approval-policy');

  try {
    const hasProtected = mockedCheck('feat/tools-dev-env-helm-deployer', '8fa2009', 'c40cf0f');
    assert.strictEqual(hasProtected, false, 'Should NOT flag protected file change when only base branch modified it');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('checkProtectedFileChanges flags sensitive files when PR modifies protected files against baseRef', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      if (args[2] === 'origin/main...head-sha') {
        return 'src/main.go\n.github/workflows/deploy.yml\n';
      }
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheck } = require('./approval-policy');

  try {
    const hasProtected = mockedCheck('main', 'approved-sha', 'head-sha');
    assert.strictEqual(hasProtected, true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('checkProtectedFileChanges detects protected file modification in fast-forward follow-up commit', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff') {
      if (args[2] === 'origin/main...head-sha') {
        return 'src/main.go\n';
      }
      if (args[2] === 'approved-sha' && args[3] === 'head-sha') {
        return 'OWNERS\n';
      }
    }
    if (cmd === 'git' && args[0] === 'merge-base' && args[1] === '--is-ancestor') {
      return ''; // Fast-forward, approved-sha IS ancestor of head-sha
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkProtectedFileChanges: mockedCheck } = require('./approval-policy');

  try {
    const hasProtected = mockedCheck('main', 'approved-sha', 'head-sha');
    assert.strictEqual(hasProtected, true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('formatReapprovalReviewBody formats markdown body accurately', () => {
  const body = formatReapprovalReviewBody('Clean rebase detected.', 'janzak-dev', '8fa20093b2f3af154f4b4cb4082b2636a4f6a45c');
  assert.ok(body.includes('Clean rebase detected.'));
  assert.ok(body.includes('@janzak-dev'));
  assert.ok(body.includes('8fa2009'));
});

test('evaluateIncrementalApproval rejects if no prior approval exists', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: false,
    approvedSha: null,
    currentSha: 'sha2',
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
    approvedSha: 'sha1',
    currentSha: 'sha1',
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
    approvedSha: 'sha1',
    currentSha: 'sha1',
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
    approvedSha: 'sha1',
    currentSha: 'sha1',
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
    approvedSha: 'sha1',
    currentSha: 'sha2',
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
    approvedSha: 'sha1',
    currentSha: 'sha2',
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
    approvedSha: 'sha1',
    currentSha: 'sha2',
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
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: true,
    deltaLines: 5,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
});

test('evaluateIncrementalApproval auto-reapproves code owner with exactly 10 lines delta', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: true,
    deltaLines: 10,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, true);
  assert.strictEqual(result.trigger, 'OWNER_GRACE_DELTA');
});

test('evaluateIncrementalApproval rejects code owner when delta exceeds 10 lines', () => {
  const result = evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha1',
    currentSha: 'sha2',
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
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: false,
    deltaLines: 1,
    isCleanRebase: false
  });
  assert.strictEqual(result.shouldReapprove, false);
  assert.strictEqual(result.trigger, 'NON_OWNER_MODIFICATION');
});

test('evaluateIncrementalApproval rejects invalid delta inputs (negative, null, NaN)', () => {
  assert.strictEqual(evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: true,
    deltaLines: -1,
    isCleanRebase: false
  }).trigger, 'INVALID_DELTA');

  assert.strictEqual(evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: true,
    deltaLines: NaN,
    isCleanRebase: false
  }).trigger, 'INVALID_DELTA');

  assert.strictEqual(evaluateIncrementalApproval({
    hasPriorApproval: true,
    approvedSha: 'sha1',
    currentSha: 'sha2',
    isAuthorOwner: true,
    deltaLines: null,
    isCleanRebase: false
  }).trigger, 'INVALID_DELTA');
});

test('getMaxOwnerGraceLines resolves custom env or fallback default', () => {
  assert.strictEqual(getMaxOwnerGraceLines('15'), 15);
  assert.strictEqual(getMaxOwnerGraceLines('0'), 0);
  assert.strictEqual(getMaxOwnerGraceLines('invalid'), 10);
  assert.strictEqual(getMaxOwnerGraceLines(undefined), 10);
  assert.strictEqual(getMaxOwnerGraceLines('', 20), 20);
});

test('checkAddedCommitsForProtectedFiles detects protected file in added commits', () => {
  const childProcess = require('child_process');
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      const sha = args[args.length - 1];
      if (sha === 'sha-normal') {
        return 'pkg/foo.go\n';
      }
      if (sha === 'sha-sensitive') {
        return '.github/workflows/ci.yml\n';
      }
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./approval-policy')];
  delete require.cache[require.resolve('./git-range-diff')];
  const { checkAddedCommitsForProtectedFiles: mockedCheck } = require('./approval-policy');

  try {
    assert.strictEqual(mockedCheck([]), false);
    assert.strictEqual(mockedCheck(null), false);
    assert.strictEqual(mockedCheck(['sha-normal']), false);
    assert.strictEqual(mockedCheck(['sha-normal', 'sha-sensitive']), true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./approval-policy')];
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

