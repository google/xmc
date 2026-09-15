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

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const path = require('path');
const os = require('os');

const {
  parseUsernames,
  filterActiveOwners,
  formatFailureIssueBody,
  formatRecurringCommentBody,
  run,
} = require('./nightly-failure-alert');

describe('parseUsernames', () => {
  test('parses usernames ignoring comments and whitespace', () => {
    const content = `
# Reviewer pool
userA
  userB # inline comment

userC
`;
    const res = parseUsernames(content);
    assert.deepEqual(res, ['userA', 'userB', 'userC']);
  });

  test('strips markdown list bullets and @ prefixes', () => {
    const content = `
- @user1
* user2
+ @user3
user4
`;
    const res = parseUsernames(content);
    assert.deepEqual(res, ['user1', 'user2', 'user3', 'user4']);
  });

  test('handles null or empty content gracefully', () => {
    assert.deepEqual(parseUsernames(''), []);
    assert.deepEqual(parseUsernames(null), []);
    assert.deepEqual(parseUsernames(undefined), []);
  });
});

describe('filterActiveOwners', () => {
  test('excludes owners present in absences (case-insensitive)', () => {
    const owners = ['UserAlpha', 'UserBeta', 'UserGamma', 'UserDelta', 'UserEpsilon'];
    const absences = ['userdelta', 'userepsilon'];
    const active = filterActiveOwners(owners, absences);
    assert.deepEqual(active, ['UserAlpha', 'UserBeta', 'UserGamma']);
  });

  test('returns all owners if absences is empty', () => {
    const owners = ['user1', 'user2'];
    assert.deepEqual(filterActiveOwners(owners, []), ['user1', 'user2']);
    assert.deepEqual(filterActiveOwners(owners, null), ['user1', 'user2']);
  });

  test('returns empty array when owners is empty', () => {
    assert.deepEqual(filterActiveOwners([], ['user1']), []);
  });
});

describe('formatFailureIssueBody & formatRecurringCommentBody', () => {
  test('formats markdown body with all metadata, links, and mentions', () => {
    const body = formatFailureIssueBody({
      serverUrl: 'https://github.com',
      owner: 'google',
      repo: 'xmc',
      runId: 12345678,
      sha: 'abcdef1234567890',
      ref: 'refs/heads/main',
      eventName: 'schedule',
      xid: '12006',
      associatedPr: {
        number: 876,
        title: 'feat: example PR',
        url: 'https://github.com/google/xmc/pull/876',
      },
      triggerOutcome: 'success',
      verifyOutcome: 'failure',
      activeOwners: ['userA', 'userB'],
    });

    assert.ok(body.includes('https://github.com/google/xmc/actions/runs/12345678'));
    assert.ok(body.includes('https://github.com/google/xmc/commit/abcdef1234567890'));
    assert.ok(body.includes('`abcdef1`'));
    assert.ok(body.includes('[PR #876: *feat: example PR*](https://github.com/google/xmc/pull/876)'));
    assert.ok(body.includes('`12006`'));
    assert.ok(body.includes('cc @userA @userB'));
    assert.ok(!body.includes('Web UI URL'));
  });

  test('handles missing optional fields safely', () => {
    const body = formatFailureIssueBody({
      owner: 'google',
      repo: 'xmc',
      runId: 123,
      sha: '',
      activeOwners: [],
    });

    assert.ok(body.includes('`unknown`'));
    assert.ok(body.includes('No active owners found to notify'));
  });

  test('formats recurring comment body with recurring header', () => {
    const comment = formatRecurringCommentBody({
      owner: 'google',
      repo: 'xmc',
      runId: 123,
      sha: '1234567',
      activeOwners: ['user1'],
    });

    assert.ok(comment.startsWith('### 🚨 Recurring Nightly Test Failure Detected'));
    assert.ok(comment.includes('cc @user1'));
  });
});

describe('run orchestrator', () => {
  test('creates new issue when no open nightly failure issue exists', async () => {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-alert-test-'));
    const ownersFile = path.join(tempDir, 'OWNERS');
    const absenceFile = path.join(tempDir, 'ABSENCE.md');

    fs.writeFileSync(ownersFile, 'Alice\nBob\nCharlie\n');
    fs.writeFileSync(absenceFile, 'Bob\n');

    const origNeedsContext = process.env.NEEDS_CONTEXT;
    const origXid = process.env.XID;

    process.env.NEEDS_CONTEXT = JSON.stringify({
      'trigger-demo-workload': { result: 'success' },
      'validate-workload-results': { result: 'failure' },
    });
    process.env.XID = '999';

    let createdIssue = null;
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => ({
            data: [{ number: 123, title: 'Sample PR', html_url: 'https://github.com/google/xmc/pull/123' }],
          }),
        },
        issues: {
          listForRepo: async () => ({ data: [] }),
          create: async (payload) => {
            createdIssue = payload;
            return { data: { number: 42 } };
          },
          createComment: async () => assert.fail('Should not create comment when no issue exists'),
        },
      },
    };

    const mockContext = {
      repo: { owner: 'google', repo: 'xmc' },
      runId: 777,
      sha: 'aabbccdd',
      ref: 'refs/heads/main',
      eventName: 'schedule',
    };

    const mockCore = {
      info: () => {},
      warning: () => {},
      setFailed: () => {},
    };

    try {
      const result = await run({
        github: mockGithub,
        context: mockContext,
        core: mockCore,
        ownersPath: ownersFile,
        absencePath: absenceFile,
      });

      assert.equal(result.status, 'issue_created');
      assert.equal(result.issueNumber, 42);
      assert.ok(createdIssue.title.includes('Nightly Test Suite Failure'));
      assert.ok(createdIssue.body.includes('@Alice @Charlie'));
      assert.ok(!createdIssue.body.includes('@Bob'));
      assert.deepEqual(createdIssue.labels, ['nightly-failure', 'bug']);
    } finally {
      if (origNeedsContext === undefined) delete process.env.NEEDS_CONTEXT;
      else process.env.NEEDS_CONTEXT = origNeedsContext;
      if (origXid === undefined) delete process.env.XID;
      else process.env.XID = origXid;
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  test('adds comment to existing open issue when one already exists', async () => {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-alert-test-'));
    const ownersFile = path.join(tempDir, 'OWNERS');
    const absenceFile = path.join(tempDir, 'ABSENCE.md');

    fs.writeFileSync(ownersFile, 'Alice\n');

    let createdComment = null;
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => ({ data: [] }),
        },
        issues: {
          listForRepo: async () => ({ data: [{ number: 101, title: 'Open failure issue' }] }),
          create: async () => assert.fail('Should not create new issue when open issue exists'),
          createComment: async (payload) => {
            createdComment = payload;
            return { data: { id: 1 } };
          },
        },
      },
    };

    const mockContext = {
      repo: { owner: 'google', repo: 'xmc' },
      runId: 888,
      sha: '11223344',
      ref: 'refs/heads/main',
      eventName: 'schedule',
    };

    const mockCore = {
      info: () => {},
      warning: () => {},
      setFailed: () => {},
    };

    try {
      const result = await run({
        github: mockGithub,
        context: mockContext,
        core: mockCore,
        ownersPath: ownersFile,
        absencePath: absenceFile,
      });

      assert.equal(result.status, 'comment_created');
      assert.equal(result.issueNumber, 101);
      assert.equal(createdComment.issue_number, 101);
      assert.ok(createdComment.body.includes('Recurring Nightly Test Failure'));
      assert.ok(createdComment.body.includes('cc @Alice'));
    } finally {
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  test('ignores pull requests with nightly-failure label and creates a new issue', async () => {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-alert-test-'));
    const ownersFile = path.join(tempDir, 'OWNERS');
    const absenceFile = path.join(tempDir, 'ABSENCE.md');

    fs.writeFileSync(ownersFile, 'Alice\n');

    let createdIssue = null;
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => ({ data: [] }),
        },
        issues: {
          listForRepo: async () => ({
            data: [
              { number: 50, title: 'Some PR with label', pull_request: { url: 'https://...' } },
            ],
          }),
          create: async (payload) => {
            createdIssue = payload;
            return { data: { number: 43 } };
          },
          createComment: async () => assert.fail('Should not comment on pull request'),
        },
      },
    };

    const mockContext = {
      repo: { owner: 'google', repo: 'xmc' },
      runId: 999,
      sha: '55667788',
      ref: 'refs/heads/main',
      eventName: 'schedule',
    };

    const mockCore = {
      info: () => {},
      warning: () => {},
      setFailed: () => {},
    };

    try {
      const result = await run({
        github: mockGithub,
        context: mockContext,
        core: mockCore,
        ownersPath: ownersFile,
        absencePath: absenceFile,
      });

      assert.equal(result.status, 'issue_created');
      assert.equal(result.issueNumber, 43);
      assert.ok(createdIssue);
    } finally {
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });
});
