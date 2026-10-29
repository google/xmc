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

const fs = require('fs');

/**
 * Parses a flat text file (like OWNERS or ABSENCE.md) containing usernames.
 * Strips inline comments, markdown bullets/prefixes, and trims whitespace.
 * @param {string} content - Raw file content.
 * @returns {string[]} Array of parsed usernames.
 */
function parseUsernames(content) {
  return (content || '')
    .split(/\r?\n/)
    .map(line => line.split('#')[0].trim())
    .map(line => line.replace(/^[-*+@\s]+/, '').trim())
    .filter(Boolean);
}

/**
 * Filters out absent users from the list of repository owners.
 * Comparison is case-insensitive.
 * @param {string[]} owners - Array of owner usernames.
 * @param {string[]} absences - Array of absent usernames.
 * @returns {string[]} Filtered list of active owners.
 */
function filterActiveOwners(owners = [], absences = []) {
  const absentSet = new Set((absences || []).map(u => (u || '').toLowerCase()));
  return (owners || []).filter(o => !absentSet.has((o || '').toLowerCase()));
}

/**
 * Reads and resolves active owners from OWNERS and ABSENCE.md files.
 * @param {string} ownersPath
 * @param {string} absencePath
 * @param {any} core
 * @returns {string[]} Active owner usernames.
 */
function resolveActiveOwners(ownersPath, absencePath, core) {
  if (!fs.existsSync(ownersPath) && core) {
    core.warning(`Owners file not found at '${ownersPath}'.`);
  }
  const ownersContent = fs.existsSync(ownersPath) ? fs.readFileSync(ownersPath, 'utf8') : '';
  const absenceContent = fs.existsSync(absencePath) ? fs.readFileSync(absencePath, 'utf8') : '';

  const activeOwners = filterActiveOwners(parseUsernames(ownersContent), parseUsernames(absenceContent));
  if (core) {
    core.info(`Active OWNERS to notify (${activeOwners.length}): ${activeOwners.join(', ') || 'none'}`);
  }
  return activeOwners;
}

/**
 * Sanitizes a PR title for safe inclusion in markdown links and tables.
 * Strips newlines, escapes table pipes, brackets, asterisks, and underscores.
 * @param {string} title - Raw PR title.
 * @returns {string} Sanitized PR title.
 */
function sanitizeMarkdownTitle(title) {
  if (!title || typeof title !== 'string') {
    return '';
  }
  return title
    .replace(/[\r\n]+/g, ' ')
    .replace(/\|/g, '\\|')
    .replace(/\[/g, '\\[')
    .replace(/\]/g, '\\]')
    .replace(/\*/g, '\\*')
    .replace(/_/g, '\\_')
    .trim();
}

/**
 * Formats the issue body markdown for nightly test suite failure alerts.
 * @param {Object} params
 * @returns {string} Formatted markdown body.
 */
function formatFailureIssueBody({
  serverUrl = 'https://github.com',
  owner,
  repo,
  runId,
  sha,
  ref,
  eventName,
  xid,
  associatedPr,
  triggerOutcome = 'unknown',
  verifyOutcome = 'unknown',
  activeOwners = [],
}) {
  const runUrl = `${serverUrl}/${owner}/${repo}/actions/runs/${runId}`;
  const commitUrl = `${serverUrl}/${owner}/${repo}/commit/${sha}`;
  const shortSha = sha ? sha.substring(0, 7) : 'unknown';
  const mentions = (activeOwners || []).map(o => `@${o}`).join(' ');

  let body = `### 🌙 Nightly E2E Test Suite Failed\n\n`;
  body += `The automated nightly test suite against the \`xmc-nightly\` deployment encountered a failure.\n\n`;
  body += `| Metric / Step | Status / Value |\n`;
  body += `| :--- | :--- |\n`;
  body += `| **Workflow Run** | [View Run Details](${runUrl}) |\n`;
  body += `| **Commit** | [\`${shortSha}\`](${commitUrl}) |\n`;

  if (associatedPr && associatedPr.number) {
    const prUrl = associatedPr.url || `${serverUrl}/${owner}/${repo}/pull/${associatedPr.number}`;
    const cleanTitle = sanitizeMarkdownTitle(associatedPr.title);
    const prTitle = cleanTitle ? `: *${cleanTitle}*` : '';
    body += `| **Associated PR** | [PR #${associatedPr.number}${prTitle}](${prUrl}) |\n`;
  }

  body += `| **Branch / Ref** | \`${ref || 'refs/heads/main'}\` |\n`;
  body += `| **Trigger Event** | \`${eventName || 'schedule'}\` |\n`;
  body += `| **Experiment XID** | \`${xid || 'N/A'}\` |\n`;
  body += `| **Trigger Action** | \`${triggerOutcome}\` |\n`;
  body += `| **Verification Assertions** | \`${verifyOutcome}\` |\n\n`;

  if (mentions) {
    body += `cc ${mentions} — please investigate the nightly failure when possible.\n`;
  } else {
    body += `> **Notice:** No active owners found to notify.\n`;
  }
  return body;
}

/**
 * Formats a comment body for recurring nightly failures when an open issue exists.
 * @param {Object} params
 * @returns {string} Formatted markdown comment.
 */
function formatRecurringCommentBody(params) {
  return `### 🚨 Recurring Nightly Test Failure Detected\n\n${formatFailureIssueBody(params)}`;
}

/**
 * Looks up the merged Pull Request associated with a commit SHA.
 * @param {any} github
 * @param {string} owner
 * @param {string} repo
 * @param {string} sha
 * @param {any} core
 * @returns {Promise<{ number: number, title: string, url: string } | null>}
 */
async function lookupAssociatedPullRequest(github, owner, repo, sha, core) {
  if (!sha || !github?.rest?.repos?.listPullRequestsAssociatedWithCommit) {
    return null;
  }
  try {
    const { data: prs } = await github.rest.repos.listPullRequestsAssociatedWithCommit({
      owner,
      repo,
      commit_sha: sha,
    });
    if (prs && prs.length > 0) {
      if (core) core.info(`Found associated PR #${prs[0].number}: "${prs[0].title}"`);
      return { number: prs[0].number, title: prs[0].title, url: prs[0].html_url };
    }
  } catch (err) {
    if (core) core.warning(`Failed to look up associated PR for commit ${sha}: ${err.message}`);
  }
  return null;
}

/**
 * Finds an existing open issue with the specified label (excluding PRs).
 * @param {any} github
 * @param {string} owner
 * @param {string} repo
 * @param {string} labelName
 * @param {any} core
 * @returns {Promise<any | null>}
 */
async function findExistingNightlyFailureIssue(github, owner, repo, labelName, core) {
  try {
    const { data: issues } = await github.rest.issues.listForRepo({
      owner,
      repo,
      state: 'open',
      labels: labelName,
      per_page: 10,
    });
    return (issues || []).find(issue => !issue.pull_request) || null;
  } catch (err) {
    if (core) core.warning(`Failed to query open issues with label '${labelName}': ${err.message}`);
    return null;
  }
}

/**
 * Creates a new failure issue or posts a recurring comment on an existing issue.
 * @param {any} github
 * @param {string} owner
 * @param {string} repo
 * @param {any | null} existingIssue
 * @param {Object} params
 * @param {string} labelName
 * @param {any} core
 * @returns {Promise<{ status: string, issueNumber: number }>}
 */
async function postOrUpdateAlertIssue(github, owner, repo, existingIssue, params, labelName, core) {
  if (existingIssue) {
    if (core) core.info(`Found open nightly failure issue #${existingIssue.number}. Adding comment.`);
    await github.rest.issues.createComment({
      owner,
      repo,
      issue_number: existingIssue.number,
      body: formatRecurringCommentBody(params),
    });
    if (core) core.info(`Successfully added failure comment to issue #${existingIssue.number}`);
    return { status: 'comment_created', issueNumber: existingIssue.number };
  }

  const today = new Date().toISOString().split('T')[0];
  const title = `🚨 Nightly Test Suite Failure (${today})`;
  if (core) core.info(`Creating new nightly failure issue: "${title}"`);

  const { data: newIssue } = await github.rest.issues.create({
    owner,
    repo,
    title,
    body: formatFailureIssueBody(params),
    labels: [labelName, 'bug'],
  });
  if (core) core.info(`Successfully created issue #${newIssue.number}`);
  return { status: 'issue_created', issueNumber: newIssue.number };
}

/**
 * Parses JSON needs context safely from environment.
 * @param {any} core
 * @returns {Object}
 */
function parseNeedsContext(core) {
  try {
    return JSON.parse(process.env.NEEDS_CONTEXT || '{}');
  } catch (err) {
    if (core) core.warning(`Failed to parse NEEDS_CONTEXT: ${err.message}`);
    return {};
  }
}

/**
 * Upstream jobs whose non-success results take precedence in the trigger outcome,
 * in reporting priority order.
 */
const PRECONDITION_JOBS = ['examples-import-check', 'build-example-images'];

/**
 * Resolves the triggerOutcome field from GitHub Actions needsContext,
 * surfacing precondition job failures (examples import check, image build)
 * ahead of the workload trigger result.
 *
 * @param {Object} needsContext
 * @returns {string}
 */
function resolveTriggerOutcome(needsContext) {
  for (const job of PRECONDITION_JOBS) {
    const result = needsContext?.[job]?.result;
    if (result && result !== 'success' && result !== 'skipped') {
      return `${result} (${job})`;
    }
  }
  return (
    needsContext?.['run-and-verify-demo-workload']?.result ||
    needsContext?.['run-and-verify-examples']?.result ||
    'unknown'
  );
}

/**
 * Constructs the parameter dictionary for alert issue creation and comment updates.
 * @param {Object} context
 * @param {string[]} activeOwners
 * @param {Object|null} associatedPr
 * @param {Object} needsContext
 * @returns {Object}
 */
function buildAlertParams(context, activeOwners, associatedPr, needsContext) {
  const { owner, repo } = context.repo;
  const rawXid = process.env.XID || '';
  return {
    serverUrl: process.env.GITHUB_SERVER_URL || context.serverUrl || 'https://github.com',
    owner,
    repo,
    runId: context.runId,
    sha: context.sha,
    ref: context.ref,
    eventName: context.eventName,
    xid: rawXid === '{}' ? '' : rawXid,
    associatedPr,
    triggerOutcome: resolveTriggerOutcome(needsContext),
    verifyOutcome:
      needsContext?.['run-and-verify-demo-workload']?.result ||
      needsContext?.['aggregate-results']?.result ||
      'unknown',
    activeOwners,
  };
}

/**
 * Main orchestrator: Coordinates parsing OWNERS, resolving PR context, and posting/updating alert issue.
 * @param {Object} params
 * @param {any} params.github - Octokit client.
 * @param {any} params.context - Workflow execution context.
 * @param {any} params.core - Actions core logger.
 * @param {string} [params.ownersPath='OWNERS'] - Path to OWNERS file.
 * @param {string} [params.absencePath='ABSENCE.md'] - Path to ABSENCE.md file.
 */
async function run({
  github,
  context,
  core,
  ownersPath = 'OWNERS',
  absencePath = 'ABSENCE.md',
}) {
  const { owner, repo } = context.repo;
  const activeOwners = resolveActiveOwners(ownersPath, absencePath, core);
  const needsContext = parseNeedsContext(core);
  const associatedPr = await lookupAssociatedPullRequest(github, owner, repo, context.sha, core);
  const alertParams = buildAlertParams(context, activeOwners, associatedPr, needsContext);

  const LABEL_NAME = 'nightly-failure';
  const existingIssue = await findExistingNightlyFailureIssue(github, owner, repo, LABEL_NAME, core);
  return postOrUpdateAlertIssue(github, owner, repo, existingIssue, alertParams, LABEL_NAME, core);
}

module.exports = {
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
  resolveTriggerOutcome,
  buildAlertParams,
  run,
};
