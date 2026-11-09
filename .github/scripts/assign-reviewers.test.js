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
const fs = require('fs');
const os = require('os');
const path = require('path');
const { parseAbsenceFile } = require('./absence-schedule');
const {
  parseReviewersFromOwners,
  filterEligibleReviewers,
  selectRandomReviewers,
  isPullRequestOpen,
  shouldSkipAssignment,
  run
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
  const { absentUsers } = parseAbsenceFile(rawAbsent);
  const result = filterEligibleReviewers(candidates, 'bob', absentUsers);
  assert.deepStrictEqual(result, ['charlie']);
});

test('run skips absent reviewers and logs warnings for malformed ABSENCE.md entries', async () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-assign-reviewers-'));
  const origCwd = process.cwd();
  const warnings = [];
  let requestedReviewers = null;

  try {
    process.chdir(tempDir);
    fs.writeFileSync('OWNERS', 'alice\nbob\ncharlie\n');
    fs.writeFileSync(
      'ABSENCE.md',
      'bob\ncharlie 16.11.2026-15.11.2026\n'
    );

    await run({
      github: {
        rest: {
          pulls: {
            requestReviewers: async ({ reviewers }) => {
              requestedReviewers = reviewers;
            }
          }
        }
      },
      context: {
        repo: { owner: 'google', repo: 'xmc' },
        eventName: 'pull_request',
        payload: {
          pull_request: {
            number: 1,
            state: 'open',
            user: { login: 'alice' },
            requested_reviewers: [],
            requested_teams: []
          }
        }
      },
      core: {
        info: () => {},
        warning: msg => warnings.push(msg),
        setFailed: msg => assert.fail(`Unexpected setFailed: ${msg}`)
      }
    });

    // alice is author, bob is absent, charlie has an invalid line (warned and treated as eligible)
    assert.deepStrictEqual(requestedReviewers, ['charlie']);
    assert.strictEqual(warnings.length, 1);
    assert.ok(warnings[0].includes('after end date'));
  } finally {
    process.chdir(origCwd);
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
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
