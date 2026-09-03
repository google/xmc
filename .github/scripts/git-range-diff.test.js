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

  try {
    const delta = getCommitLineDelta('94d64508');
    assert.strictEqual(delta.additions, 4);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 5);
  } finally {
    childProcess.execFileSync = originalExec;
  }
});

test('getCommitLineDelta handles subprocess failure gracefully', () => {
  const originalExec = childProcess.execFileSync;

  childProcess.execFileSync = () => {
    throw new Error('Fatal git error');
  };

  try {
    const delta = getCommitLineDelta('invalid-sha');
    assert.strictEqual(delta.additions, 0);
    assert.strictEqual(delta.deletions, 0);
    assert.strictEqual(delta.totalDelta, 0);
  } finally {
    childProcess.execFileSync = originalExec;
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

  try {
    const delta = computeAddedCommitsDelta(['sha-1', 'sha-2']);
    assert.strictEqual(delta.additions, 5);
    assert.strictEqual(delta.deletions, 1);
    assert.strictEqual(delta.totalDelta, 6);
  } finally {
    childProcess.execFileSync = originalExec;
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
