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

const childProcess = require('child_process');

// Maximum execution duration (60s) for git child processes (range-diff, fetch) before timing out to prevent hanging runner jobs.
const GIT_EXEC_TIMEOUT_MS = 60000;

// Maximum stdout buffer size (10MB) for git range-diff to accommodate large multi-commit diffs without memory exhaustion.
const GIT_MAX_BUFFER_BYTES = 10 * 1024 * 1024;

const SHA_REGEX = /^[0-9a-f]{7,40}$/i;
const REF_REGEX = /^[a-zA-Z0-9_./-]+$/;

/**
 * Pure function: Validates git revision and branch reference formatting.
 * @param {string} baseRef - Target base branch name.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 */
function validateGitRefs(baseRef, approvedSha, currentSha) {
  if (!baseRef || !REF_REGEX.test(baseRef) || baseRef.startsWith('-') || baseRef.includes('..')) {
    throw new Error(`Invalid base reference: "${baseRef}"`);
  }
  if (!approvedSha || !SHA_REGEX.test(approvedSha)) {
    throw new Error(`Invalid approved commit SHA: "${approvedSha}"`);
  }
  if (!currentSha || !SHA_REGEX.test(currentSha)) {
    throw new Error(`Invalid current commit SHA: "${currentSha}"`);
  }
}

/**
 * Shell Helper: Resolves commit ranges for git range-diff.
 * Handles stacked PRs (/gh-stack) where the base branch itself was rebased.
 * @param {string} baseRef - Target base branch name.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {{ approvedRange: string, currentRange: string }}
 */
function resolveCommitRanges(baseRef, approvedSha, currentSha, execOptions = {}) {
  const currentRange = `origin/${baseRef}..${currentSha}`;
  let approvedRange = `origin/${baseRef}..${approvedSha}`;
  const opts = { ...execOptions, encoding: 'utf8', stdio: ['pipe', 'pipe', 'ignore'] };

  try {
    const prCommitCountStr = childProcess.execFileSync(
      'git',
      ['rev-list', '--count', currentRange],
      opts
    ).trim();
    const prCommitCount = parseInt(prCommitCountStr, 10) || 0;

    if (prCommitCount > 0) {
      childProcess.execFileSync('git', ['rev-parse', '--verify', `${approvedSha}~${prCommitCount}`], opts);
      const approvedBaseCountStr = childProcess.execFileSync(
        'git',
        ['rev-list', '--count', approvedRange],
        opts
      ).trim();
      const approvedBaseCount = parseInt(approvedBaseCountStr, 10) || 0;

      // If approved range contains extra commits from a rebased parent branch in /gh-stack,
      // scope the approved range to the PR layer commit count.
      if (approvedBaseCount > prCommitCount) {
        approvedRange = `${approvedSha}~${prCommitCount}..${approvedSha}`;
      }
    }
  } catch (error) {
    // Fall back to default origin/${baseRef}..${approvedSha}
    console.log(`[incremental-approval] Failed to resolve advanced commit range, falling back: ${error.message}`);
  }

  return { approvedRange, currentRange };
}

/**
 * Shell Helper: Executes git range-diff between approved commit and current commit relative to base.
 * @param {string} baseRef - Target base branch name.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @returns {string} Output of git range-diff.
 */
function runGitRangeDiff(baseRef, approvedSha, currentSha) {
  validateGitRefs(baseRef, approvedSha, currentSha);

  if (approvedSha === currentSha) {
    return '';
  }

  const execOptions = {
    encoding: 'utf8',
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS
  };

  const execute = () => {
    const { approvedRange, currentRange } = resolveCommitRanges(baseRef, approvedSha, currentSha, execOptions);
    if (approvedRange === currentRange) {
      return '';
    }
    return childProcess.execFileSync('git', ['range-diff', '--no-color', approvedRange, currentRange], execOptions);
  };

  try {
    return execute();
  } catch (initialError) {
    try {
      const fetchArgs = ['fetch', 'origin', `+refs/heads/${baseRef}:refs/remotes/origin/${baseRef}`, approvedSha, currentSha];
      const fetchEnv = { ...process.env };
      const token = process.env.GITHUB_TOKEN;
      if (token) {
        const authHeader = `Authorization: Basic ${Buffer.from(`x-access-token:${token}`).toString('base64')}`;
        fetchEnv.GIT_CONFIG_COUNT = '1';
        fetchEnv.GIT_CONFIG_KEY_0 = 'http.extraheader';
        fetchEnv.GIT_CONFIG_VALUE_0 = authHeader;
      }
      childProcess.execFileSync(
        'git',
        fetchArgs,
        { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], timeout: GIT_EXEC_TIMEOUT_MS, env: fetchEnv }
      );
      return execute();
    } catch (fallbackError) {
      const stderr = fallbackError.stderr ? fallbackError.stderr.toString().trim() : fallbackError.message;
      throw new Error(`Failed to execute git range-diff: ${stderr || fallbackError.message}`);
    }
  }
}

/**
 * Shell Helper: Calculates line additions and deletions for a single commit.
 * @param {string} commitSha - Commit SHA to inspect.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {{ additions: number, deletions: number, totalDelta: number }}
 */
function getCommitLineDelta(commitSha, execOptions = {}) {
  if (!commitSha || !SHA_REGEX.test(commitSha)) {
    console.log('[incremental-approval] Invalid commit SHA: "' + commitSha + '"');
    return { additions: 0, deletions: 0, totalDelta: 0 };
  }

  const opts = {
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS,
    ...execOptions,
    encoding: 'utf8',
    stdio: ['pipe', 'pipe', 'ignore']
  };

  try {
    const numstat = childProcess.execFileSync('git', ['diff-tree', '--numstat', '-r', '--no-renames', commitSha], opts);
    let additions = 0;
    let deletions = 0;
    for (const line of numstat.split(/\r?\n/)) {
      const parts = line.trim().split(/\s+/);
      if (parts.length >= 2) {
        const a = parseInt(parts[0], 10);
        const d = parseInt(parts[1], 10);
        if (!isNaN(a)) additions += a;
        if (!isNaN(d)) deletions += d;
      }
    }
    return { additions, deletions, totalDelta: additions + deletions };
  } catch (error) {
    console.log(`[incremental-approval] Failed to get line delta for commit ${commitSha}: ${error.message}`);
    return { additions: 0, deletions: 0, totalDelta: 0 };
  }
}

/**
 * Shell Helper: Aggregates line additions and deletions across all newly added commits.
 * @param {Array<string>} addedCommitShas - List of added commit SHAs.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {{ additions: number, deletions: number, totalDelta: number }}
 */
function computeAddedCommitsDelta(addedCommitShas, execOptions = {}) {
  let additions = 0;
  let deletions = 0;
  if (!Array.isArray(addedCommitShas)) {
    return { additions, deletions, totalDelta: 0 };
  }
  for (const sha of addedCommitShas) {
    const delta = getCommitLineDelta(sha, execOptions);
    additions += delta.additions;
    deletions += delta.deletions;
  }
  return { additions, deletions, totalDelta: additions + deletions };
}

module.exports = {
  GIT_EXEC_TIMEOUT_MS,
  GIT_MAX_BUFFER_BYTES,
  validateGitRefs,
  resolveCommitRanges,
  runGitRangeDiff,
  getCommitLineDelta,
  computeAddedCommitsDelta
};
