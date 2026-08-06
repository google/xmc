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
  isOnlyAbsenceFileModified,
  isAuthorCodeOwner,
  shouldAutoApproveAbsencePR
} = require('./auto-approve-absence');

test('isOnlyAbsenceFileModified returns true when strictly ABSENCE.md is modified', () => {
  assert.strictEqual(isOnlyAbsenceFileModified(['ABSENCE.md']), true);
});

test('isOnlyAbsenceFileModified returns false when other files or multiple files modified', () => {
  assert.strictEqual(isOnlyAbsenceFileModified(['ABSENCE.md', 'OWNERS']), false);
  assert.strictEqual(isOnlyAbsenceFileModified(['README.md']), false);
  assert.strictEqual(isOnlyAbsenceFileModified([]), false);
  assert.strictEqual(isOnlyAbsenceFileModified(null), false);
});

test('isAuthorCodeOwner checks code owners list case-insensitively', () => {
  const owners = ['alice', 'bob', 'charlie'];
  assert.strictEqual(isAuthorCodeOwner('alice', owners), true);
  assert.strictEqual(isAuthorCodeOwner('ALICE', owners), true);
  assert.strictEqual(isAuthorCodeOwner('dave', owners), false);
  assert.strictEqual(isAuthorCodeOwner('', owners), false);
  assert.strictEqual(isAuthorCodeOwner('alice', null), false);
});

test('shouldAutoApproveAbsencePR approves valid ABSENCE.md PR by owner', () => {
  const owners = ['alice', 'bob'];
  const decision = shouldAutoApproveAbsencePR({
    modifiedFileNames: ['ABSENCE.md'],
    prAuthor: 'alice',
    ownersList: owners
  });

  assert.strictEqual(decision.canApprove, true);
  assert.ok(decision.reason.includes('authorized code owner'));
});

test('shouldAutoApproveAbsencePR rejects PR modifying other files', () => {
  const owners = ['alice', 'bob'];
  const decision = shouldAutoApproveAbsencePR({
    modifiedFileNames: ['ABSENCE.md', 'main.go'],
    prAuthor: 'alice',
    ownersList: owners
  });

  assert.strictEqual(decision.canApprove, false);
  assert.ok(decision.reason.includes('files other than ABSENCE.md'));
});

test('shouldAutoApproveAbsencePR rejects PR by non-owner author', () => {
  const owners = ['alice', 'bob'];
  const decision = shouldAutoApproveAbsencePR({
    modifiedFileNames: ['ABSENCE.md'],
    prAuthor: 'dave',
    ownersList: owners
  });

  assert.strictEqual(decision.canApprove, false);
  assert.ok(decision.reason.includes('not listed in OWNERS'));
});
