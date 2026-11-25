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
const fs = require('fs');
const childProcess = require('child_process');
const incrementalApproval = require('./incremental-approval');
function createMockCore() {
  return {
    failed: [],
    infos: [],
    warnings: [],
    setFailed(msg) { this.failed.push(msg); },
    info(msg) { this.infos.push(msg); },
    warning(msg) { this.warnings.push(msg); }
  };
}

function createPullRequestContext(prOverrides = {}) {
  return {
    repo: { owner: 'google', repo: 'xmc' },
    payload: {
      pull_request: {
        number: 123,
        draft: false,
        user: { login: 'alice' },
        head: { sha: '2222222222222222222222222222222222222222' },
        base: { ref: 'main' },
        ...prOverrides
      }
    }
  };
}

function createMockGithub({ reviews, onCreateReview } = {}) {
  return {
    paginate: async () => reviews ?? [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: {
      pulls: {
        listReviews: 'dummy',
        createReview: onCreateReview || (async () => ({ status: 200 }))
      }
    }
  };
}

async function withPatchedIo({ execFileSync, existsSync, readFileSync }, fn) {
  const originalExec = childProcess.execFileSync;
  const originalExists = fs.existsSync;
  const originalRead = fs.readFileSync;
  if (execFileSync) childProcess.execFileSync = execFileSync;
  if (existsSync) fs.existsSync = existsSync;
  if (readFileSync) fs.readFileSync = readFileSync;
  try {
    return await fn();
  } finally {
    childProcess.execFileSync = originalExec;
    fs.existsSync = originalExists;
    fs.readFileSync = originalRead;
  }
}

function createGitExecMock({
  diffFiles = 'pkg/main.go\n',
  rangeDiff = '1:  1111111 = 1:  2222222 clean rebase\n',
  mergeBaseOk = true,
  diffTreeFiles = 'pkg/main.go\n',
  diffTreeNumstat = '1\t0\tpkg/main.go\n'
} = {}) {
  return (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') return 'ok\n';
    if (cmd === 'git' && args[0] === 'diff' && args[1] === '--name-only') return diffFiles;
    if (cmd === 'git' && args[0] === 'merge-base') {
      if (mergeBaseOk) return '';
      const err = new Error('not ancestor');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'rev-list') return '1\n';
    if (cmd === 'git' && args[0] === 'range-diff') return rangeDiff;
    if (cmd === 'git' && args[0] === 'diff-tree' && args.includes('--name-only')) return diffTreeFiles;
    if (cmd === 'git' && args[0] === 'diff-tree' && args.includes('--numstat')) return diffTreeNumstat;
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };
}

test('run fails when not in a pull request synchronize event', async () => {
  const core = createMockCore();
  await incrementalApproval.run({
    github: {},
    context: { payload: {} },
    core
  });
  assert.strictEqual(core.failed.length, 1);
  assert.match(core.failed[0], /must run in the context of a pull request/);
});

test('run skips auto-reapproval when PR is in draft mode', async () => {
  const core = createMockCore();
  await incrementalApproval.run({
    github: {},
    context: {
      payload: {
        pull_request: { number: 123, draft: true }
      }
    },
    core
  });
  assert.strictEqual(core.failed.length, 0);
  assert.ok(core.infos.some(m => m.includes('draft mode')));
});

test('run fails when head SHA or base ref is missing', async () => {
  const core = createMockCore();
  await incrementalApproval.run({
    github: {},
    context: {
      repo: { owner: 'google', repo: 'xmc' },
      payload: {
        pull_request: { number: 123, draft: false, user: { login: 'alice' }, head: {}, base: {} }
      }
    },
    core
  });
  assert.strictEqual(core.failed.length, 1);
  assert.match(core.failed[0], /missing head SHA or base ref/);
});

test('run fails when github review pagination fails', async () => {
  const core = createMockCore();
  const github = {
    paginate: async () => { throw new Error('API Rate Limit Exceeded'); },
    rest: { pulls: { listReviews: 'dummy' } }
  };
  await incrementalApproval.run({
    github,
    context: {
      repo: { owner: 'google', repo: 'xmc' },
      payload: {
        pull_request: {
          number: 123,
          draft: false,
          user: { login: 'alice' },
          head: { sha: 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e' },
          base: { ref: 'main' }
        }
      }
    },
    core
  });
  assert.strictEqual(core.failed.length, 1);
  assert.match(core.failed[0], /Failed to fetch PR reviews: API Rate Limit Exceeded/);
});

test('run skips when no prior approval is found', async () => {
  const core = createMockCore();
  const github = {
    paginate: async () => [],
    rest: { pulls: { listReviews: 'dummy' } }
  };
  await incrementalApproval.run({
    github,
    context: {
      repo: { owner: 'google', repo: 'xmc' },
      payload: {
        pull_request: {
          number: 123,
          draft: false,
          user: { login: 'alice' },
          head: { sha: 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e' },
          base: { ref: 'main' }
        }
      }
    },
    core
  });
  assert.strictEqual(core.failed.length, 0);
  assert.ok(core.infos.some(m => m.includes('does not have any prior active approvals')));
});

test('run warns and aborts when approved commit is unreachable on remote', async () => {
  const core = createMockCore();
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      if (args[3] === 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e') return 'ok\n';
      const err = new Error('not found');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'fetch') {
      const err = new Error('fetch failed');
      err.status = 128;
      throw err;
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  const github = {
    paginate: async () => [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: { pulls: { listReviews: 'dummy' } }
  };

  try {
    await incrementalApproval.run({
      github,
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        payload: {
          pull_request: {
            number: 123,
            draft: false,
            user: { login: 'alice' },
            head: { sha: 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e' },
            base: { ref: 'main' }
          }
        }
      },
      core
    });

    assert.strictEqual(core.warnings.length, 1);
    assert.match(core.warnings[0], /is no longer reachable on remote/);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('run warns and aborts gracefully when ensureGitRefs throws an unexpected error', async () => {
  const core = createMockCore();
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd) => {
    if (cmd === 'git') {
      throw new Error('git rev-parse unexpected failure');
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  const github = {
    paginate: async () => [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: { pulls: { listReviews: 'dummy' } }
  };

  try {
    await incrementalApproval.run({
      github,
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        payload: {
          pull_request: {
            number: 123,
            draft: false,
            user: { login: 'alice' },
            head: { sha: 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e' },
            base: { ref: 'main..invalid' }
          }
        }
      },
      core
    });

    assert.strictEqual(core.warnings.length, 1);
    assert.match(core.warnings[0], /Failed to verify git references.*Requiring manual review\./);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('run executes clean rebase auto-reapproval successfully', async () => {
  const core = createMockCore();
  const originalExec = childProcess.execFileSync;
  const originalExists = fs.existsSync;
  const originalRead = fs.readFileSync;
  let createdReview = null;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      return 'ok\n';
    }
    if (cmd === 'git' && args[0] === 'diff' && args[1] === '--name-only') {
      return 'tools/dev_env/runner.py\n';
    }
    if (cmd === 'git' && args[0] === 'merge-base') {
      const err = new Error('not ancestor');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'range-diff') {
      return '1:  1111111 = 1:  2222222 feat: my commit\n';
    }
    if (cmd === 'git' && args[0] === 'rev-list') {
      return '1\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  fs.existsSync = (p) => p === 'OWNERS';
  fs.readFileSync = () => 'alice\nbob\n';

  const github = {
    paginate: async () => [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: {
      pulls: {
        listReviews: 'dummy',
        createReview: async (params) => {
          createdReview = params;
          return { status: 200 };
        }
      }
    }
  };

  try {
    await incrementalApproval.run({
      github,
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        payload: {
          pull_request: {
            number: 123,
            draft: false,
            user: { login: 'alice' },
            head: { sha: '2222222222222222222222222222222222222222' },
            base: { ref: 'main' }
          }
        }
      },
      core
    });

    assert.strictEqual(core.failed.length, 0);
    assert.ok(createdReview);
    assert.strictEqual(createdReview.pull_number, 123);
    assert.strictEqual(createdReview.commit_id, '2222222222222222222222222222222222222222');
    assert.strictEqual(createdReview.event, 'APPROVE');
    assert.match(createdReview.body, /Clean rebase detected/);
    assert.match(createdReview.body, /Preserving prior approval by @bob/);
  } finally {
    childProcess.execFileSync = originalExec;
    fs.existsSync = originalExists;
    fs.readFileSync = originalRead;
  }
});

test('run warns and aborts when computing delta for added commits fails', async () => {
  const core = createMockCore();
  const originalExec = childProcess.execFileSync;
  const originalExists = fs.existsSync;
  const originalRead = fs.readFileSync;
  let createdReview = null;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      return 'ok\n';
    }
    if (cmd === 'git' && args[0] === 'diff' && args[1] === '--name-only') {
      return 'tools/dev_env/runner.py\n';
    }
    if (cmd === 'git' && args[0] === 'merge-base') {
      const err = new Error('not ancestor');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'range-diff') {
      return '1:  1111111 < -:  ------- commit 1\n-:  ------- > 1:  3333333 commit 2\n';
    }
    if (cmd === 'git' && args[0] === 'rev-list') {
      return '1\n';
    }
    if (cmd === 'git' && args[0] === 'diff-tree') {
      throw new Error('corrupt commit object 3333333');
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  fs.existsSync = (p) => p === 'OWNERS';
  fs.readFileSync = () => 'alice\nbob\n';

  const github = {
    paginate: async () => [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: {
      pulls: {
        listReviews: 'dummy',
        createReview: async (params) => {
          createdReview = params;
          return { status: 200 };
        }
      }
    }
  };

  try {
    await incrementalApproval.run({
      github,
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        payload: {
          pull_request: {
            number: 123,
            draft: false,
            user: { login: 'alice' },
            head: { sha: '3333333333333333333333333333333333333333' },
            base: { ref: 'main' }
          }
        }
      },
      core
    });

    assert.strictEqual(core.failed.length, 0);
    assert.strictEqual(createdReview, null);
    assert.strictEqual(core.warnings.length, 1);
    assert.match(core.warnings[0], /Unable to compute line delta for added commits/);
    assert.match(core.warnings[0], /corrupt commit object 3333333/);
  } finally {
    childProcess.execFileSync = originalExec;
    fs.existsSync = originalExists;
    fs.readFileSync = originalRead;
  }
});


test('run warns about undeterminable changed files instead of reporting protected files when no merge base exists', async () => {
  const core = createMockCore();
  const originalExec = childProcess.execFileSync;
  const originalExists = fs.existsSync;
  const originalRead = fs.readFileSync;
  let createdReview = null;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      return 'ok\n';
    }
    if (cmd === 'git' && args[0] === 'diff' && args[1] === '--name-only') {
      const err = new Error('git diff failed (exit 128): fatal: no merge base');
      err.stderr = 'fatal: origin/main...3333333: no merge base\n';
      throw err;
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  fs.existsSync = (p) => p === 'OWNERS';
  fs.readFileSync = () => 'alice\nbob\n';

  const github = {
    paginate: async () => [
      { id: 1, user: { login: 'bob' }, state: 'APPROVED', commit_id: '1111111111111111111111111111111111111111', submitted_at: '2026-09-01T00:00:00Z' }
    ],
    rest: {
      pulls: {
        listReviews: 'dummy',
        createReview: async (params) => {
          createdReview = params;
          return { status: 200 };
        }
      }
    }
  };

  try {
    await incrementalApproval.run({
      github,
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        payload: {
          pull_request: {
            number: 1236,
            draft: false,
            user: { login: 'alice' },
            head: { sha: '3333333333333333333333333333333333333333' },
            base: { ref: 'main' }
          }
        }
      },
      core
    });

    assert.strictEqual(core.failed.length, 0);
    assert.strictEqual(createdReview, null);
    assert.strictEqual(core.warnings.length, 1);
    assert.match(core.warnings[0], /Unable to determine files changed by PR #1236/);
    assert.match(core.warnings[0], /no merge base/);
    assert.ok(!core.infos.some((msg) => /modifies sensitive/.test(msg)));
  } finally {
    childProcess.execFileSync = originalExec;
    fs.existsSync = originalExists;
    fs.readFileSync = originalRead;
  }
});

test('describeError tolerates non-Error throws and appends trimmed stderr', () => {
  const { describeError } = incrementalApproval;
  assert.strictEqual(describeError(new Error('boom')), 'boom');
  const withStderr = Object.assign(new Error('git failed'), { stderr: 'fatal: no merge base\n' });
  assert.strictEqual(describeError(withStderr), 'git failed | Stderr: fatal: no merge base');
  assert.strictEqual(describeError(null), 'null');
  assert.strictEqual(describeError(undefined), 'undefined');
  assert.strictEqual(describeError('plain string'), 'plain string');
  assert.strictEqual(describeError({ stderr: 'oops' }), '[object Object] | Stderr: oops');
});

async function runWithGitMock(gitOpts, { prOverrides, onCreateReview, ownersExists = true } = {}) {
  const core = createMockCore();
  let createdReview = null;
  await withPatchedIo({
    execFileSync: createGitExecMock(gitOpts),
    existsSync: (p) => ownersExists && p === 'OWNERS',
    readFileSync: () => 'alice\nbob\n'
  }, async () => {
    await incrementalApproval.run({
      github: createMockGithub({
        onCreateReview: onCreateReview || (async (params) => { createdReview = params; return { status: 200 }; })
      }),
      context: createPullRequestContext(prOverrides),
      core
    });
  });
  return { core, createdReview };
}

test('validatePullRequestContext extracts context or fails/skips on invalid payloads', () => {
  const { validatePullRequestContext } = incrementalApproval;
  const validCore = createMockCore();
  assert.deepStrictEqual(validatePullRequestContext(createPullRequestContext(), validCore), {
    owner: 'google', repo: 'xmc', prNumber: 123, prAuthor: 'alice',
    currentSha: '2222222222222222222222222222222222222222', baseRef: 'main'
  });
  assert.strictEqual(validCore.failed.length, 0);

  const missingPrCore = createMockCore();
  assert.strictEqual(validatePullRequestContext({ payload: {} }, missingPrCore), null);
  assert.strictEqual(missingPrCore.failed.length, 1);

  const draftCore = createMockCore();
  assert.strictEqual(validatePullRequestContext(createPullRequestContext({ draft: true }), draftCore), null);
  assert.strictEqual(draftCore.failed.length, 0);

  const missingShaCore = createMockCore();
  assert.strictEqual(validatePullRequestContext(createPullRequestContext({ head: {}, base: {} }), missingShaCore), null);
  assert.strictEqual(missingShaCore.failed.length, 1);
});

test('verifyGitRefs handles falsy refStatus, failed status, thrown error, and success', () => {
  const { verifyGitRefs } = incrementalApproval;
  const approvedSha = '1111111111111111111111111111111111111111';
  const currentSha = '2222222222222222222222222222222222222222';

  const nullCore = createMockCore();
  assert.strictEqual(verifyGitRefs({ core: nullCore, baseRef: 'main', approvedSha, currentSha, ensureRefs: () => null }), false);
  assert.match(nullCore.warnings[0], /no longer reachable on remote/);

  const failCore = createMockCore();
  assert.strictEqual(verifyGitRefs({ core: failCore, baseRef: 'main', approvedSha, currentSha, ensureRefs: () => ({ success: false }) }), false);
  assert.match(failCore.warnings[0], /no longer reachable on remote/);

  const throwCore = createMockCore();
  assert.strictEqual(verifyGitRefs({ core: throwCore, baseRef: 'main', approvedSha, currentSha, ensureRefs: () => { throw new Error('network error'); } }), false);
  assert.match(throwCore.warnings[0], /Failed to verify git references \(network error\)/);

  const okCore = createMockCore();
  assert.strictEqual(verifyGitRefs({ core: okCore, baseRef: 'main', approvedSha, currentSha, ensureRefs: () => ({ success: true }) }), true);
  assert.strictEqual(okCore.warnings.length, 0);
});

test('resolveAuthorOwnership handles missing OWNERS, owner match, and non-owner', async () => {
  const { resolveAuthorOwnership } = incrementalApproval;
  await withPatchedIo({ existsSync: () => false }, () => {
    const core = createMockCore();
    assert.strictEqual(resolveAuthorOwnership(core, 'alice'), null);
    assert.strictEqual(core.failed.length, 1);
    assert.match(core.failed[0], /OWNERS file not found/);
  });

  await withPatchedIo({ existsSync: (p) => p === 'OWNERS', readFileSync: () => 'alice\nbob\n' }, () => {
    const ownerCore = createMockCore();
    assert.strictEqual(resolveAuthorOwnership(ownerCore, 'alice'), true);
    assert.strictEqual(ownerCore.failed.length, 0);

    const nonOwnerCore = createMockCore();
    assert.strictEqual(resolveAuthorOwnership(nonOwnerCore, 'charlie'), false);
    assert.strictEqual(nonOwnerCore.failed.length, 0);
  });
});

test('computeDeltaSummary warns and returns null when runGitRangeDiff throws', async () => {
  const { computeDeltaSummary } = incrementalApproval;
  const core = createMockCore();
  await withPatchedIo({
    execFileSync: (cmd, args) => {
      if (cmd === 'git' && args[0] === 'diff' && args[1] === '--name-only') return 'pkg/main.go\n';
      if (cmd === 'git' && args[0] === 'merge-base') return '';
      if (cmd === 'git' && args[0] === 'range-diff') throw new Error('range-diff failed');
      throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
    }
  }, () => {
    const result = computeDeltaSummary({
      core, prNumber: 123, baseRef: 'main',
      approvedSha: '1111111111111111111111111111111111111111',
      currentSha: '2222222222222222222222222222222222222222'
    });
    assert.strictEqual(result, null);
    assert.strictEqual(core.warnings.length, 1);
    assert.match(core.warnings[0], /Unable to compute git range-diff/);
  });
});

test('applyIncrementalDecision skips createReview on rejection and calls setFailed when createReview rejects', async () => {
  const { applyIncrementalDecision } = incrementalApproval;
  let createCalls = 0;
  const baseArgs = {
    core: createMockCore(), owner: 'google', repo: 'xmc', prNumber: 123,
    currentSha: '2222222222222222222222222222222222222222',
    latestApproval: { approver: 'bob', approvedSha: '1111111111111111111111111111111111111111', isDismissed: false }
  };

  await applyIncrementalDecision({
    ...baseArgs,
    github: createMockGithub({ onCreateReview: async () => { createCalls++; } }),
    isAuthorOwner: false,
    deltaSummary: { totalDelta: 5, testDelta: 0, isCleanRebase: false, hasRemovedCommits: false, hasProtectedFileChanges: false }
  });
  assert.strictEqual(createCalls, 0);
  assert.ok(baseArgs.core.infos.some(m => m.includes('Non-owner author pushed 5 changed lines')));

  const failCore = createMockCore();
  await applyIncrementalDecision({
    ...baseArgs,
    core: failCore,
    github: createMockGithub({ onCreateReview: async () => { throw new Error('422 Unprocessable Entity'); } }),
    isAuthorOwner: true,
    deltaSummary: { totalDelta: 0, testDelta: 0, isCleanRebase: true, hasRemovedCommits: false, hasProtectedFileChanges: false }
  });
  assert.strictEqual(failCore.failed.length, 1);
  assert.match(failCore.failed[0], /Failed to submit re-approval review for PR #123: 422 Unprocessable Entity/);
});

test('run aborts cleanly when OWNERS file is missing', async () => {
  const { core, createdReview } = await runWithGitMock({}, { ownersExists: false });
  assert.strictEqual(createdReview, null);
  assert.strictEqual(core.failed.length, 1);
  assert.match(core.failed[0], /OWNERS file not found/);
});

test('run rejects re-approval for protected files (diff or added commit), removed commits, and exceeded grace', async () => {
  const diffProtected = await runWithGitMock({
    diffFiles: '.github/workflows/lint.yml\n',
    rangeDiff: '1:  1111111 ! 1:  2222222 update workflow\n    @@ .github/workflows/lint.yml\n    + +  - run: echo hi\n'
  });
  assert.strictEqual(diffProtected.core.failed.length, 0);
  assert.strictEqual(diffProtected.createdReview, null);
  assert.ok(diffProtected.core.infos.some(m => m.includes('Manual human review is required')));

  const addedProtected = await runWithGitMock({
    rangeDiff: '-:  ------- > 1:  3333333 add owners\n',
    diffTreeFiles: 'OWNERS\n',
    diffTreeNumstat: '1\t0\tOWNERS\n'
  }, { prOverrides: { head: { sha: '3333333333333333333333333333333333333333' } } });
  assert.strictEqual(addedProtected.createdReview, null);
  assert.ok(addedProtected.core.infos.some(m => m.includes('Newly added commit modifies sensitive files')));
  assert.ok(addedProtected.core.infos.some(m => m.includes('Manual human review is required')));

  const removed = await runWithGitMock({
    mergeBaseOk: false,
    rangeDiff: '1:  1111111 < -:  ------- dropped commit\n'
  });
  assert.strictEqual(removed.createdReview, null);
  assert.ok(removed.core.infos.some(m => m.includes('removed or rewritten commits')));

  const graceExceeded = await runWithGitMock({
    rangeDiff: '-:  ------- > 1:  3333333 large change\n',
    diffTreeNumstat: '55\t0\tpkg/main.go\n'
  }, { prOverrides: { head: { sha: '3333333333333333333333333333333333333333' } } });
  assert.strictEqual(graceExceeded.createdReview, null);
  assert.ok(graceExceeded.core.infos.some(m => m.includes('exceed the 50-line grace delta')));
});

test('run auto-reapproves code owner when test lines push totalDelta over 50 while non-test delta is within budget', async () => {
  const { core, createdReview } = await runWithGitMock({
    diffFiles: 'pkg/main.go\npkg/main_test.go\n',
    rangeDiff: '-:  ------- > 1:  3333333 add tests and small fix\n',
    diffTreeFiles: 'pkg/main.go\npkg/main_test.go\n',
    diffTreeNumstat: '20\t0\tpkg/main.go\n60\t0\tpkg/main_test.go\n'
  }, { prOverrides: { head: { sha: '3333333333333333333333333333333333333333' } } });
  assert.strictEqual(core.failed.length, 0);
  assert.ok(createdReview);
  assert.strictEqual(createdReview.event, 'APPROVE');
  assert.match(createdReview.body, /20 lines, excluding 60 test file lines/);
});

test('run calls core.setFailed when createReview rejects', async () => {
  const { core } = await runWithGitMock({ diffFiles: 'tools/dev_env/runner.py\n' }, {
    onCreateReview: async () => { throw new Error('API 500'); }
  });
  assert.strictEqual(core.failed.length, 1);
  assert.match(core.failed[0], /Failed to submit re-approval review for PR #123: API 500/);
});
