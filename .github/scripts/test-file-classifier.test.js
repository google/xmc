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
const { isTestFile } = require('./test-file-classifier');

test('isTestFile recognizes test files and fixtures', () => {
  for (const path of [
    'pkg/workunit/create_test.go',
    'web/src/app/app.component.spec.ts',
    '.github/scripts/approval-policy.test.js',
    'ui/widget.test.tsx',
    'python/xm/test_launcher.py',
    'python/xm/launcher_test.py',
    'pkg/api/testdata/golden.json',
    'testdata/fixture.yaml',
    'pkg\\api\\handler_test.go',
    '"pkg/workunit/create_test.go"',
    '"pkg/\\303\\251t\\303\\251_test.go"'
  ]) {
    assert.strictEqual(isTestFile(path), true, path);
  }
});

test('isTestFile never treats git backslash escapes as directory separators', () => {
  // Git quotes and C-escapes a production file named `testdata<TAB>prod.go`.
  assert.strictEqual(isTestFile('"testdata\\tprod.go"'), false);
  assert.strictEqual(isTestFile('testdata\\prod.go'), false);
  assert.strictEqual(isTestFile('"pkg\\\\testdata\\\\prod.go"'), false);
});

test('isTestFile rejects production files and invalid input', () => {
  for (const path of [
    'pkg/workunit/create.go',
    'pkg/testing/fake.go',
    'web/src/app/app.component.ts',
    'python/xm/testing.py',
    'pkg/latest_testdata.go',
    '',
    null,
    undefined,
    42
  ]) {
    assert.strictEqual(isTestFile(path), false, String(path));
  }
});
