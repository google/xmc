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
 * Pure function: Builds execution environment for git subprocesses.
 * Enforces non-interactive terminal flags and injects GITHUB_TOKEN basic auth header if available.
 * @param {Object} [customEnv={}] - Optional caller-specified environment overrides.
 * @returns {Object} Configured process environment dictionary.
 */
function buildGitEnv(customEnv = {}) {
  const env = {
    ...process.env,
    GIT_TERMINAL_PROMPT: '0',
    GIT_OPTIONAL_LOCKS: '0',
    ...customEnv
  };

  const token = process.env.GITHUB_TOKEN;
  if (token && !env.GIT_CONFIG_KEY_0) {
    const authHeader = `Authorization: Basic ${Buffer.from(`x-access-token:${token}`).toString('base64')}`;
    env.GIT_CONFIG_COUNT = '1';
    env.GIT_CONFIG_KEY_0 = 'http.extraheader';
    env.GIT_CONFIG_VALUE_0 = authHeader;
  }
  return env;
}

/**
 * Executes a git command synchronously with standardized security, timeout, buffer, and authentication options.
 * @param {Array<string>} args - Command line arguments for git.
 * @param {Object} [options={}] - Execution options.
 * @returns {string|{ status: number, stdout: string, stderr: string }}
 */
function execGit(args, options = {}) {
  const safeOptions = options || {};
  const { input, ignoreExitCode = false, env: customEnv, ...restOptions } = safeOptions;

  const defaultOptions = {
    encoding: 'utf8',
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS,
    stdio: ['pipe', 'pipe', 'pipe'],
    env: buildGitEnv(customEnv),
    ...restOptions
  };
  if (input !== undefined) {
    defaultOptions.input = input;
  }

  try {
    const stdout = childProcess.execFileSync('git', args, defaultOptions);
    return ignoreExitCode ? { status: 0, stdout: typeof stdout === 'string' ? stdout : '', stderr: '' } : stdout;
  } catch (error) {
    const stderr = error.stderr ? error.stderr.toString().trim() : '';
    const stdout = error.stdout ? error.stdout.toString().trim() : '';
    const exitCode = typeof error.status === 'number' ? error.status : 1;
    if (ignoreExitCode) {
      return { status: exitCode, stdout, stderr };
    }
    const failure = new Error(`git ${args.join(' ')} failed (exit ${exitCode}): ${stderr || stdout || error.message}`);
    failure.status = exitCode;
    failure.stderr = stderr;
    failure.stdout = stdout;
    throw failure;
  }
}

/**
 * Shell Helper: Ensures origin/${baseRef}, currentSha, and approvedSha are available locally.
 * Proactively fetches missing objects from origin with authentication.
 * @param {string} baseRef - Target base branch name.
 * @param {string} approvedSha - Previously approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {{ success: boolean, unreachableSha?: string, reason?: string, details?: string }}
 */
function ensureGitRefs(baseRef, approvedSha, currentSha, execOptions = {}) {
  validateGitRefs(baseRef, approvedSha, currentSha);

  const isRefPresent = (ref) => {
    try {
      execGit(['rev-parse', '--verify', '--quiet', ref], execOptions);
      return true;
    } catch (_) {
      return false;
    }
  };

  const hasBase = isRefPresent(`refs/remotes/origin/${baseRef}`);
  const hasCurrent = isRefPresent(currentSha);
  const hasApproved = isRefPresent(approvedSha);
  if (hasBase && hasCurrent && hasApproved) {
    return { success: true };
  }

  try {
    const fetchArgs = ['fetch', 'origin'];
    if (!hasBase) fetchArgs.push(`+refs/heads/${baseRef}:refs/remotes/origin/${baseRef}`);
    if (!hasCurrent) fetchArgs.push(currentSha);
    if (fetchArgs.length > 2) execGit(fetchArgs, execOptions);
  } catch (error) {
    return { success: false, reason: 'FETCH_BASE_OR_HEAD_FAILED', details: error.message };
  }

  if (!hasApproved && !isRefPresent(approvedSha)) {
    try {
      execGit(['fetch', 'origin', approvedSha], execOptions);
    } catch (error) {
      return { success: false, reason: 'APPROVED_SHA_UNREACHABLE', unreachableSha: approvedSha, details: error.message };
    }
  }
  return { success: true };
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
  if (approvedSha === currentSha) {
    return { approvedRange: currentRange, currentRange };
  }

  let approvedRange = `origin/${baseRef}..${approvedSha}`;
  const opts = { ...execOptions, encoding: 'utf8', stdio: ['pipe', 'pipe', 'ignore'] };

  try {
    const prCommitCountStr = execGit(['rev-list', '--count', currentRange], opts).trim();
    const prCommitCount = parseInt(prCommitCountStr, 10) || 0;

    if (prCommitCount > 0) {
      execGit(['rev-parse', '--verify', `${approvedSha}~${prCommitCount}`], opts);
      const approvedBaseCountStr = execGit(['rev-list', '--count', approvedRange], opts).trim();
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

  const { approvedRange, currentRange } = resolveCommitRanges(baseRef, approvedSha, currentSha);
  if (approvedRange === currentRange) {
    return '';
  }

  try {
    return execGit(['range-diff', '--no-color', approvedRange, currentRange]);
  } catch (error) {
    throw new Error(`Failed to execute git range-diff between ${approvedRange} and ${currentRange}: ${error.message}`);
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
    const numstat = execGit(['diff-tree', '--no-commit-id', '--numstat', '-r', '--no-renames', commitSha], opts);
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
    throw new Error(`Failed to get line delta for commit ${commitSha}: ${error.message}`);
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

/**
 * Shell Helper: Retrieves modified file paths between two refs or across PR merge base.
 * When baseRef is provided, uses 3-dot diff (origin/${baseRef}...${headSha}) to verify files changed
 * by the PR against the base branch, avoiding false positives from commits merged into base.
 * @param {string} baseRefOrApprovedSha - Target base branch name or approved SHA.
 * @param {string} approvedShaOrCurrentSha - Approved SHA or current SHA.
 * @param {string} [currentSha] - Current HEAD commit SHA.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {Array<string>} List of unique modified file paths.
 */
function getPrChangedFiles(baseRefOrApprovedSha, approvedShaOrCurrentSha, currentSha, execOptions = {}) {
  let baseRef, approvedSha, headSha, opts = execOptions;
  if (typeof currentSha === 'string') {
    baseRef = baseRefOrApprovedSha;
    approvedSha = approvedShaOrCurrentSha;
    headSha = currentSha;
  } else {
    approvedSha = baseRefOrApprovedSha;
    headSha = approvedShaOrCurrentSha;
    if (typeof currentSha === 'object') opts = currentSha;
  }

  if (baseRef) {
    const prDiffOutput = execGit(['diff', '--name-only', `origin/${baseRef}...${headSha}`], opts);
    const files = new Set(prDiffOutput.split(/\r?\n/).map(f => f.trim()).filter(Boolean));

    if (approvedSha && approvedSha !== headSha) {
      try {
        const isAncestor = execGit(['merge-base', '--is-ancestor', approvedSha, headSha], { ...opts, ignoreExitCode: true });
        if (isAncestor.status === 0) {
          const followUpDiff = execGit(['diff', '--name-only', approvedSha, headSha], opts);
          for (const file of followUpDiff.split(/\r?\n/).map(f => f.trim()).filter(Boolean)) {
            files.add(file);
          }
        }
      } catch (_) {}
    }
    return Array.from(files);
  }

  const diffOutput = execGit(['diff', '--name-only', approvedSha, headSha], opts);
  return diffOutput.split(/\r?\n/).map(f => f.trim()).filter(Boolean);
}

/**
 * Shell Helper: Retrieves list of modified files for a specific commit.
 * @param {string} commitSha - Commit SHA to inspect.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {Array<string>} List of modified file paths in the commit.
 */
function getCommitModifiedFiles(commitSha, execOptions = {}) {
  const output = execGit(['diff-tree', '--no-commit-id', '--name-only', '-r', '--no-renames', commitSha], execOptions);
  return output.split(/\r?\n/).map(f => f.trim()).filter(Boolean);
}

module.exports = {
  GIT_EXEC_TIMEOUT_MS,
  GIT_MAX_BUFFER_BYTES,
  validateGitRefs,
  buildGitEnv,
  execGit,
  ensureGitRefs,
  resolveCommitRanges,
  runGitRangeDiff,
  getCommitLineDelta,
  computeAddedCommitsDelta,
  getPrChangedFiles,
  getCommitModifiedFiles
};
