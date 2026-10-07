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

/**
 * @fileoverview Decides whether the protobuf breaking-change check of a pull request is
 * non-blocking. The check always runs; an override label applied by a maintainer only keeps
 * breaking changes from failing the job (see "Protobuf Breaking Changes" in docs/developer.md).
 */

/** Label that makes protobuf breaking-change findings non-blocking. */
const OVERRIDE_LABEL = 'proto-breaking-change-approved';

/**
 * Decides whether the override label is honored. Only the most recent `labeled` event of the
 * label counts, and it must come from a user account other than the pull request author, so
 * that neither the author nor a bot can approve a breaking change. Accounts are compared by
 * id because logins can be renamed and reused; if either id is missing, the label is ignored.
 * @param {Object} params
 * @param {Array<{ name: string }>} params.labels - Current labels of the pull request.
 * @param {Array<Object>} params.events - Issue events of the pull request.
 * @param {?{ login: string, id: number }} params.author - Pull request author.
 * @param {string} [params.label] - Override label name (defaults to OVERRIDE_LABEL).
 * @returns {{ honored: boolean, actor: (string|null), reason: string }}
 */
function decideOverride({ labels, events, author, label = OVERRIDE_LABEL }) {
  if (!labels.some(l => l.name === label)) {
    return { honored: false, actor: null, reason: 'the label is not on the pull request' };
  }
  // ISO 8601 UTC timestamps compare chronologically as strings; ties go to the later-listed event.
  const latest = events
    .filter(e => e.event === 'labeled' && e.label?.name === label)
    .reduce((newest, e) => (!newest || e.created_at >= newest.created_at ? e : newest), null);
  if (!latest) {
    return { honored: false, actor: null, reason: "no 'labeled' event was found for it" };
  }
  const actor = latest.actor?.login ?? 'unknown';
  const type = latest.actor?.type ?? 'unknown';
  if (type !== 'User') {
    const reason = `its latest application was by @${actor} (account type: ${type})`;
    return { honored: false, actor, reason };
  }
  // Fail closed: a missing id must count neither as the author nor as another account.
  if (latest.actor.id == null || author?.id == null) {
    const reason = `the account id of @${actor} or of the pull request author is missing`;
    return { honored: false, actor, reason };
  }
  if (latest.actor.id === author.id) {
    const reason = `its latest application was by the pull request author (@${actor})`;
    return { honored: false, actor, reason };
  }
  return { honored: true, actor, reason: `its latest application was by @${actor}` };
}

/**
 * Publishes the override decision for the pull request of a `pull_request` event as the
 * `honored` step output. Labels and label events are read live rather than from the event
 * payload, so re-running the job honors a newly added label. API errors propagate and fail the
 * step without setting the output, which keeps the check blocking.
 * @param {Object} params
 * @param {any} params.github - Octokit client.
 * @param {any} params.context - GitHub workflow run context.
 * @param {any} params.core - Actions core library.
 * @returns {Promise<{ honored: boolean, actor: (string|null), reason: string }>}
 */
async function run({ github, context, core }) {
  const repo = { owner: context.repo.owner, repo: context.repo.repo };
  const number = context.payload.pull_request.number;
  const { data: pullRequest } = await github.rest.pulls.get({ ...repo, pull_number: number });
  const labels = pullRequest.labels || [];
  const labelPresent = labels.some(l => l.name === OVERRIDE_LABEL);
  // The issue events API is covered by the workflow's pull-requests: read permission.
  const params = { ...repo, issue_number: number, per_page: 100 };
  const events = labelPresent
    ? await github.paginate(github.rest.issues.listEvents, params)
    : [];
  const decision = decideOverride({ labels, events, author: pullRequest.user });
  core.setOutput('honored', String(decision.honored));
  if (decision.honored) {
    const message = 'Protobuf breaking-change findings are non-blocking: ' +
      `'${OVERRIDE_LABEL}' was applied by @${decision.actor}.`;
    core.notice(message);
    await core.summary.addRaw(message, true).write();
  } else if (labelPresent) {
    core.warning(`Ignoring '${OVERRIDE_LABEL}' because ${decision.reason}; only a user other ` +
      'than the pull request author can approve a breaking change.');
  }
  return decision;
}

module.exports = {
  OVERRIDE_LABEL,
  decideOverride,
  run,
};
