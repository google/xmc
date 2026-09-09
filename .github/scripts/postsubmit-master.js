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
 * Generates the deduplication marker for a given commit SHA.
 * @param {string} headSha - Full commit SHA.
 * @returns {string} HTML comment marker.
 */
function getDedupMarker(headSha) {
  return `<!-- xmc-postsubmit-master-failure: ${headSha} -->`;
}

/**
 * Builds the structured job result array from the GitHub Actions `needs` context.
 * @param {Record<string, { result?: string }>} needsContext - Needs context dictionary from workflow.
 * @returns {Array<{ name: string, jobId: string, result: string }>}
 */
function buildJobResults(needsContext = {}) {
  const safeContext = needsContext || {};
  return [
    {
      name: 'Backend & Go Unit Tests',
      jobId: 'run-backend-tests',
      result: safeContext['run-backend-tests']?.result || 'unknown',
    },
    {
      name: 'Angular UI Unit Tests',
      jobId: 'run-ui-tests',
      result: safeContext['run-ui-tests']?.result || 'unknown',
    },
    {
      name: 'Helm Chart Configuration Tests',
      jobId: 'run-chart-tests',
      result: safeContext['run-chart-tests']?.result || 'unknown',
    },
    {
      name: 'KinD & Playwright Headless E2E',
      jobId: 'run-e2e-tests',
      result: safeContext['run-e2e-tests']?.result || 'unknown',
    },
  ];
}

/**
 * Filters and returns the list of failed or cancelled jobs.
 * @param {Array<{ name: string, jobId: string, result: string }>} jobResults
 * @returns {Array<{ name: string, jobId: string, result: string }>}
 */
function getFailedJobs(jobResults) {
  if (!Array.isArray(jobResults)) return [];
  const failureStates = new Set(['failure', 'timed_out', 'cancelled']);
  return jobResults.filter(j => failureStates.has(j.result));
}

/**
 * Formats the markdown comment body for post-submit failure alerts on a PR.
 * @param {Object} params
 * @param {string} params.headSha
 * @param {string} params.author
 * @param {string} [params.serverUrl]
 * @param {string} params.owner
 * @param {string} params.repo
 * @param {string|number} params.runId
 * @param {Array<{ name: string, result: string }>} params.jobResults
 * @returns {string} Markdown comment body.
 */
function formatFailureCommentBody({
  headSha,
  author,
  serverUrl = 'https://github.com',
  owner,
  repo,
  runId,
  jobResults,
}) {
  const shortSha = headSha ? headSha.substring(0, 7) : 'unknown';
  const runUrl = `${serverUrl}/${owner}/${repo}/actions/runs/${runId}`;
  const commitUrl = `${serverUrl}/${owner}/${repo}/commit/${headSha}`;
  const dedupMarker = getDedupMarker(headSha);

  let commentBody = `${dedupMarker}\n`;
  commentBody += `### 🚨 Post-Submit Test Regression on \`main\`\n\n`;
  commentBody += `Hello @${author}, one or more automated post-submit test suites failed after merging commit [\`${shortSha}\`](${commitUrl}) into \`main\`.\n\n`;
  commentBody += `| Test Suite | Result |\n`;
  commentBody += `| :--- | :---: |\n`;

  for (const j of jobResults) {
    const icon =
      j.result === 'success' ? '✅' :
      j.result === 'skipped' ? '⚪' : '❌';
    commentBody += `| **${j.name}** | ${icon} \`${j.result}\` |\n`;
  }

  commentBody += `\n- **Full Workflow Logs**: [View CI Run Details](${runUrl})\n\n`;
  commentBody += `> **Action Required:** Please investigate the failures above and submit a fix or revert to restore \`main\` health.\n`;

  return commentBody;
}

/**
 * Evaluates the results of the post-submit jobs and posts a notification comment on the associated merged PR if failures occur.
 * @param {Object} params
 * @param {any} params.github - Octokit client.
 * @param {any} params.context - GitHub workflow run context.
 * @param {any} params.core - Actions core library.
 * @param {Object} [params.needsContext] - Optional explicit needs context (defaults to process.env.NEEDS_CONTEXT).
 */
async function run({ github, context, core, needsContext }) {
  let parsedNeeds = needsContext;
  if (!parsedNeeds) {
    try {
      parsedNeeds = JSON.parse(process.env.NEEDS_CONTEXT || '{}');
    } catch (err) {
      core.warning(`Failed to parse process.env.NEEDS_CONTEXT: ${err.message}`);
      parsedNeeds = {};
    }
  }

  const headSha = context.sha;
  const owner = context.repo.owner;
  const repo = context.repo.repo;

  core.info(`Evaluating post-submit job statuses for commit SHA: ${headSha}`);

  const jobResults = buildJobResults(parsedNeeds);
  const failedJobs = getFailedJobs(jobResults);

  if (failedJobs.length === 0) {
    core.info('All post-submit master pipeline jobs succeeded.');
    return { status: 'success', failedCount: 0 };
  }

  core.warning(`Detected ${failedJobs.length} failing job(s) on main.`);

  // 1. Look up associated merged Pull Request
  const { data: prs } = await github.rest.repos.listPullRequestsAssociatedWithCommit({
    owner,
    repo,
    commit_sha: headSha,
  });

  if (!prs || prs.length === 0) {
    core.warning(`No PR associated with commit ${headSha} (direct push or manual trigger).`);
    return { status: 'no_pr', headSha };
  }

  const pr = prs[0];
  const prAuthor = pr.user?.login || 'unknown';
  core.info(`Found associated PR #${pr.number} (Author: @${prAuthor})`);

  // 2. Check if comment already exists for this exact commit SHA
  const { data: comments } = await github.rest.issues.listComments({
    owner,
    repo,
    issue_number: pr.number,
    per_page: 100,
  });

  const dedupMarker = getDedupMarker(headSha);
  if (comments && comments.some(c => c.body && c.body.includes(dedupMarker))) {
    core.info(`Failure comment already posted on PR #${pr.number} for commit ${headSha}. Skipping.`);
    return { status: 'already_commented', prNumber: pr.number };
  }

  // 3. Build and post markdown comment table
  const commentBody = formatFailureCommentBody({
    headSha,
    author: prAuthor,
    serverUrl: context.serverUrl || 'https://github.com',
    owner,
    repo,
    runId: context.runId,
    jobResults,
  });

  await github.rest.issues.createComment({
    owner,
    repo,
    issue_number: pr.number,
    body: commentBody,
  });

  core.info(`Successfully posted failure report comment to PR #${pr.number}`);
  return { status: 'comment_posted', prNumber: pr.number, failedJobs: failedJobs.map(j => j.name) };
}

module.exports = {
  getDedupMarker,
  buildJobResults,
  getFailedJobs,
  formatFailureCommentBody,
  run,
};
