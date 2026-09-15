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
 * Formats the issue body markdown for nightly test suite failure alerts.
 * @param {Object} params
 * @param {string} params.serverUrl
 * @param {string} params.owner
 * @param {string} params.repo
 * @param {string|number} params.runId
 * @param {string} params.sha
 * @param {string} params.ref
 * @param {string} params.eventName
 * @param {string} [params.xid]
 * @param {string} [params.domainName]
 * @param {string} [params.triggerOutcome]
 * @param {string} [params.verifyOutcome]
 * @param {string[]} [params.activeOwners]
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
  body += `| **Branch / Ref** | \`${ref || 'refs/heads/main'}\` |\n`;
  body += `| **Trigger Event** | \`${eventName || 'schedule'}\` |\n`;
  body += `| **Experiment XID** | \`${xid || 'N/A'}\` |\n`;
  body += `| **Trigger Action** | \`${triggerOutcome}\` |\n`;
  body += `| **Verification Assertions** | \`${verifyOutcome}\` |\n`;
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
  const failureBody = formatFailureIssueBody(params);
  return `### 🚨 Recurring Nightly Test Failure Detected\n\n${failureBody}`;
}

/**
 * Main orchestrator: Reads OWNERS/ABSENCE.md, checks for open alert issues, and creates/updates an issue.
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
  const owner = context.repo.owner;
  const repo = context.repo.repo;

  // 1. Read and parse OWNERS
  if (!fs.existsSync(ownersPath)) {
    core.warning(`Owners file not found at '${ownersPath}'.`);
  }
  const ownersContent = fs.existsSync(ownersPath) ? fs.readFileSync(ownersPath, 'utf8') : '';
  const owners = parseUsernames(ownersContent);

  // 2. Read and parse ABSENCE.md
  const absenceContent = fs.existsSync(absencePath) ? fs.readFileSync(absencePath, 'utf8') : '';
  const absences = parseUsernames(absenceContent);

  const activeOwners = filterActiveOwners(owners, absences);
  core.info(`Active OWNERS to notify (${activeOwners.length}): ${activeOwners.join(', ') || 'none'}`);

  // 3. Extract workflow context & step outcomes
  let needsContext = {};
  try {
    needsContext = JSON.parse(process.env.NEEDS_CONTEXT || '{}');
  } catch (err) {
    core.warning(`Failed to parse NEEDS_CONTEXT: ${err.message}`);
  }

  const triggerOutcome = needsContext['trigger-demo-workload']?.result || 'unknown';
  const verifyOutcome = needsContext['validate-workload-results']?.result || 'unknown';
  const xid = process.env.XID || '';

  const params = {
    serverUrl: context.serverUrl || 'https://github.com',
    owner,
    repo,
    runId: context.runId,
    sha: context.sha,
    ref: context.ref,
    eventName: context.eventName,
    xid,
    triggerOutcome,
    verifyOutcome,
    activeOwners,
  };

  // 4. Query for existing open issue with label 'nightly-failure'
  const LABEL_NAME = 'nightly-failure';
  let existingIssue = null;
  try {
    const { data: issues } = await github.rest.issues.listForRepo({
      owner,
      repo,
      state: 'open',
      labels: LABEL_NAME,
      per_page: 10,
    });
    if (issues && issues.length > 0) {
      existingIssue = issues[0];
    }
  } catch (err) {
    core.warning(`Failed to query open issues with label '${LABEL_NAME}': ${err.message}`);
  }

  // 5. Create new issue or comment on existing open issue
  if (existingIssue) {
    core.info(`Found open nightly failure issue #${existingIssue.number}. Adding comment.`);
    const commentBody = formatRecurringCommentBody(params);
    await github.rest.issues.createComment({
      owner,
      repo,
      issue_number: existingIssue.number,
      body: commentBody,
    });
    core.info(`Successfully added failure comment to issue #${existingIssue.number}`);
    return { status: 'comment_created', issueNumber: existingIssue.number };
  } else {
    const today = new Date().toISOString().split('T')[0];
    const title = `🚨 Nightly Test Suite Failure (${today})`;
    const body = formatFailureIssueBody(params);

    core.info(`Creating new nightly failure issue: "${title}"`);
    const { data: newIssue } = await github.rest.issues.create({
      owner,
      repo,
      title,
      body,
      labels: [LABEL_NAME, 'bug'],
    });
    core.info(`Successfully created issue #${newIssue.number}`);
    return { status: 'issue_created', issueNumber: newIssue.number };
  }
}

module.exports = {
  parseUsernames,
  filterActiveOwners,
  formatFailureIssueBody,
  formatRecurringCommentBody,
  run,
};
