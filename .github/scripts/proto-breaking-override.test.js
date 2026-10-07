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
const { OVERRIDE_LABEL, decideOverride, run } = require('./proto-breaking-override');

const AUTHOR = { login: 'octocat', id: 1 };
const MAINTAINER = { login: 'maintainer', id: 2 };
const BOT = { login: 'helper[bot]', id: 3 };
const LABELS = [{ name: 'api' }, { name: OVERRIDE_LABEL }];
const CONTEXT = {
  repo: { owner: 'google', repo: 'xmc' },
  payload: { pull_request: { number: 42 } },
};

/**
 * Builds an issue event shaped like the GitHub issue events API response.
 * @param {string} event - Event name, e.g. 'labeled'.
 * @param {{ login: string, id: number }} actor - Account that triggered the event.
 * @param {string} createdAt - ISO 8601 timestamp.
 * @param {Object} [options]
 * @param {string} [options.type] - Account type of the actor.
 * @param {string} [options.label] - Label name.
 * @returns {Object}
 */
function issueEvent(event, actor, createdAt, { type = 'User', label = OVERRIDE_LABEL } = {}) {
  return { event, label: { name: label }, actor: { ...actor, type }, created_at: createdAt };
}

/**
 * Fake Octokit client serving one pull request and its issue events split into pages. Like the
 * GitHub API, each events response links the next page in its `link` header, if there is one.
 * @param {Array<{ name: string }>} labels - Labels of the pull request.
 * @param {Array<Array<Object>>} eventPages - Issue events per page.
 * @returns {Object}
 */
function fakeGithub(labels, eventPages) {
  const calls = [];
  return {
    calls,
    // Mirrors Octokit's paginate: requests pages while the response links a next one.
    paginate: async (method, params) => {
      const items = [];
      for (let page = 1; ; page++) {
        const { data, headers } = await method({ ...params, page });
        items.push(...data);
        if (!/rel="next"/.test(headers.link || '')) return items;
      }
    },
    rest: {
      pulls: {
        get: async params => {
          calls.push({ method: 'pulls.get', params });
          return { data: { labels, user: AUTHOR } };
        },
      },
      issues: {
        listEvents: async params => {
          calls.push({ method: 'issues.listEvents', params });
          const link = `<https://api.github.com/events?page=${params.page + 1}>; rel="next"`;
          const headers = params.page < eventPages.length ? { link } : {};
          return { data: eventPages[params.page - 1] || [], headers };
        },
      },
    },
  };
}

/**
 * Fake Actions core library recording outputs, annotations, and the step summary.
 * @returns {Object}
 */
function fakeCore() {
  const core = {
    outputs: {},
    notices: [],
    warnings: [],
    summaryLines: [],
    summaryWrites: 0,
    setOutput: (name, value) => {
      core.outputs[name] = value;
    },
    notice: msg => core.notices.push(msg),
    warning: msg => core.warnings.push(msg),
  };
  core.summary = {
    addRaw: (text, addEol) => {
      core.summaryLines.push(addEol ? `${text}\n` : text);
      return core.summary;
    },
    write: async () => {
      core.summaryWrites += 1;
      return core.summary;
    },
  };
  return core;
}

test('decideOverride honors the label applied by a user other than the author', () => {
  const events = [issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z')];
  assert.deepStrictEqual(decideOverride({ labels: LABELS, events, author: AUTHOR }), {
    honored: true,
    actor: 'maintainer',
    reason: 'its latest application was by @maintainer',
  });
});

test('decideOverride ignores the label applied by the pull request author', () => {
  const events = [issueEvent('labeled', AUTHOR, '2026-01-01T00:00:00Z')];
  const decision = decideOverride({ labels: LABELS, events, author: AUTHOR });
  assert.strictEqual(decision.honored, false);
  assert.strictEqual(decision.reason, 'its latest application was by the pull request author (@octocat)');
});

test('decideOverride identifies the author by account id because logins can change', () => {
  const decide = actor => decideOverride({
    labels: LABELS,
    events: [issueEvent('labeled', actor, '2026-01-01T00:00:00Z')],
    author: AUTHOR,
  });
  // The author under a new login is still the author.
  assert.strictEqual(decide({ login: 'octocat-renamed', id: AUTHOR.id }).honored, false);
  // Another account that took over the author's former login is not.
  assert.strictEqual(decide({ login: AUTHOR.login, id: 4 }).honored, true);
});

test('decideOverride fails closed when the account id of the label actor is missing', () => {
  const events = [issueEvent('labeled', { login: 'maintainer' }, '2026-01-01T00:00:00Z')];
  assert.deepStrictEqual(decideOverride({ labels: LABELS, events, author: AUTHOR }), {
    honored: false,
    actor: 'maintainer',
    reason: 'the account id of @maintainer or of the pull request author is missing',
  });
});

test('decideOverride fails closed when the account id of the author is missing', () => {
  const byMaintainer = [issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z')];
  // With both ids missing, undefined === undefined must not make the actor the author.
  const withoutIds = [issueEvent('labeled', { login: 'maintainer' }, '2026-01-01T00:00:00Z')];
  for (const author of [{ login: 'octocat' }, null, undefined]) {
    for (const events of [byMaintainer, withoutIds]) {
      const decision = decideOverride({ labels: LABELS, events, author });
      assert.strictEqual(decision.honored, false);
      assert.strictEqual(decision.reason, 'the account id of @maintainer or of the pull request author is missing');
    }
  }
});

test('decideOverride lets the later-listed event win a timestamp tie', () => {
  const events = [
    issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z'),
    issueEvent('labeled', AUTHOR, '2026-01-01T00:00:00Z'),
  ];
  assert.strictEqual(decideOverride({ labels: LABELS, events, author: AUTHOR }).actor, AUTHOR.login);
});

test('decideOverride ignores the label when the author re-applies it after a maintainer', () => {
  // Listed newest first: the decision must not depend on the API's ordering.
  const events = [
    issueEvent('labeled', AUTHOR, '2026-01-03T00:00:00Z'),
    issueEvent('unlabeled', AUTHOR, '2026-01-02T00:00:00Z'),
    issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z'),
  ];
  const decision = decideOverride({ labels: LABELS, events, author: AUTHOR });
  assert.strictEqual(decision.honored, false);
  assert.strictEqual(decision.actor, AUTHOR.login);
});

test('decideOverride ignores the label applied by a bot', () => {
  const events = [issueEvent('labeled', BOT, '2026-01-01T00:00:00Z', { type: 'Bot' })];
  const decision = decideOverride({ labels: LABELS, events, author: AUTHOR });
  assert.strictEqual(decision.honored, false);
  assert.strictEqual(decision.reason, 'its latest application was by @helper[bot] (account type: Bot)');
});

test('decideOverride ignores a present label without a labeled event and says why', () => {
  const events = [issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z', { label: 'api' })];
  assert.deepStrictEqual(decideOverride({ labels: LABELS, events, author: AUTHOR }), {
    honored: false,
    actor: null,
    reason: "no 'labeled' event was found for it",
  });
});

test('decideOverride ignores an absent label even with a past labeled event', () => {
  const events = [issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z')];
  const decision = decideOverride({ labels: [{ name: 'api' }], events, author: AUTHOR });
  assert.strictEqual(decision.honored, false);
  assert.strictEqual(decision.reason, 'the label is not on the pull request');
});

test('run publishes an honored override as output, notice, and step summary', async () => {
  const github = fakeGithub(LABELS, [[issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z')]]);
  const core = fakeCore();
  const decision = await run({ github, context: CONTEXT, core });
  const message = "Protobuf breaking-change findings are non-blocking: 'proto-breaking-change-approved' " +
    'was applied by @maintainer.';
  assert.strictEqual(decision.honored, true);
  assert.deepStrictEqual(core.outputs, { honored: 'true' });
  assert.deepStrictEqual(core.notices, [message]);
  assert.deepStrictEqual(core.summaryLines, [`${message}\n`]);
  assert.strictEqual(core.summaryWrites, 1);
  assert.deepStrictEqual(core.warnings, []);
  assert.deepStrictEqual(github.calls[0], {
    method: 'pulls.get',
    params: { owner: 'google', repo: 'xmc', pull_number: 42 },
  });
});

test('run warns about an ignored label and keeps the check blocking', async () => {
  const github = fakeGithub(LABELS, [[issueEvent('labeled', AUTHOR, '2026-01-01T00:00:00Z')]]);
  const core = fakeCore();
  await run({ github, context: CONTEXT, core });
  assert.deepStrictEqual(core.outputs, { honored: 'false' });
  assert.strictEqual(core.warnings.length, 1);
  assert.match(core.warnings[0], /^Ignoring 'proto-breaking-change-approved' because its latest application was by the pull request author/);
  assert.deepStrictEqual(core.summaryLines, []);
});

test('run does not list issue events when the label is absent', async () => {
  const github = fakeGithub([{ name: 'api' }], []);
  const core = fakeCore();
  await run({ github, context: CONTEXT, core });
  assert.deepStrictEqual(core.outputs, { honored: 'false' });
  assert.deepStrictEqual(github.calls.map(c => c.method), ['pulls.get']);
  assert.deepStrictEqual(core.warnings, []);
});

test('run follows next-page links so that the latest event on page 2 decides', async () => {
  const unrelated = Array.from({ length: 99 }, (_, i) =>
    issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z', { label: `other-${i}` }));
  const github = fakeGithub(LABELS, [
    [issueEvent('labeled', MAINTAINER, '2026-01-01T00:00:00Z'), ...unrelated],
    [issueEvent('labeled', AUTHOR, '2026-01-02T00:00:00Z')],
  ]);
  const core = fakeCore();
  const decision = await run({ github, context: CONTEXT, core });
  assert.strictEqual(decision.honored, false);
  assert.strictEqual(decision.actor, AUTHOR.login);
  const pages = github.calls.filter(c => c.method === 'issues.listEvents').map(c => c.params);
  assert.deepStrictEqual(pages.map(p => p.page), [1, 2]);
  assert.ok(pages.every(p => p.issue_number === 42 && p.per_page === 100));
});

test('run propagates pulls.get errors without publishing an output', async () => {
  const github = fakeGithub(LABELS, []);
  github.rest.pulls.get = async () => {
    throw new Error('API rate limit exceeded');
  };
  const core = fakeCore();
  await assert.rejects(run({ github, context: CONTEXT, core }), /API rate limit exceeded/);
  assert.deepStrictEqual(core.outputs, {});
});

test('run propagates issues.listEvents errors without publishing an output', async () => {
  const github = fakeGithub(LABELS, []);
  github.rest.issues.listEvents = async () => {
    throw new Error('Server Error');
  };
  const core = fakeCore();
  await assert.rejects(run({ github, context: CONTEXT, core }), /Server Error/);
  assert.deepStrictEqual(core.outputs, {});
  assert.deepStrictEqual(core.warnings, []);
});
