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
const { parseRestrictedAuthors, run } = require('./check-feature-request-author');

test('parseRestrictedAuthors parses list correctly and ignores comments/whitespace', () => {
  const sampleContent = `
# Feature Request Authors
alice # feature author
bob

# another comment
charlie
`;
  const result = parseRestrictedAuthors(sampleContent);
  assert.deepStrictEqual(result, ['alice', 'bob', 'charlie']);
});

test('parseRestrictedAuthors returns empty array for empty or comment-only content', () => {
  const sampleContent = `
# Just comments
# and whitespace
`;
  const result = parseRestrictedAuthors(sampleContent);
  assert.deepStrictEqual(result, []);
});

test('run fails workflow if not executed in pull_request or merge_group event context', async () => {
  let failedMessage = null;
  const mockCore = {
    info: () => {},
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'push',
    payload: {},
  };

  await run({ github: {}, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, 'This script must be run in the context of a pull_request or merge_group event.');
});

test('run handles merge_group event context successfully', async () => {
  let failedMessage = null;
  let infoLogs = [];
  const mockCore = {
    info: (msg) => { infoLogs.push(msg); },
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockGithub = {
    rest: {
      repos: {
        compareCommits: async () => ({
          data: {
            commits: [
              { sha: 'abc123456789', author: { login: 'Alice' } },
            ],
          },
        }),
        getContent: async () => ({
          data: {
            content: Buffer.from('bob\ncharlie\n', 'utf8').toString('base64'),
          },
        }),
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'merge_group',
    payload: {
      merge_group: {
        head_sha: 'abc123456789',
        base_sha: 'base12345678',
        base_ref: 'refs/heads/main',
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, null);
  assert.ok(infoLogs.some(log => log.includes('Merge block check passed')));
});

test('run fails closed if GitHub username cannot be resolved in merge_group context', async () => {
  let failedMessage = null;
  const mockCore = {
    info: () => {},
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockGithub = {
    rest: {
      repos: {
        compareCommits: async () => ({
          data: {
            commits: [
              { sha: 'abc123456789', author: null },
            ],
          },
        }),
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'merge_group',
    payload: {
      merge_group: {
        head_sha: 'abc123456789',
        base_sha: 'base12345678',
        base_ref: 'refs/heads/main',
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, 'Could not resolve GitHub username for commit abc1234 in merge group.');
});

test('run fails merge_group if any commit in batch is authored by restricted author', async () => {
  let failedMessage = null;
  const mockCore = {
    info: () => {},
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockGithub = {
    rest: {
      repos: {
        compareCommits: async () => ({
          data: {
            commits: [
              { sha: 'sha1', author: { login: 'Alice' } },
              { sha: 'sha2', author: { login: 'Bob' } },
            ],
          },
        }),
        getContent: async () => ({
          data: {
            content: Buffer.from('bob\ncharlie\n', 'utf8').toString('base64'),
          },
        }),
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'merge_group',
    payload: {
      merge_group: {
        head_sha: 'abc123456789',
        base_sha: 'base12345678',
        base_ref: 'refs/heads/main',
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.ok(failedMessage.includes('authored by restricted author(s): [@Bob]'));
});

test('run fails workflow when PR author is in FEATURE_REQUEST_AUTHORS', async () => {
  let failedMessage = null;
  const mockCore = {
    info: () => {},
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockGithub = {
    rest: {
      repos: {
        getContent: async () => ({
          data: {
            content: Buffer.from('alice\nbob\n', 'utf8').toString('base64'),
          },
        }),
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'pull_request',
    payload: {
      pull_request: {
        number: 42,
        user: { login: 'Alice' },
        base: { ref: 'main' },
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.ok(failedMessage.includes('authored by restricted author(s): [@Alice]'));
});

test('run passes workflow when PR author is not in FEATURE_REQUEST_AUTHORS', async () => {
  let failedMessage = null;
  let infoLogs = [];
  const mockCore = {
    info: (msg) => { infoLogs.push(msg); },
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockGithub = {
    rest: {
      repos: {
        getContent: async () => ({
          data: {
            content: Buffer.from('bob\ncharlie\n', 'utf8').toString('base64'),
          },
        }),
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'pull_request',
    payload: {
      pull_request: {
        number: 42,
        user: { login: 'Alice' },
        base: { ref: 'main' },
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, null);
  assert.ok(infoLogs.some(log => log.includes('Merge block check passed')));
});

test('run handles 404 gracefully if FEATURE_REQUEST_AUTHORS does not exist', async () => {
  let failedMessage = null;
  let infoLogs = [];
  const mockCore = {
    info: (msg) => { infoLogs.push(msg); },
    setFailed: (msg) => { failedMessage = msg; },
  };

  const error404 = new Error('Not Found');
  error404.status = 404;

  const mockGithub = {
    rest: {
      repos: {
        getContent: async () => { throw error404; },
      },
    },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    eventName: 'pull_request',
    payload: {
      pull_request: {
        number: 42,
        user: { login: 'Alice' },
        base: { ref: 'main' },
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, null);
  assert.ok(infoLogs.some(log => log.includes('Skipping merge block')));
});
