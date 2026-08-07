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

test('run fails workflow if not executed in pull_request event context', async () => {
  let failedMessage = null;
  const mockCore = {
    info: () => {},
    setFailed: (msg) => { failedMessage = msg; },
  };

  const mockContext = {
    repo: { owner: 'google', repo: 'xmc' },
    payload: {},
  };

  await run({ github: {}, context: mockContext, core: mockCore });
  assert.strictEqual(failedMessage, 'This script must be run in the context of a pull request event.');
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
    payload: {
      pull_request: {
        number: 42,
        user: { login: 'Alice' },
        base: { ref: 'main' },
      },
    },
  };

  await run({ github: mockGithub, context: mockContext, core: mockCore });
  assert.ok(failedMessage.includes('permanently blocked from merging'));
  assert.ok(failedMessage.includes('@Alice'));
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
