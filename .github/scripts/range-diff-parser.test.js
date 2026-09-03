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
  isIgnoredRangeDiffHeader,
  parseRangeDiff
} = require('./range-diff-parser');

test('parseRangeDiff identifies pure rebase with zero delta', () => {
  const sampleOutput = [
    '1:  a1b2c3d = 1:  e5f6g7h First commit',
    '2:  1234567 = 2:  89abcdef Second commit'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, true);
  assert.strictEqual(result.hasRemovedCommits, false);
  assert.strictEqual(result.totalDelta, 0);
  assert.strictEqual(result.additions, 0);
  assert.strictEqual(result.deletions, 0);
});

test('parseRangeDiff ignores unchanged commit additions and deletions in Column 2', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h First commit',
    '    @@ src/main.go:10,5 +10,5 @@',
    '     -unchangedOriginalCode()',
    '     +unchangedReplacementCode()',
    '    -+removedInV2()',
    '    ++addedInV2()'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, false);
  assert.strictEqual(result.hasRemovedCommits, false);
  assert.strictEqual(result.additions, 1);
  assert.strictEqual(result.deletions, 1);
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

test('parseRangeDiff identifies added commits and captures addedCommitShas', () => {
  const sampleAdded = [
    '1:  a1b2c3d = 1:  e5f6g7h First commit',
    '-:  ------- > 2:  89abcdef Added new commit'
  ].join('\n');

  const resultAdded = parseRangeDiff(sampleAdded);
  assert.strictEqual(resultAdded.isCleanRebase, false);
  assert.strictEqual(resultAdded.hasRemovedCommits, false);
  assert.deepStrictEqual(resultAdded.addedCommitShas, ['89abcdef']);
  assert.strictEqual(resultAdded.totalDelta, 0);

  const sampleRemoved = [
    '1:  a1b2c3d < -:  ------- Removed commit'
  ].join('\n');

  const resultRemoved = parseRangeDiff(sampleRemoved);
  assert.strictEqual(resultRemoved.isCleanRebase, false);
  assert.strictEqual(resultRemoved.hasRemovedCommits, true);
  assert.deepStrictEqual(resultRemoved.addedCommitShas, []);
});

test('isIgnoredRangeDiffHeader detects metadata and header lines', () => {
  assert.strictEqual(isIgnoredRangeDiffHeader('@@ Metadata'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('Author: Alice <alice@google.com>'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('Commit: Initial commit'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('## Commit message ##'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('## Notes (feature-branch) ##'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('## src/main.go ##'), false);
  assert.strictEqual(isIgnoredRangeDiffHeader('@@ -1,3 +1,4 @@'), false);
  assert.strictEqual(isIgnoredRangeDiffHeader('+validCodeLine()'), false);
});
