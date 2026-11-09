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
  parseDdMmYyyy,
  parseAbsenceLine,
  parseAbsenceFile,
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers,
  isPullRequestOpen,
  shouldSkipAssignment
} = require('./assign-reviewers');

test('parseReviewersFromOwners parses flat text reviewers correctly and strips comments', () => {
  const sampleOwners = `
# Sample OWNERS file with one username per line
alice # frontend lead
bob
charlie

# another comment
`;
  const result = parseReviewersFromOwners(sampleOwners);
  assert.deepStrictEqual(result, ['alice', 'bob', 'charlie']);
});

test('parseReviewersFromOwners returns empty array if no usernames defined', () => {
  const sampleOwners = `
# Just comments
# and blank lines
`;
  const result = parseReviewersFromOwners(sampleOwners);
  assert.deepStrictEqual(result, []);
});

test('filterEligibleReviewers excludes the PR author', () => {
  const candidates = ['alice', 'bob', 'charlie'];
  const result = filterEligibleReviewers(candidates, 'bob');
  assert.deepStrictEqual(result, ['alice', 'charlie']);
});

test('filterEligibleReviewers excludes the PR author case-insensitively', () => {
  const candidates = ['Alice', 'Bob', 'Charlie'];
  const result = filterEligibleReviewers(candidates, 'bob');
  assert.deepStrictEqual(result, ['Alice', 'Charlie']);
});

test('filterEligibleReviewers excludes absent reviewers', () => {
  const candidates = ['alice', 'bob', 'charlie', 'dave', 'eve'];
  const absent = ['alice', 'dave'];
  const result = filterEligibleReviewers(candidates, 'bob', absent);
  assert.deepStrictEqual(result, ['charlie', 'eve']);
});

test('filterEligibleReviewers excludes absent reviewers case-insensitively', () => {
  const candidates = ['Alice', 'Bob', 'Charlie', 'Dave', 'Eve'];
  const absent = ['alice', 'DAVE'];
  const result = filterEligibleReviewers(candidates, 'bob', absent);
  assert.deepStrictEqual(result, ['Charlie', 'Eve']);
});

test('absent reviewers mapping handles markdown formatted lists (- , * , @)', () => {
  const candidates = ['alice', 'bob', 'charlie', 'dave', 'eve'];
  const rawAbsent = ['- alice', '* @dave', '+ eve'].join('\n');
  const absent = parseAbsenceFile(rawAbsent);
  const result = filterEligibleReviewers(candidates, 'bob', absent);
  assert.deepStrictEqual(result, ['charlie']);
});

test('parseDdMmYyyy validates strict DD.MM.YYYY calendar dates', () => {
  const valid = parseDdMmYyyy('10.11.2026');
  assert.ok(valid instanceof Date);
  assert.strictEqual(valid.getFullYear(), 2026);
  assert.strictEqual(valid.getMonth(), 10);
  assert.strictEqual(valid.getDate(), 10);

  // Leap year Feb 29 is valid; non-leap year Feb 29 is rejected
  assert.ok(parseDdMmYyyy('29.02.2024') instanceof Date);
  assert.strictEqual(parseDdMmYyyy('29.02.2026'), null);

  // Impossible calendar dates and malformed strings are rejected
  assert.strictEqual(parseDdMmYyyy('31.02.2026'), null);
  assert.strictEqual(parseDdMmYyyy('31.04.2026'), null);
  assert.strictEqual(parseDdMmYyyy('00.11.2026'), null);
  assert.strictEqual(parseDdMmYyyy('10.13.2026'), null);
  assert.strictEqual(parseDdMmYyyy('10.00.2026'), null);
  assert.strictEqual(parseDdMmYyyy('2026-11-10'), null);
  assert.strictEqual(parseDdMmYyyy('9.11.2026'), null);
  assert.strictEqual(parseDdMmYyyy(''), null);
});

test('parseAbsenceFile handles bare NICK and inclusive DD.MM.YYYY-DD.MM.YYYY ranges', () => {
  const absenceContent = `
# Unconditional absence
alice

# Scheduled absence (inclusive)
bob 10.11.2026-15.11.2026
- @charlie 12.11.2026-12.11.2026 # single-day absence
`;

  // Before bob's range (09.11.2026)
  assert.deepStrictEqual(
    parseAbsenceFile(absenceContent, new Date(2026, 10, 9)),
    ['alice']
  );

  // On start boundary (10.11.2026) - inclusive
  assert.deepStrictEqual(
    parseAbsenceFile(absenceContent, new Date(2026, 10, 10, 15, 30)),
    ['alice', 'bob']
  );

  // Single-day range + middle of bob's range (12.11.2026)
  assert.deepStrictEqual(
    parseAbsenceFile(absenceContent, new Date(2026, 10, 12)),
    ['alice', 'bob', 'charlie']
  );

  // On end boundary (15.11.2026) - inclusive
  assert.deepStrictEqual(
    parseAbsenceFile(absenceContent, new Date(2026, 10, 15, 23, 59)),
    ['alice', 'bob']
  );

  // After end boundary (16.11.2026)
  assert.deepStrictEqual(
    parseAbsenceFile(absenceContent, new Date(2026, 10, 16)),
    ['alice']
  );
});

test('parseAbsenceFile ignores malformed or inverted date ranges', () => {
  const content = `
validUser
invertedUser 15.11.2026-10.11.2026
badDateUser 31.02.2026-15.11.2026
extraTokensUser 10.11.2026-15.11.2026 extra
`;
  assert.deepStrictEqual(
    parseAbsenceFile(content, new Date(2026, 10, 12)),
    ['validUser']
  );
  assert.strictEqual(parseAbsenceLine('invertedUser 15.11.2026-10.11.2026', 2).valid, false);
});

test('selectRandomReviewers returns up to count elements and preserves candidate pool elements', () => {
  const candidates = ['alice', 'bob', 'charlie', 'dave', 'eve'];
  const selected = selectRandomReviewers(candidates, 1);
  assert.strictEqual(selected.length, 1);
  selected.forEach(reviewer => {
    assert.ok(candidates.includes(reviewer));
  });
});

test('shouldSkipAssignment returns true when reviewers or teams are already assigned', () => {
  assert.strictEqual(shouldSkipAssignment(2), true);
  assert.strictEqual(shouldSkipAssignment(1), true);
});

test('shouldSkipAssignment returns false when no reviewers or teams are assigned', () => {
  assert.strictEqual(shouldSkipAssignment(0), false);
});

test('isPullRequestOpen returns true only when state is open', () => {
  assert.strictEqual(isPullRequestOpen('open'), true);
  assert.strictEqual(isPullRequestOpen('OPEN'), true);
  assert.strictEqual(isPullRequestOpen('closed'), false);
  assert.strictEqual(isPullRequestOpen('merged'), false);
  assert.strictEqual(isPullRequestOpen(''), false);
});
