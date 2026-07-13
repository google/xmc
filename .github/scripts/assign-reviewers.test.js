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
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers
} = require('./assign-reviewers');

test('parseReviewersFromOwners parses reviewers correctly', () => {
  const sampleOwners = `
# Sample OWNERS file
reviewers:
  - alice
  - bob
  - charlie

approvers:
  - dave
  - eve
`;
  const result = parseReviewersFromOwners(sampleOwners);
  assert.deepStrictEqual(result, ['alice', 'bob', 'charlie']);
});

test('parseReviewersFromOwners returns empty array if no reviewers defined', () => {
  const sampleOwners = `
approvers:
  - dave
`;
  const result = parseReviewersFromOwners(sampleOwners);
  assert.deepStrictEqual(result, []);
});

test('filterEligibleReviewers excludes the PR author', () => {
  const candidates = ['alice', 'bob', 'charlie'];
  const result = filterEligibleReviewers(candidates, 'bob');
  assert.deepStrictEqual(result, ['alice', 'charlie']);
});

test('selectRandomReviewers returns up to count elements and preserves candidate pool elements', () => {
  const candidates = ['alice', 'bob', 'charlie', 'dave', 'eve'];
  const selected = selectRandomReviewers(candidates, 2);
  assert.strictEqual(selected.length, 2);
  selected.forEach(reviewer => {
    assert.ok(candidates.includes(reviewer));
  });
});
