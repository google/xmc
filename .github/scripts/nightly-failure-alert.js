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
const { parseAbsenceFile } = require('./absence-schedule');

/**
 * Parses a flat text file (like OWNERS) containing usernames.
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
 * Reads OWNERS and resolves active owners by filtering out absent users from ABSENCE_LIST content.
 * @param {string} ownersPath
 * @param {string} absenceContent
 * @param {any} [core=null]
 * @param {Date} [currentDate=new Date()]
 * @returns {string[]} Active owner usernames.
 */
function resolveActiveOwners(ownersPath, absenceContent, core = null, currentDate = new Date()) {
  if (!fs.existsSync(ownersPath) && core) {
    core.warning(`Owners file not found at '${ownersPath}'.`);
  }
  const ownersContent = fs.existsSync(ownersPath) ? fs.readFileSync(ownersPath, 'utf8') : '';

  const { absentUsers, errors } = parseAbsenceFile(absenceContent || '', currentDate);
  if (core) {
    for (const err of errors) {
      core.warning(`Invalid ABSENCE_LIST entry ignored: ${err}`);
    }
  }
  const activeOwners = filterActiveOwners(parseUsernames(ownersContent), absentUsers);
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
 * Per-workflow alert settings. Each profile owns its issue label (so failures of
 * different nightly workflows are tracked in separate issues), the issue title and
 * heading, and the workflow-specific result rows of the issue table.
 */
const ALERT_PROFILES = {
  'examples-e2e': {
    labelName: 'nightly-failure',
    title: 'Nightly Test Suite Failure',
    heading: '🌙 Nightly E2E Test Suite Failed',
    description: 'The automated nightly test suite against the `xmc-nightly` deployment encountered a failure.',
    resultRows: ({ xid, triggerOutcome = 'unknown', verifyOutcome = 'unknown' }) => [
      ['Experiment XID', xid || 'N/A'],
      ['Trigger Action', triggerOutcome],
      ['Verification Assertions', verifyOutcome],
    ],
  },
  'unit-tests': {
    labelName: 'nightly-unit-test-failure',
    title: 'Nightly Uncached Unit Test Failure',
    heading: '🧪 Nightly Uncached Unit Tests Failed',
    description:
      'The nightly `unit-tests` run (backend Bazel tests with `--nocache_test_results`, plus the Angular UI and Helm chart suites) failed. ' +
      'If the backend job failed, post-submit runs reuse cached Bazel test results, so the failure may be a flaky or broken test they did not execute; ' +
      'check the workflow run to see which suite failed.',
    resultRows: ({ jobResults = [] }) => (jobResults.length > 0 ? jobResults : [['Jobs', 'unknown']]),
  },
};

const DEFAULT_ALERT_PROFILE = 'examples-e2e';

/**
 * Returns the alert profile registered under name, or throws for an unknown name.
 * @param {string} [name=DEFAULT_ALERT_PROFILE]
 * @returns {Object} Alert profile.
 */
function resolveAlertProfile(name) {
  const profileName = name || DEFAULT_ALERT_PROFILE;
  const profile = ALERT_PROFILES[profileName];
  if (!profile) {
    throw new Error(`Unknown ALERT_PROFILE '${profileName}'; expected one of: ${Object.keys(ALERT_PROFILES).join(', ')}`);
  }
  return profile;
}

/**
 * Formats the issue body markdown for nightly test suite failure alerts.
 * @param {Object} params
 * @returns {string} Formatted markdown body.
 */
function formatFailureIssueBody(params) {
  const {
    serverUrl = 'https://github.com',
    owner,
    repo,
    runId,
    sha,
    ref,
    eventName,
    associatedPr,
    activeOwners = [],
    profile = ALERT_PROFILES[DEFAULT_ALERT_PROFILE],
  } = params;
  const runUrl = `${serverUrl}/${owner}/${repo}/actions/runs/${runId}`;
  const commitUrl = `${serverUrl}/${owner}/${repo}/commit/${sha}`;
  const shortSha = sha ? sha.substring(0, 7) : 'unknown';
  const mentions = (activeOwners || []).map(o => `@${o}`).join(' ');

  let body = `### ${profile.heading}\n\n`;
  body += `${profile.description}\n\n`;
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
  for (const [label, value] of profile.resultRows(params)) {
    body += `| **${label}** | \`${value}\` |\n`;
  }
  body += `\n`;

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
  const profile = params.profile || ALERT_PROFILES[DEFAULT_ALERT_PROFILE];
  const title = `🚨 ${profile.title} (${today})`;
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
 * Upstream jobs whose non-success results are surfaced in the trigger outcome,
 * in reporting priority order.
 */
const PRECONDITION_JOBS = ['examples-import-check', 'build-example-images'];

/**
 * Returns true when a job result indicates a problem worth reporting.
 * @param {string|undefined} result
 * @returns {boolean}
 */
function isProblemResult(result) {
  return Boolean(result) && result !== 'success' && result !== 'skipped';
}

/**
 * Resolves the triggerOutcome field from GitHub Actions needsContext.
 *
 * Lists every failed precondition job (examples import check, image build) and,
 * when any is present, also the workload result if it failed too, so one failure
 * cannot mask another. Falls back to the plain workload trigger result
 * (results aggregation is reported separately, as the verification outcome).
 *
 * @param {Object} needsContext
 * @returns {string}
 */
function resolveTriggerOutcome(needsContext) {
  const workload =
    needsContext?.['run-and-verify-demo-workload']?.result ||
    needsContext?.['run-and-verify-examples']?.result;
  const problems = PRECONDITION_JOBS.map(job => [job, needsContext?.[job]?.result])
    .filter(([, result]) => isProblemResult(result))
    .map(([job, result]) => `${result} (${job})`);
  if (problems.length === 0) {
    return workload || 'unknown';
  }
  if (isProblemResult(workload)) {
    problems.push(`${workload} (workloads)`);
  }
  return problems.join(', ');
}

/**
 * Lists the result of every upstream job as [jobName, result] rows.
 * @param {Object} needsContext
 * @returns {Array<[string, string]>}
 */
function resolveJobResults(needsContext) {
  return Object.entries(needsContext || {}).map(([job, value]) => [job, value?.result || 'unknown']);
}

/**
 * Constructs the parameter dictionary for alert issue creation and comment updates.
 * @param {Object} context
 * @param {string[]} activeOwners
 * @param {Object|null} associatedPr
 * @param {Object} needsContext
 * @param {Object} [profile=ALERT_PROFILES[DEFAULT_ALERT_PROFILE]]
 * @returns {Object}
 */
function buildAlertParams(context, activeOwners, associatedPr, needsContext, profile = ALERT_PROFILES[DEFAULT_ALERT_PROFILE]) {
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
    jobResults: resolveJobResults(needsContext),
    activeOwners,
    profile,
  };
}

/**
 * Main orchestrator: Coordinates parsing OWNERS, resolving PR context, and posting/updating alert issue.
 * @param {Object} params
 * @param {any} params.github - Octokit client.
 * @param {any} params.context - Workflow execution context.
 * @param {any} params.core - Actions core logger.
 * @param {string} [params.ownersPath='OWNERS'] - Path to OWNERS file.
 * @param {string} [params.absenceContent=process.env.ABSENCE_LIST || ''] - Raw ABSENCE_LIST content.
 * @param {string} [params.profileName=process.env.ALERT_PROFILE] - Alert profile name (default: examples-e2e).
 */
async function run({
  github,
  context,
  core,
  ownersPath = 'OWNERS',
  absenceContent = process.env.ABSENCE_LIST || '',
  profileName = process.env.ALERT_PROFILE,
}) {
  const profile = resolveAlertProfile(profileName);
  const { owner, repo } = context.repo;
  const activeOwners = resolveActiveOwners(ownersPath, absenceContent, core);
  const needsContext = parseNeedsContext(core);
  const associatedPr = await lookupAssociatedPullRequest(github, owner, repo, context.sha, core);
  const alertParams = buildAlertParams(context, activeOwners, associatedPr, needsContext, profile);

  const existingIssue = await findExistingNightlyFailureIssue(github, owner, repo, profile.labelName, core);
  return postOrUpdateAlertIssue(github, owner, repo, existingIssue, alertParams, profile.labelName, core);
}

module.exports = {
  ALERT_PROFILES,
  resolveAlertProfile,
  resolveJobResults,
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
