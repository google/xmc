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
  resolveActiveOwners,
  sanitizeMarkdownTitle,
  formatFailureIssueBody,
  formatRecurringCommentBody,
  lookupAssociatedPullRequest,
  findExistingNightlyFailureIssue,
  postOrUpdateAlertIssue,
  parseNeedsContext,
  buildAlertParams,
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

describe('resolveActiveOwners', () => {
  test('reads files from disk and filters active owners', () => {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-resolve-owners-'));
    const ownersFile = path.join(tempDir, 'OWNERS');
    const absenceFile = path.join(tempDir, 'ABSENCE.md');

    try {
      fs.writeFileSync(ownersFile, 'User1\nUser2\nUser3\n');
      fs.writeFileSync(absenceFile, 'User2\n');

      const active = resolveActiveOwners(ownersFile, absenceFile, { info: () => {}, warning: () => {} });
      assert.deepEqual(active, ['User1', 'User3']);
    } finally {
      fs.rmSync(tempDir, { recursive: true, force: true });
    }
  });

  test('handles missing owners and absence files gracefully', () => {
    let warningLogged = false;
    const mockCore = {
      info: () => {},
      warning: (msg) => {
        if (msg.includes('not found')) warningLogged = true;
      },
    };
    const active = resolveActiveOwners('/nonexistent/OWNERS', '/nonexistent/ABSENCE.md', mockCore);
    assert.deepEqual(active, []);
    assert.ok(warningLogged);
  });
});

describe('sanitizeMarkdownTitle', () => {
  test('escapes pipe, bracket, asterisk, and underscore characters', () => {
    const raw = 'fix(core): handle [special] | *value* and _sub_';
    const clean = sanitizeMarkdownTitle(raw);
    assert.equal(clean, 'fix(core): handle \\[special\\] \\| \\*value\\* and \\_sub\\_');
  });

  test('replaces newlines and carriage returns with spaces and trims', () => {
    const raw = 'feat(ui):\nmultiple\r\nlines  \n';
    const clean = sanitizeMarkdownTitle(raw);
    assert.equal(clean, 'feat(ui): multiple lines');
  });

  test('handles null, undefined, or non-string inputs safely', () => {
    assert.equal(sanitizeMarkdownTitle(''), '');
    assert.equal(sanitizeMarkdownTitle(null), '');
    assert.equal(sanitizeMarkdownTitle(undefined), '');
    assert.equal(sanitizeMarkdownTitle(123), '');
  });
});

describe('formatFailureIssueBody & formatRecurringCommentBody', () => {
  test('formats markdown body with all metadata, links, and mentions', () => {
    const body = formatFailureIssueBody({
      serverUrl: 'https://github.com',
      owner: 'google',
      repo: 'xmc',
      runId: 12345678,
      sha: 'abcdef1234567890abcdef1234567890abcdef12',
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
    assert.ok(body.includes('https://github.com/google/xmc/commit/abcdef1234567890abcdef1234567890abcdef12'));
    assert.ok(body.includes('`abcdef1`'));
    assert.ok(body.includes('[PR #876: *feat: example PR*](https://github.com/google/xmc/pull/876)'));
    assert.ok(body.includes('`12006`'));
    assert.ok(body.includes('cc @userA @userB'));
    assert.ok(!body.includes('Web UI URL'));
  });

  test('sanitizes PR title containing markdown formatting characters in issue body', () => {
    const body = formatFailureIssueBody({
      serverUrl: 'https://github.com',
      owner: 'google',
      repo: 'xmc',
      runId: 12345678,
      sha: 'abcdef1234567890abcdef1234567890abcdef12',
      ref: 'refs/heads/main',
      eventName: 'schedule',
      associatedPr: {
        number: 999,
        title: 'fix(ci): [alert] | table break *test* _bar_\nnewline',
        url: 'https://github.com/google/xmc/pull/999',
      },
      triggerOutcome: 'cancelled',
      verifyOutcome: 'cancelled',
      activeOwners: ['userA'],
    });

    assert.ok(body.includes('[PR #999: *fix(ci): \\[alert\\] \\| table break \\*test\\* \\_bar\\_ newline*](https://github.com/google/xmc/pull/999)'));
    assert.ok(body.includes('| **Trigger Action** | `cancelled` |'));
    assert.ok(body.includes('| **Verification Assertions** | `cancelled` |'));
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
      sha: 'abcdef1234567890abcdef1234567890abcdef12',
      activeOwners: ['user1'],
    });

    assert.ok(comment.startsWith('### 🚨 Recurring Nightly Test Failure Detected'));
    assert.ok(comment.includes('cc @user1'));
  });
});

describe('lookupAssociatedPullRequest', () => {
  test('resolves and returns associated PR metadata when found', async () => {
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => ({
            data: [{ number: 876, title: 'feat: add feature', html_url: 'https://github.com/google/xmc/pull/876' }],
          }),
        },
      },
    };

    const pr = await lookupAssociatedPullRequest(mockGithub, 'google', 'xmc', 'abcdef1234567890abcdef1234567890abcdef12', { info: () => {}, warning: () => {} });
    assert.deepEqual(pr, {
      number: 876,
      title: 'feat: add feature',
      url: 'https://github.com/google/xmc/pull/876',
    });
  });

  test('returns null when no PRs are associated with commit', async () => {
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => ({ data: [] }),
        },
      },
    };

    const pr = await lookupAssociatedPullRequest(mockGithub, 'google', 'xmc', 'abcdef1234567890abcdef1234567890abcdef12', null);
    assert.equal(pr, null);
  });

  test('returns null gracefully on API error without throwing', async () => {
    const mockGithub = {
      rest: {
        repos: {
          listPullRequestsAssociatedWithCommit: async () => {
            throw new Error('API Rate limit exceeded');
          },
        },
      },
    };

    let warningLogged = false;
    const pr = await lookupAssociatedPullRequest(mockGithub, 'google', 'xmc', 'abcdef1234567890abcdef1234567890abcdef12', {
      info: () => {},
      warning: () => { warningLogged = true; },
    });
    assert.equal(pr, null);
    assert.ok(warningLogged);
  });
});

describe('findExistingNightlyFailureIssue', () => {
  test('returns the open issue and ignores pull requests', async () => {
    const mockGithub = {
      rest: {
        issues: {
          listForRepo: async () => ({
            data: [
              { number: 10, title: 'PR 10', pull_request: {} },
              { number: 20, title: 'Issue 20' },
            ],
          }),
        },
      },
    };

    const issue = await findExistingNightlyFailureIssue(mockGithub, 'google', 'xmc', 'nightly-failure', null);
    assert.ok(issue);
    assert.equal(issue.number, 20);
  });

  test('returns null when no issues exist or API fails', async () => {
    const mockGithub = {
      rest: {
        issues: {
          listForRepo: async () => {
            throw new Error('Network failure');
          },
        },
      },
    };

    const issue = await findExistingNightlyFailureIssue(mockGithub, 'google', 'xmc', 'nightly-failure', {
      warning: () => {},
    });
    assert.equal(issue, null);
  });
});

describe('postOrUpdateAlertIssue', () => {
  test('creates a comment when existingIssue is provided', async () => {
    let commentPayload = null;
    const mockGithub = {
      rest: {
        issues: {
          createComment: async (payload) => {
            commentPayload = payload;
            return { data: { id: 100 } };
          },
        },
      },
    };

    const result = await postOrUpdateAlertIssue(
      mockGithub,
      'google',
      'xmc',
      { number: 55 },
      { owner: 'google', repo: 'xmc', runId: 1, sha: 'abcdef1234567890abcdef1234567890abcdef12' },
      'nightly-failure',
      { info: () => {} }
    );

    assert.equal(result.status, 'comment_created');
    assert.equal(result.issueNumber, 55);
    assert.equal(commentPayload.issue_number, 55);
  });

  test('creates a new issue when existingIssue is null', async () => {
    let createPayload = null;
    const mockGithub = {
      rest: {
        issues: {
          create: async (payload) => {
            createPayload = payload;
            return { data: { number: 77 } };
          },
        },
      },
    };

    const result = await postOrUpdateAlertIssue(
      mockGithub,
      'google',
      'xmc',
      null,
      { owner: 'google', repo: 'xmc', runId: 1, sha: 'abcdef1234567890abcdef1234567890abcdef12' },
      'nightly-failure',
      { info: () => {} }
    );

    assert.equal(result.status, 'issue_created');
    assert.equal(result.issueNumber, 77);
    assert.deepEqual(createPayload.labels, ['nightly-failure', 'bug']);
  });
});

describe('parseNeedsContext', () => {
  test('parses valid JSON string from environment', () => {
    const orig = process.env.NEEDS_CONTEXT;
    try {
      process.env.NEEDS_CONTEXT = JSON.stringify({ jobA: { result: 'success' } });
      const parsed = parseNeedsContext(null);
      assert.deepEqual(parsed, { jobA: { result: 'success' } });
    } finally {
      if (orig === undefined) delete process.env.NEEDS_CONTEXT;
      else process.env.NEEDS_CONTEXT = orig;
    }
  });

  test('returns empty object when JSON is invalid', () => {
    const orig = process.env.NEEDS_CONTEXT;
    try {
      process.env.NEEDS_CONTEXT = 'invalid json {';
      let warningLogged = false;
      const parsed = parseNeedsContext({ warning: () => { warningLogged = true; } });
      assert.deepEqual(parsed, {});
      assert.ok(warningLogged);
    } finally {
      if (orig === undefined) delete process.env.NEEDS_CONTEXT;
      else process.env.NEEDS_CONTEXT = orig;
    }
  });
});

describe('buildAlertParams', () => {
  const baseContext = {
    repo: { owner: 'google', repo: 'xmc' },
    runId: 123456,
    sha: 'abcdef1234567890abcdef1234567890abcdef12',
    ref: 'refs/heads/main',
    eventName: 'schedule',
    serverUrl: 'https://github.com',
  };

  test('resolves outcomes for run-and-verify-demo-workload workflow', () => {
    const needsContext = {
      'run-and-verify-demo-workload': { result: 'failure' },
    };
    const params = buildAlertParams(baseContext, ['alice'], null, needsContext);
    assert.equal(params.triggerOutcome, 'failure');
    assert.equal(params.verifyOutcome, 'failure');
    assert.equal(params.owner, 'google');
    assert.equal(params.repo, 'xmc');
    assert.deepEqual(params.activeOwners, ['alice']);
  });

  test('resolves outcomes for trigger-demo-workload and validate-workload-results workflow', () => {
    const needsContext = {
      'trigger-demo-workload': { result: 'success' },
      'validate-workload-results': { result: 'failure' },
    };
    const params = buildAlertParams(baseContext, ['bob'], { number: 895, title: 'demo' }, needsContext);
    assert.equal(params.triggerOutcome, 'success');
    assert.equal(params.verifyOutcome, 'failure');
    assert.deepEqual(params.associatedPr, { number: 895, title: 'demo' });
  });

  test('resolves outcomes for run-and-verify-examples and aggregate-results workflow', () => {
    const needsContext = {
      'run-and-verify-examples': { result: 'failure' },
      'aggregate-results': { result: 'cancelled' },
    };
    const params = buildAlertParams(baseContext, [], null, needsContext);
    assert.equal(params.triggerOutcome, 'failure');
    assert.equal(params.verifyOutcome, 'cancelled');
  });

  test('falls back to unknown when job keys are missing', () => {
    const params = buildAlertParams(baseContext, [], null, {});
    assert.equal(params.triggerOutcome, 'unknown');
    assert.equal(params.verifyOutcome, 'unknown');
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
      sha: 'aabbccdd11223344556677889900aabbccddeeff',
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
      sha: '11223344556677889900aabbccddeeff11223344',
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
      sha: '556677889900aabbccddeeff1122334455667788',
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
