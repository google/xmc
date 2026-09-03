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
  buildGitEnv,
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
  computeAddedCommitsDelta,
  getPrChangedFiles,
  getCommitModifiedFiles
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
      assert.ok(args.includes('--no-commit-id'), 'should pass --no-commit-id to diff-tree');
      return '3\t1\t.github/workflows/unit-tests.yml\n1\t0\tREADME.md\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const delta = getCommitLineDelta('94d64508');
    assert.strictEqual(delta.additions, 4);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 5);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('getCommitLineDelta throws on subprocess failure to fail-safe', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = () => {
    throw new Error('Fatal git error');
  };

  try {
    assert.throws(
      () => getCommitLineDelta('94d64508'),
      /Failed to get line delta for commit 94d64508:.*Fatal git error/
    );
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('getCommitLineDelta validates commit SHA before spawning subprocess', () => {
  let execCalled = false;
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = () => {
    execCalled = true;
    return '';
  };

  try {
    const invalidShaDelta = getCommitLineDelta('invalid-sha');
    assert.strictEqual(execCalled, false);
    assert.deepStrictEqual(invalidShaDelta, { additions: 0, deletions: 0, totalDelta: 0 });

    const nullDelta = getCommitLineDelta(null);
    assert.strictEqual(execCalled, false);
    assert.deepStrictEqual(nullDelta, { additions: 0, deletions: 0, totalDelta: 0 });

    const emptyDelta = getCommitLineDelta('');
    assert.strictEqual(execCalled, false);
    assert.deepStrictEqual(emptyDelta, { additions: 0, deletions: 0, totalDelta: 0 });
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('computeAddedCommitsDelta sums additions and deletions across multiple commits', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree') {
      const sha = args[args.length - 1];
      if (sha === '1111111') {
        return '1111111\n2\t1\tfile1.go\n';
      }
      if (sha === '2222222') {
        return '2222222\n3\t0\tfile2.go\n';
      }
    }
    throw new Error(`Unexpected command: ${cmd}`);
  };

  try {
    const delta = computeAddedCommitsDelta(['1111111', '2222222']);
    assert.strictEqual(delta.additions, 5);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 6);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('computeAddedCommitsDelta handles non-array input defensively', () => {
  assert.deepStrictEqual(computeAddedCommitsDelta(null), { additions: 0, deletions: 0, totalDelta: 0 });
  assert.deepStrictEqual(computeAddedCommitsDelta(undefined), { additions: 0, deletions: 0, totalDelta: 0 });
  assert.deepStrictEqual(computeAddedCommitsDelta('not-an-array'), { additions: 0, deletions: 0, totalDelta: 0 });
  assert.deepStrictEqual(computeAddedCommitsDelta({}), { additions: 0, deletions: 0, totalDelta: 0 });
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

  try {
    const res = resolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
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

  try {
    const res = resolveCommitRanges('feat/base', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/feat/base..head2');
    assert.strictEqual(res.approvedRange, 'head1~6..head1');
  } finally {
    childProcess.execFileSync = originalExec;
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

  try {
    const res = resolveCommitRanges('main', 'head1', 'head2', {});
    assert.strictEqual(res.currentRange, 'origin/main..head2');
    assert.strictEqual(res.approvedRange, 'origin/main..head1');
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('runGitRangeDiff executes range-diff without usage errors', () => {
  const headSha = childProcess.execSync('git rev-parse HEAD', { encoding: 'utf8' }).trim();
  const output = runGitRangeDiff('main', headSha, headSha);
  assert.strictEqual(typeof output, 'string');
  assert.strictEqual(output, '');
});

test('resolveCommitRanges enforces encoding and stdio precedence over execOptions', () => {
  const originalExec = childProcess.execFileSync;
  let capturedOpts = null;

  childProcess.execFileSync = (cmd, args, opts) => {
    capturedOpts = opts;
    return '0\n';
  };

  try {
    resolveCommitRanges('main', '1111111', '2222222', { encoding: 'buffer', stdio: 'inherit', extra: true });
    assert.strictEqual(capturedOpts.encoding, 'utf8');
    assert.deepStrictEqual(capturedOpts.stdio, ['pipe', 'pipe', 'ignore']);
    assert.strictEqual(capturedOpts.extra, true);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('getCommitLineDelta enforces encoding and stdio precedence over execOptions', () => {
  const originalExec = childProcess.execFileSync;
  let capturedOpts = null;

  childProcess.execFileSync = (cmd, args, opts) => {
    capturedOpts = opts;
    return '1111111\n1\t0\tfile.go\n';
  };

  try {
    getCommitLineDelta('1111111', { encoding: 'buffer', stdio: 'inherit', extra: true });
    assert.strictEqual(capturedOpts.encoding, 'utf8');
    assert.deepStrictEqual(capturedOpts.stdio, ['pipe', 'pipe', 'ignore']);
    assert.strictEqual(capturedOpts.extra, true);
  } finally {
    childProcess.execFileSync = originalExec;
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

test('ensureGitRefs skips fetching approved commit if it became present after base fetch', () => {
  const originalExec = childProcess.execFileSync;
  const fetched = [];
  let baseFetched = false;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'rev-parse') {
      if (args[3] === 'c40cf0f') return 'ok\n';
      if (args[3] === '8fa2009' && baseFetched) return 'ok\n';
      const err = new Error('missing ref');
      err.status = 1;
      throw err;
    }
    if (cmd === 'git' && args[0] === 'fetch') {
      fetched.push(args.slice(1));
      baseFetched = true;
      return '';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { ensureGitRefs: mockedEnsure } = require('./git-range-diff');

  try {
    const res = mockedEnsure('feat/layer', '8fa2009', 'c40cf0f');
    assert.strictEqual(res.success, true);
    assert.strictEqual(fetched.length, 1);
    assert.deepStrictEqual(fetched[0], ['origin', '+refs/heads/feat/layer:refs/remotes/origin/feat/layer']);
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

test('execGit handles explicitly passed null options without throwing TypeError', () => {
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = () => 'ok\n';

  delete require.cache[require.resolve('./git-range-diff')];
  const { execGit: mockedExecGit } = require('./git-range-diff');

  try {
    const result = mockedExecGit(['status'], null);
    assert.strictEqual(result, 'ok\n');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('buildGitEnv constructs non-interactive environment and basic auth header', () => {
  const originalToken = process.env.GITHUB_TOKEN;
  try {
    process.env.GITHUB_TOKEN = 'secret-token';
    const env = buildGitEnv({ CUSTOM_VAR: '1' });
    assert.strictEqual(env.GIT_TERMINAL_PROMPT, '0');
    assert.strictEqual(env.GIT_OPTIONAL_LOCKS, '0');
    assert.strictEqual(env.CUSTOM_VAR, '1');
    assert.strictEqual(env.GIT_CONFIG_COUNT, '1');
    assert.strictEqual(env.GIT_CONFIG_KEY_0, 'http.extraheader');
    assert.ok(env.GIT_CONFIG_VALUE_0.includes('Authorization: Basic '));
  } finally {
    if (originalToken !== undefined) {
      process.env.GITHUB_TOKEN = originalToken;
    } else {
      delete process.env.GITHUB_TOKEN;
    }
  }
});

test('getPrChangedFiles returns files from 3-dot diff and includes follow-up commits if ancestor', () => {
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff' && args[2] === 'origin/main...headSha') {
      return 'file1.go\nfile2.go\n';
    }
    if (cmd === 'git' && args[0] === 'merge-base' && args[1] === '--is-ancestor') {
      return '';
    }
    if (cmd === 'git' && args[0] === 'diff' && args[2] === 'approvedSha' && args[3] === 'headSha') {
      return 'file2.go\nfile3.go\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const files = getPrChangedFiles('main', 'approvedSha', 'headSha');
    assert.deepStrictEqual(files.sort(), ['file1.go', 'file2.go', 'file3.go']);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('getCommitModifiedFiles returns file list for commit from diff-tree', () => {
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'diff-tree' && args.includes('sha123')) {
      return 'pkg/auth.go\ncmd/main.go\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  try {
    const files = getCommitModifiedFiles('sha123');
    assert.deepStrictEqual(files, ['pkg/auth.go', 'cmd/main.go']);
  } finally {
    childProcess.execFileSync = originalExec;
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
    // Verify ignoreExitCode: true does not cause TypeError on .trim()
    assert.strictEqual(mockedCount('origin/main..HEAD', { ignoreExitCode: true }), 5);
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
      assert.ok(args.includes('--root'), 'should pass --root to diff-tree for root commit support');
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
    // Verify ignoreExitCode: true in opts does not cause TypeError
    assert.strictEqual(mockedPatchId('sha123', { ignoreExitCode: true }), '1122334455aabbcc');
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

test('indexApprovedCandidateCommits indexes patch-id and subject maps and limits to 30 commits', () => {
  const originalExec = childProcess.execFileSync;
  let logPatchCalls = 0;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'log' && args.includes('--format=%H %s')) {
      // Return 50 candidate commits
      const lines = [];
      for (let i = 1; i <= 50; i++) {
        lines.push(`sha${i} feat: commit ${i}`);
      }
      return lines.join('\n') + '\n';
    }
    if (cmd === 'git' && args[0] === 'log' && args.includes('-p') && args.includes('--no-walk')) {
      logPatchCalls++;
      assert.strictEqual(args.length - 3, 30, 'should pass exactly 30 SHAs to git log -p --no-walk');
      return 'diff';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return 'pid123 sha50\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { indexApprovedCandidateCommits: mockedIndex } = require('./git-range-diff');

  try {
    const maps = mockedIndex('origin/main..approvedSha', {});
    assert.strictEqual(logPatchCalls, 1, 'should spawn git log -p once for bulk patch-id computation');
    assert.strictEqual(maps.bySubject.has('feat: commit 1'), false, 'older commits beyond 30 should not be indexed');
    assert.strictEqual(maps.bySubject.has('feat: commit 50'), true, 'newest commits should be indexed');
    assert.deepStrictEqual(maps.bySubject.get('feat: commit 50'), ['sha50']);
    assert.strictEqual(maps.byPatchId.get('pid123'), 'sha50');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('correlateStackedParentBase matches commits using object and string formats without redundant git log', () => {
  const originalExec = childProcess.execFileSync;
  let logCalls = 0;
  let bulkLogCalls = 0;

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'log' && args.includes('-p') && args.includes('--no-walk')) {
      bulkLogCalls++;
      return 'diff';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return 'pid-nomatch sha\n';
    }
    if (cmd === 'git' && args[0] === 'log') {
      logCalls++;
      return 'feat: fallback subject\n';
    }
    if (cmd === 'git' && args[0] === 'rev-parse') {
      return 'matched-base-sha\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { correlateStackedParentBase: mockedCorrelate } = require('./git-range-diff');

  try {
    const approvedMaps = {
      byPatchId: new Map(),
      bySubject: new Map([['feat: my stacked commit', 'approved-sha-1']])
    };

    // When objects with { sha, subj } are provided, git log should NOT be invoked
    const objectResult = mockedCorrelate(
      [{ sha: 'curr-sha-1', subj: 'feat: my stacked commit' }],
      approvedMaps,
      {}
    );
    assert.strictEqual(objectResult, 'matched-base-sha');
    assert.strictEqual(logCalls, 0, 'git log should not be called when subj is provided in commit object');
    assert.strictEqual(bulkLogCalls, 1, 'should call bulk git log -p once');

    // Reset calls
    bulkLogCalls = 0;
    logCalls = 0;

    // When strings are provided, git log should be invoked as fallback
    const stringApprovedMaps = {
      byPatchId: new Map(),
      bySubject: new Map([['feat: fallback subject', 'approved-sha-2']])
    };
    const stringResult = mockedCorrelate(['curr-sha-2'], stringApprovedMaps, {});
    assert.strictEqual(stringResult, 'matched-base-sha');
    assert.strictEqual(logCalls, 1, 'git log should be called when commit is a string');
    assert.strictEqual(bulkLogCalls, 1, 'should call bulk git log -p once');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('correlateStackedParentBase resolves base commit via patch-id match', () => {
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'log' && args.includes('-p')) {
      return 'commit curr-sha-1\n diff content\n';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return 'pid-match-123 curr-sha-1\n';
    }
    if (cmd === 'git' && args[0] === 'rev-parse') {
      return 'resolved-base-sha\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { correlateStackedParentBase: mockedCorrelate } = require('./git-range-diff');

  try {
    const approvedMaps = {
      byPatchId: new Map([['pid-match-123', 'approved-parent-sha']]),
      bySubject: new Map()
    };
    const result = mockedCorrelate(['curr-sha-1'], approvedMaps, {});
    assert.strictEqual(result, 'resolved-base-sha');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('correlateStackedParentBase handles null or invalid arguments defensively', () => {
  const { correlateStackedParentBase: mockedCorrelate } = require('./git-range-diff');
  assert.strictEqual(mockedCorrelate(null, { byPatchId: new Map(), bySubject: new Map() }), null);
  assert.strictEqual(mockedCorrelate([], null), null);
  assert.strictEqual(mockedCorrelate([null], { byPatchId: new Map(), bySubject: new Map() }), null);
});

test('indexApprovedCandidateCommits handles duplicate commit subjects by preserving chronological order', () => {
  const originalExec = childProcess.execFileSync;
  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'log' && args.includes('--format=%H %s')) {
      return 'sha1 fix: typo\nsha2 fix: typo\nsha3 feat: feature\n';
    }
    if (cmd === 'git' && args[0] === 'log' && args.includes('-p')) {
      return '';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return '';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { indexApprovedCandidateCommits: mockedIndex } = require('./git-range-diff');

  try {
    const maps = mockedIndex('origin/main..HEAD', {});
    assert.deepStrictEqual(maps.bySubject.get('fix: typo'), ['sha1', 'sha2']);
    assert.deepStrictEqual(maps.bySubject.get('feat: feature'), ['sha3']);
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});

test('correlateStackedParentBase matches duplicate subjects in chronological order to compute correct baseOffset', () => {
  const originalExec = childProcess.execFileSync;
  const revParseTargets = [];

  childProcess.execFileSync = (cmd, args) => {
    if (cmd === 'git' && args[0] === 'log' && args.includes('-p') && args.includes('--no-walk')) {
      return 'diff';
    }
    if (cmd === 'git' && args[0] === 'patch-id') {
      return ''; // No patch-id match, forcing subject correlation
    }
    if (cmd === 'git' && args[0] === 'rev-parse') {
      revParseTargets.push(args[2]);
      return 'resolved-base-sha\n';
    }
    throw new Error(`Unexpected command: ${cmd} ${args.join(' ')}`);
  };

  delete require.cache[require.resolve('./git-range-diff')];
  const { correlateStackedParentBase: mockedCorrelate } = require('./git-range-diff');

  try {
    const approvedMaps = {
      byPatchId: new Map(),
      bySubject: new Map([
        ['fix: duplicate', ['approved-sha-1', 'approved-sha-2']]
      ])
    };

    // First commit with duplicate subject should match the first candidate (approved-sha-1)
    const result = mockedCorrelate(
      [
        { sha: 'curr-1', subj: 'fix: duplicate' },
        { sha: 'curr-2', subj: 'fix: duplicate' }
      ],
      approvedMaps,
      {}
    );

    assert.strictEqual(result, 'resolved-base-sha');
    // For i = 0 (first commit), baseOffset = 1, so target should be approved-sha-1~1 (NOT approved-sha-2~1)
    assert.strictEqual(revParseTargets[0], 'approved-sha-1~1');
  } finally {
    childProcess.execFileSync = originalExec;
    delete require.cache[require.resolve('./git-range-diff')];
  }
});
