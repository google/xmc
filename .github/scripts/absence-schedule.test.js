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
const assert = require('node:assert/strict');
const {
  parseDdMmYyyy,
  parseAbsenceLine,
  parseAbsenceFile,
  validateAbsenceContent
} = require('./absence-schedule');

test('parseDdMmYyyy validates strict DD.MM.YYYY calendar dates in UTC', () => {
  const valid = parseDdMmYyyy('10.11.2026');
  assert.ok(valid instanceof Date);
  assert.equal(valid.getUTCFullYear(), 2026);
  assert.equal(valid.getUTCMonth(), 10);
  assert.equal(valid.getUTCDate(), 10);

  // Leap year Feb 29 is valid; non-leap year Feb 29 is rejected
  assert.ok(parseDdMmYyyy('29.02.2024') instanceof Date);
  assert.equal(parseDdMmYyyy('29.02.2026'), null);

  // Impossible calendar dates and malformed strings are rejected
  assert.equal(parseDdMmYyyy('31.02.2026'), null);
  assert.equal(parseDdMmYyyy('31.04.2026'), null);
  assert.equal(parseDdMmYyyy('00.11.2026'), null);
  assert.equal(parseDdMmYyyy('10.13.2026'), null);
  assert.equal(parseDdMmYyyy('10.00.2026'), null);
  assert.equal(parseDdMmYyyy('2026-11-10'), null);
  assert.equal(parseDdMmYyyy('9.11.2026'), null);
  assert.equal(parseDdMmYyyy(''), null);
});

test('parseAbsenceFile handles bare NICK and inclusive UTC DD.MM.YYYY-DD.MM.YYYY ranges', () => {
  const absenceContent = `
# Unconditional absence
alice

# Scheduled absence (inclusive UTC dates)
bob 10.11.2026-15.11.2026
- @charlie 12.11.2026-12.11.2026 # single-day absence
`;

  // Before bob's range (2026-11-09T23:59:59Z)
  assert.deepEqual(
    parseAbsenceFile(absenceContent, new Date('2026-11-09T23:59:59Z')),
    { absentUsers: ['alice'], errors: [] }
  );

  // On start boundary in early UTC hours (2026-11-10T02:00:00Z) - inclusive
  assert.deepEqual(
    parseAbsenceFile(absenceContent, new Date('2026-11-10T02:00:00Z')),
    { absentUsers: ['alice', 'bob'], errors: [] }
  );

  // Single-day range + middle of bob's range (2026-11-12T12:00:00Z)
  assert.deepEqual(
    parseAbsenceFile(absenceContent, new Date('2026-11-12T12:00:00Z')),
    { absentUsers: ['alice', 'bob', 'charlie'], errors: [] }
  );

  // On end boundary in late UTC hours (2026-11-15T23:59:59Z) - inclusive
  assert.deepEqual(
    parseAbsenceFile(absenceContent, new Date('2026-11-15T23:59:59Z')),
    { absentUsers: ['alice', 'bob'], errors: [] }
  );

  // After end boundary (2026-11-16T00:00:00Z)
  assert.deepEqual(
    parseAbsenceFile(absenceContent, new Date('2026-11-16T00:00:00Z')),
    { absentUsers: ['alice'], errors: [] }
  );
});

test('parseAbsenceFile skips malformed or inverted lines and returns diagnostic errors', () => {
  const content = `
validUser
invertedUser 15.11.2026-10.11.2026
badDateUser 31.02.2026-15.11.2026
extraTokensUser 10.11.2026-15.11.2026 extra
`;
  const result = parseAbsenceFile(content, new Date('2026-11-12T00:00:00Z'));
  assert.deepEqual(result.absentUsers, ['validUser']);
  assert.equal(result.errors.length, 3);
  assert.ok(result.errors[0].includes('after end date'));
  assert.ok(result.errors[1].includes('invalid start date'));
  assert.ok(result.errors[2].includes('invalid format'));
  assert.equal(parseAbsenceLine('invertedUser 15.11.2026-10.11.2026', 2).valid, false);
});

test('validateAbsenceContent validates content and rejects non-string input', () => {
  assert.deepEqual(validateAbsenceContent('alice\nbob 10.11.2026-15.11.2026\n'), {
    valid: true
  });
  const nonStringResult = validateAbsenceContent(null);
  assert.equal(nonStringResult.valid, false);
  assert.ok(nonStringResult.reason.includes('not a string'));
});
