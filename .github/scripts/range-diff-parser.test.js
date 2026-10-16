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
  extractRangeDiffFilePath,
  isIgnoredRangeDiffHeader,
  parseRangeDiff
} = require('./range-diff-parser');

test('extractRangeDiffFilePath parses outer hunk and inner file headers', () => {
  assert.strictEqual(extractRangeDiffFilePath('@@ pkg/a_test.go (new)'), 'pkg/a_test.go');
  assert.strictEqual(extractRangeDiffFilePath('@@ pkg/b.go: func Foo() {'), 'pkg/b.go');
  assert.strictEqual(extractRangeDiffFilePath('@@ pkg/b.go:'), 'pkg/b.go');
  assert.strictEqual(extractRangeDiffFilePath('## pkg/new_test.go (new) ##'), 'pkg/new_test.go');
  assert.strictEqual(extractRangeDiffFilePath('## pkg/old.go => pkg/old_test.go ##'), 'pkg/old.go');
  assert.strictEqual(extractRangeDiffFilePath('## pkg/a_test.go => pkg/b_test.go ##'), 'pkg/b_test.go');
  assert.strictEqual(extractRangeDiffFilePath('## pkg/c.go ##'), 'pkg/c.go');
  assert.strictEqual(extractRangeDiffFilePath('@@'), null);
  assert.strictEqual(extractRangeDiffFilePath('@@ -1,3 +1,4 @@'), null);
  assert.strictEqual(extractRangeDiffFilePath('plain text'), null);
});

test('parseRangeDiff counts test file lines separately from production lines', () => {
  // Captured from real `git range-diff` output.
  const sampleOutput = [
    '1:  d1d6891 ! 1:  68a818d c1',
    '    @@ p/a_test.go (new)',
    '     +4',
    '    -+5',
    '    ++FIVE',
    '     +6',
    '    @@ p/b.go (new)',
    '     +4',
    '    -+5',
    '    ++FIVE',
    '     +6',
    '    @@ p/b.go (new)',
    '     +30',
    '     ',
    '    - ## p/old.go (new) ##',
    '    + ## p/new_test.go (new) ##',
    '    +@@',
    '    ++1',
    '    ++2',
    '    ++3',
    '    +',
    '    + ## p/renamed.go (new) ##',
    '     @@',
    '     +1',
    '    ++extra'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  // a_test.go: 2, b.go: 2, new_test.go: 3 lines + blank = 4 ('+@@' header skipped), renamed.go: 1.
  assert.strictEqual(result.totalDelta, 9);
  assert.strictEqual(result.testDelta, 6);
});

test('parseRangeDiff treats lines with unknown file as non-test', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit',
    '    @@',
    '    -+old',
    '    ++new'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.totalDelta, 2);
  assert.strictEqual(result.testDelta, 0);
});

test('parseRangeDiff resets current file across commits', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h First',
    '    @@ pkg/a_test.go: func TestA(t *testing.T) {',
    '    ++x',
    '2:  1234567 ! 2:  89abcde Second',
    '    @@',
    '    ++y'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.totalDelta, 2);
  assert.strictEqual(result.testDelta, 1);
});

test('parseRangeDiff ignores forged file headers in added code lines', () => {
  // Captured from real `git range-diff`: an added prod line whose text is `## a_test.go ##`.
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit',
    '    @@ p/prod.py (new)',
    '     +1',
    '    ++## a_test.go ##',
    '    ++@@ fake',
    '    ++EVIL1',
    '    ++EVIL2'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.totalDelta, 4);
  assert.strictEqual(result.testDelta, 0);
});

test('parseRangeDiff ignores forged section headers in context lines', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit',
    '    @@ p/prod.go: func Foo() {',
    '    + ## a_test.go ##',
    '    +@@',
    '    ++EVIL1',
    '      ## Commit message ##',
    '    ++EVIL2'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  // Forged lines are counted as code: the added context line + 2 EVIL lines.
  assert.strictEqual(result.totalDelta, 3);
  assert.strictEqual(result.testDelta, 0);
});

test('parseRangeDiff drops an unconfirmed file header to unknown (non-test)', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit',
    '    @@ p/a_test.go (new)',
    '     ',
    '    + ## p/b_test.go (new) ##',
    '    ++line'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  // Only `++line` counts; the unconfirmed header leaves the file unknown.
  assert.strictEqual(result.totalDelta, 1);
  assert.strictEqual(result.testDelta, 0);
});

test('parseRangeDiff resets file on outer header without a path', () => {
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit',
    '    @@ p/a_test.go (new)',
    '    ++x',
    '    @@',
    '    ++y'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.totalDelta, 2);
  assert.strictEqual(result.testDelta, 1);
});

test('parseRangeDiff skips metadata and commit message sections', () => {
  // Captured from real `git range-diff` with author and message changes.
  const sampleOutput = [
    '1:  d1d6891 ! 1:  2daeff1 c1',
    '    @@',
    '      ## Metadata ##',
    '    -Author: A <a@example.com>',
    '    +Author: B <a@example.com>',
    '     ',
    '      ## Commit message ##',
    '    -    c1',
    '    +    c1 changed msg',
    '     ',
    '      ## p/a_test.go (new) ##',
    '     @@',
    '    @@ p/a_test.go (new)',
    '    -+5',
    '    ++FIVE'
  ].join('\n');
  const result = parseRangeDiff(sampleOutput);
  // The leading `## Metadata ##` follows an outer header, so it is not trusted and the
  // author lines count conservatively (2); the commit message section is skipped.
  assert.strictEqual(result.totalDelta, 4);
  assert.strictEqual(result.testDelta, 2);
});

test('parseRangeDiff handles empty or invalid output safely', () => {
  for (const emptyInput of ['', '   ', null, undefined]) {
    const result = parseRangeDiff(emptyInput);
    assert.strictEqual(result.isCleanRebase, true);
    assert.strictEqual(result.hasRemovedCommits, false);
    assert.strictEqual(result.totalDelta, 0);
    assert.strictEqual(result.additions, 0);
    assert.strictEqual(result.deletions, 0);
    assert.deepStrictEqual(result.addedCommitShas, []);
    assert.strictEqual('hasUnmatchedCommits' in result, false);
  }
});

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
  // Column 1 is ' ', Column 2 is '+' or '-' -> Unchanged in this revision
  const sampleOutput = [
    '1:  a1b2c3d ! 1:  e5f6g7h Commit title',
    '    @@ src/main.go:10,5 @@',
    '      context line',
    '     +existing addition from v1',
    '     -existing deletion from v1',
    '    ++new addition in v2',
    '    -+removed addition in v2'
  ].join('\n');

  const result = parseRangeDiff(sampleOutput);
  assert.strictEqual(result.isCleanRebase, false);
  assert.strictEqual(result.additions, 1); // Only ++ is added in v2
  assert.strictEqual(result.deletions, 1); // Only -+ is removed in v2
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
  assert.strictEqual(isIgnoredRangeDiffHeader('Commit: Bob <bob@google.com>'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('## Commit message ##'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('## Notes (main) ##'), true);
  assert.strictEqual(isIgnoredRangeDiffHeader('func main() {'), false);
  assert.strictEqual(isIgnoredRangeDiffHeader('## pkg/foo/bar.go ##'), false);
});
