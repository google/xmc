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
  validateGitRefs,
  execGit,
  ensureGitRefs,
  isFastForwardApproval,
  getCommitCountInRange,
  getCommitPatchId,
  indexApprovedCandidateCommits,
  correlateStackedParentBase,
  resolveStackedApprovedRange,
  resolveCommitRanges,
  runGitRangeDiff,
  getCommitLineDelta,
  computeAddedCommitsDelta
} = require('./git-range-diff');

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

test('getCommitLineDelta parses numstat additions and deletions correctly', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      return '94d6450805df211f728adf73e0c98b8419e22005\n3\t1\t.github/workflows/unit-tests.yml\n1\t0\tREADME.md\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitLineDelta: mockedGetCommitLineDelta } = require('./git-range-diff');

  try {
    const delta = mockedGetCommitLineDelta('94d64508');
    assert.strictEqual(delta.additions, 4);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 5);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('getCommitLineDelta handles subprocess failure gracefully', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = () => {
    throw new Error('Fatal git error');
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitLineDelta: mockedGetCommitLineDelta } = require('./git-range-diff');

  try {
    const delta = mockedGetCommitLineDelta('invalid-sha');
    assert.strictEqual(delta.additions, 0);
    assert.strictEqual(delta.deletions, 0);
    assert.strictEqual(delta.totalDelta, 0);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('computeAddedCommitsDelta sums additions and deletions across multiple commits', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      const sha = args[args.length - 1];
      if (sha === 'sha-1') {
        return 'sha-1\n2\t1\tfile1.go\n';
      }
      if (sha === 'sha-2') {
        return 'sha-2\n3\t0\tfile2.go\n';
      }
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { computeAddedCommitsDelta: mockedComputeAddedCommitsDelta } = require('./git-range-diff');

  try {
    const delta = mockedComputeAddedCommitsDelta(['sha-1', 'sha-2']);
    assert.strictEqual(delta.additions, 5);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 6);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('resolveCommitRanges uses standard ranges when commit count matches', () => {
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

  delete require.cache[require.resolve('./git-range-diff')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./git-range-diff');

  try {
    const res = mockedResolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('resolveCommitRanges scopes approved range when parent branch was rebased in stacked PRs', () => {
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

  delete require.cache[require.resolve('./git-range-diff')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./git-range-diff');

  try {
    const res = mockedResolveCommitRanges('feat/base', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/feat/base..head2');
    assert.strictEqual(res.approvedRange, 'head1~6..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('resolveCommitRanges preserves standard range when developer adds new commits to regular PR', () => {
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

  delete require.cache[require.resolve('./git-range-diff')];
  const { resolveCommitRanges: mockedResolveCommitRanges } = require('./git-range-diff');

  try {
    const res = mockedResolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('runGitRangeDiff returns empty string when approvedSha equals currentSha', () => {
  const result = runGitRangeDiff('main', 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e', 'cfe1eb7d59fe72ac5e7c61b237a6c82bc791895e');
  assert.strictEqual(result, '');
});

test('ensureGitRefs returns success when all refs exist locally', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse' && args[1] === '--verify') {
      return 'ok\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { ensureGitRefs: mockedEnsure } = require('./git-range-diff');

  try {
    const res = mockedEnsure('main', '8fa2009', 'c40cf0f');
    assert.strictEqual(res.success, true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('ensureGitRefs fetches missing base and approved commits', () => {
  const originalExec = childProcess.execFileSync;
  const fetched = [];

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      if (args[3] === 'c40cf0f') return 'ok\n';
      const err = new Error('missing ref');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'fetch') {
      fetched.push(args.slice(1));
      return '';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { ensureGitRefs: mockedEnsure } = require('./git-range-diff');

  try {
    const res = mockedEnsure('feat/layer', '8fa2009', 'c40cf0f');
    assert.strictEqual(res.success, true);
    assert.strictEqual(fetched.length, 2);
    assert.deepStrictEqual(fetched[0], ['origin', '+refs/heads/feat/layer:refs/remotes/origin/feat/layer']);
    assert.deepStrictEqual(fetched[1], ['origin', '8fa2009']);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('ensureGitRefs gracefully returns failure when approved commit is GCed on remote', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      if (args[3] === 'refs/remotes/origin/main' || args[3] === 'c40cf0f') return 'ok\n';
      const err = new Error('missing ref');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'fetch' && args[2] === '8fa2009') {
      const err = new Error("fatal: couldn't find remote ref 8fa2009");
      err.status = 128;
      err.stderr = Buffer.from("fatal: couldn't find remote ref 8fa2009");
      throw err;
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { ensureGitRefs: mockedEnsure } = require('./git-range-diff');

  try {
    const res = mockedEnsure('main', '8fa2009', 'c40cf0f');
    assert.strictEqual(res.success, false);
    assert.strictEqual(res.reason, 'APPROVED_SHA_UNREACHABLE');
    assert.strictEqual(res.unreachableSha, '8fa2009');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('execGit passes GITHUB_TOKEN auth header and terminal security defaults', () => {
  const originalExec = childProcess.execFileSync;
  let capturedOptions = null;

  childProcess.execFileSync = (cmd, args, opts) => {
    capturedOptions = opts;
    return 'ok\n';
  };

  delete require.cache[require.resolve('./git-range-diff')];
  process.env.GITHUB_TOKEN = 'secret-test-token';
  const { execGit: mockedExecGit } = require('./git-range-diff');

  try {
    mockedExecGit(['status']);
    assert.ok(capturedOptions);
    assert.strictEqual(capturedOptions.env.GIT_TERMINAL_PROMPT, '0');
    assert.strictEqual(capturedOptions.env.GIT_OPTIONAL_LOCKS, '0');
    assert.strictEqual(capturedOptions.env.GIT_CONFIG_COUNT, '1');
    assert.strictEqual(capturedOptions.env.GIT_CONFIG_KEY_0, 'http.extraheader');
    assert.ok(capturedOptions.env.GIT_CONFIG_VALUE_0.startsWith('Authorization: Basic '));
  } finally {
    delete process.env.GITHUB_TOKEN;
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('isFastForwardApproval detects ancestry correctly', () => {
  const originalExec = childProcess.execFileSync;

  // Fast-forward case
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'merge-base' && args[1] === '--is-ancestor') {
      return '';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { isFastForwardApproval: mockedFF } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedFF('sha1', 'sha2', {}), true);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }

  // Non-fast-forward case
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'merge-base' && args[1] === '--is-ancestor') {
      const err = new Error('not ancestor');
      err.status = 1;
      throw err;
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { isFastForwardApproval: mockedFF2 } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedFF2('sha1', 'sha2', {}), false);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('getCommitCountInRange parses count and handles errors', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-list' && args[1] === '--count') {
      return ' 5 \n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitCountInRange: mockedCount } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedCount('origin/main..HEAD', {}), 5);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }

  childProcess.execFileSync = () => {
    throw new Error('git rev-list failure');
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitCountInRange: mockedCount2 } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedCount2('origin/main..HEAD', {}), 0);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('getCommitPatchId parses patch-id and handles errors', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      return 'patch content';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return '1122334455aabbcc commit-sha\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitPatchId: mockedPatchId } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedPatchId('sha123', {}), '1122334455aabbcc');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }

  childProcess.execFileSync = () => {
    throw new Error('git failure');
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { getCommitPatchId: mockedPatchId2 } = require('./git-range-diff');

  try {
    assert.strictEqual(mockedPatchId2('sha123', {}), null);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('indexApprovedCandidateCommits indexes patch-id and subject maps', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-list') {
      return 'sha1\nsha2\n';
    }
    if (cmd === 'git' && args[0] === 'diff-tree') {
      return 'diff';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return 'pid123 sha\n';
    }
    if (cmd === 'git' && args[0] === 'log') {
      return 'feat: commit title\n';
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { indexApprovedCandidateCommits: mockedIndex } = require('./git-range-diff');

  try {
    const maps = mockedIndex('origin/main..approvedSha', {});
    assert.strictEqual(maps.byPatchId.get('pid123'), 'sha2');
    assert.strictEqual(maps.bySubject.get('feat: commit title'), 'sha2');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});
