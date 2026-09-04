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

