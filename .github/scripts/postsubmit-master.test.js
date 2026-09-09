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
  getDedupMarker,
  buildJobResults,
  getFailedJobs,
  formatFailureCommentBody,
  run,
} = require('./postsubmit-master');

test('getDedupMarker constructs standard HTML comment with SHA', () => {
  const sha = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
  assert.strictEqual(
    getDedupMarker(sha),
    '<!-- xmc-postsubmit-master-failure: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 -->'
  );
});

test('buildJobResults maps needs context dictionary to structured test suite results', () => {
  const needsContext = {
    'run-backend-tests': { result: 'success' },
    'run-ui-tests': { result: 'failure' },
    'run-chart-tests': { result: 'skipped' },
    'run-e2e-tests': { result: 'timed_out' },
  };

  const results = buildJobResults(needsContext);
  assert.strictEqual(results.length, 4);

  assert.deepStrictEqual(results[0], {
    name: 'Backend & Go Unit Tests',
    jobId: 'run-backend-tests',
    result: 'success',
  });
  assert.deepStrictEqual(results[1], {
    name: 'Angular UI Unit Tests',
    jobId: 'run-ui-tests',
    result: 'failure',
  });
  assert.deepStrictEqual(results[2], {
    name: 'Helm Chart Configuration Tests',
    jobId: 'run-chart-tests',
    result: 'skipped',
  });
  assert.deepStrictEqual(results[3], {
    name: 'KinD & Playwright Headless E2E',
    jobId: 'run-e2e-tests',
    result: 'timed_out',
  });
});

test('buildJobResults handles empty or missing keys gracefully with unknown result', () => {
  const results = buildJobResults({});
  assert.strictEqual(results.length, 4);
  for (const r of results) {
    assert.strictEqual(r.result, 'unknown');
  }

  const nullResults = buildJobResults(null);
  assert.strictEqual(nullResults.length, 4);
});

test('getFailedJobs correctly filters failure, timed_out, and cancelled states', () => {
  const jobResults = [
    { name: 'Job 1', result: 'success' },
    { name: 'Job 2', result: 'failure' },
    { name: 'Job 3', result: 'skipped' },
    { name: 'Job 4', result: 'timed_out' },
    { name: 'Job 5', result: 'cancelled' },
    { name: 'Job 6', result: 'unknown' },
  ];

  const failed = getFailedJobs(jobResults);
  assert.strictEqual(failed.length, 3);
  assert.deepStrictEqual(
    failed.map(f => f.name),
    ['Job 2', 'Job 4', 'Job 5']
  );

  assert.deepStrictEqual(getFailedJobs([]), []);
  assert.deepStrictEqual(getFailedJobs(null), []);
});

test('formatFailureCommentBody generates complete markdown table with icons and links', () => {
  const body = formatFailureCommentBody({
    headSha: 'c094d151fd62003048085bf3a7bdc311caef4305',
    author: 'octocat',
    serverUrl: 'https://github.com',
    owner: 'google',
    repo: 'xmc',
    runId: '12345678',
    jobResults: [
      { name: 'Backend & Go Unit Tests', result: 'success' },
      { name: 'Angular UI Unit Tests', result: 'failure' },
      { name: 'Helm Chart Configuration Tests', result: 'skipped' },
      { name: 'KinD & Playwright Headless E2E', result: 'timed_out' },
    ],
  });

  assert.match(body, /<!-- xmc-postsubmit-master-failure: c094d151fd62003048085bf3a7bdc311caef4305 -->/);
  assert.match(body, /### 🚨 Post-Submit Test Regression on `main`/);
  assert.match(body, /Hello @octocat,/);
  assert.match(body, /\[`c094d15`\]\(https:\/\/github\.com\/google\/xmc\/commit\/c094d151fd62003048085bf3a7bdc311caef4305\)/);
  assert.match(body, /\|\s*\*\*Backend & Go Unit Tests\*\*\s*\|\s*✅ `success`\s*\|/);
  assert.match(body, /\|\s*\*\*Angular UI Unit Tests\*\*\s*\|\s*❌ `failure`\s*\|/);
  assert.match(body, /\|\s*\*\*Helm Chart Configuration Tests\*\*\s*\|\s*⚪ `skipped`\s*\|/);
  assert.match(body, /\|\s*\*\*KinD & Playwright Headless E2E\*\*\s*\|\s*❌ `timed_out`\s*\|/);
  assert.match(body, /https:\/\/github\.com\/google\/xmc\/actions\/runs\/12345678/);
});

test('run terminates early when all jobs succeed', async () => {
  const logs = [];
  const mockCore = {
    info: msg => logs.push(`INFO: ${msg}`),
    warning: msg => logs.push(`WARN: ${msg}`),
    error: msg => logs.push(`ERROR: ${msg}`),
  };

  const mockContext = {
    sha: '1111111111111111111111111111111111111111',
    repo: { owner: 'google', repo: 'xmc' },
    runId: 100,
  };

  const mockGithub = {
    rest: {
      repos: {
        listPullRequestsAssociatedWithCommit: async () => {
          throw new Error('Should not be called when tests pass');
        },
      },
    },
  };

  const needsContext = {
    'run-backend-tests': { result: 'success' },
    'run-ui-tests': { result: 'success' },
    'run-chart-tests': { result: 'success' },
    'run-e2e-tests': { result: 'success' },
  };

  const res = await run({
    github: mockGithub,
    context: mockContext,
    core: mockCore,
    needsContext,
  });

  assert.strictEqual(res.status, 'success');
  assert.strictEqual(res.failedCount, 0);
  assert.ok(logs.some(l => l.includes('All post-submit master pipeline jobs succeeded.')));
});

test('run handles direct push when no PR is associated with commit', async () => {
  const logs = [];
  const mockCore = {
    info: msg => logs.push(`INFO: ${msg}`),
    warning: msg => logs.push(`WARN: ${msg}`),
    error: msg => logs.push(`ERROR: ${msg}`),
  };

  const mockContext = {
    sha: '2222222222222222222222222222222222222222',
    repo: { owner: 'google', repo: 'xmc' },
    runId: 101,
  };

  const mockGithub = {
    rest: {
      repos: {
        listPullRequestsAssociatedWithCommit: async () => ({ data: [] }),
      },
    },
  };

  const needsContext = {
    'run-backend-tests': { result: 'failure' },
  };

  const res = await run({
    github: mockGithub,
    context: mockContext,
    core: mockCore,
    needsContext,
  });

  assert.strictEqual(res.status, 'no_pr');
  assert.ok(logs.some(l => l.includes('No PR associated with commit')));
});

test('run skips posting comment if deduplication marker is already present on PR', async () => {
  const logs = [];
  const mockCore = {
    info: msg => logs.push(`INFO: ${msg}`),
    warning: msg => logs.push(`WARN: ${msg}`),
    error: msg => logs.push(`ERROR: ${msg}`),
  };

  const headSha = '3333333333333333333333333333333333333333';
  const mockContext = {
    sha: headSha,
    repo: { owner: 'google', repo: 'xmc' },
    runId: 102,
  };

  let createCommentCalled = false;
  const mockGithub = {
    rest: {
      repos: {
        listPullRequestsAssociatedWithCommit: async () => ({
          data: [{ number: 830, user: { login: 'dev-user' } }],
        }),
      },
      issues: {
        listComments: async () => ({
          data: [
            { id: 1, body: 'Some generic comment' },
            { id: 2, body: `<!-- xmc-postsubmit-master-failure: ${headSha} -->\nOlder failure log` },
          ],
        }),
        createComment: async () => {
          createCommentCalled = true;
        },
      },
    },
  };

  const res = await run({
    github: mockGithub,
    context: mockContext,
    core: mockCore,
    needsContext: { 'run-ui-tests': { result: 'failure' } },
  });

  assert.strictEqual(res.status, 'already_commented');
  assert.strictEqual(createCommentCalled, false);
  assert.ok(logs.some(l => l.includes('Failure comment already posted on PR #830')));
});

test('run posts failure breakdown comment on PR when jobs fail and no duplicate exists', async () => {
  const logs = [];
  const mockCore = {
    info: msg => logs.push(`INFO: ${msg}`),
    warning: msg => logs.push(`WARN: ${msg}`),
    error: msg => logs.push(`ERROR: ${msg}`),
  };

  const headSha = '4444444444444444444444444444444444444444';
  const mockContext = {
    sha: headSha,
    repo: { owner: 'google', repo: 'xmc' },
    runId: 103,
    serverUrl: 'https://github.com',
  };

  let postedComment = null;
  const mockGithub = {
    rest: {
      repos: {
        listPullRequestsAssociatedWithCommit: async () => ({
          data: [{ number: 830, user: { login: 'test-author' } }],
        }),
      },
      issues: {
        listComments: async () => ({ data: [] }),
        createComment: async payload => {
          postedComment = payload;
          return { data: { id: 999 } };
        },
      },
    },
  };

  const res = await run({
    github: mockGithub,
    context: mockContext,
    core: mockCore,
    needsContext: {
      'run-backend-tests': { result: 'failure' },
      'run-ui-tests': { result: 'success' },
      'run-chart-tests': { result: 'success' },
      'run-e2e-tests': { result: 'cancelled' },
    },
  });

  assert.strictEqual(res.status, 'comment_posted');
  assert.strictEqual(res.prNumber, 830);
  assert.deepStrictEqual(res.failedJobs, [
    'Backend & Go Unit Tests',
    'KinD & Playwright Headless E2E',
  ]);

  assert.ok(postedComment);
  assert.strictEqual(postedComment.issue_number, 830);
  assert.strictEqual(postedComment.owner, 'google');
  assert.strictEqual(postedComment.repo, 'xmc');
  assert.match(postedComment.body, /Hello @test-author,/);
  assert.match(postedComment.body, /<!-- xmc-postsubmit-master-failure: 4444444444444444444444444444444444444444 -->/);
  assert.match(postedComment.body, /Backend & Go Unit Tests.*❌ `failure`/);
  assert.match(postedComment.body, /KinD & Playwright Headless E2E.*❌ `cancelled`/);
  assert.match(postedComment.body, /Angular UI Unit Tests.*✅ `success`/);
});
