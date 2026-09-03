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
 * Executes a git command synchronously with standardized security, timeout, buffer, and authentication options.
 * Automatically injects GITHUB_TOKEN authentication if present in process.env.
 * @param {Array<string>} args - Command line arguments for git.
 * @param {Object} [options={}] - Execution options.
 * @returns {string|{ status: number, stdout: string, stderr: string }}
 */
function execGit(args, options = {}) {
  const { input, ignoreExitCode = false, ...restOptions } = options;
  const execEnv = {
    ...process.env,
    GIT_TERMINAL_PROMPT: '0',
    GIT_OPTIONAL_LOCKS: '0',
    ...(restOptions.env || {})
  };

  const token = process.env.GITHUB_TOKEN;
  if (token && !execEnv.GIT_CONFIG_KEY_0) {
    const authHeader = `Authorization: Basic ${Buffer.from(`x-access-token:${token}`).toString('base64')}`;
    execEnv.GIT_CONFIG_COUNT = '1';
    execEnv.GIT_CONFIG_KEY_0 = 'http.extraheader';
    execEnv.GIT_CONFIG_VALUE_0 = authHeader;
  }

  const defaultOptions = {
    encoding: 'utf8',
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS,
    stdio: ['pipe', 'pipe', 'pipe'],
    env: execEnv,
    ...restOptions
  };

  if (input !== undefined) {
    defaultOptions.input = input;
  }

  try {
    const stdout = childProcess.execFileSync('git', args, defaultOptions);
    if (ignoreExitCode) {
      return { status: 0, stdout: typeof stdout === 'string' ? stdout : '', stderr: '' };
    }
    return stdout;
  } catch (error) {
    const stderr = error.stderr ? error.stderr.toString().trim() : '';
    const stdout = error.stdout ? error.stdout.toString().trim() : '';
    const exitCode = typeof error.status === 'number' ? error.status : 1;

    if (ignoreExitCode) {
      return { status: exitCode, stdout, stderr };
    }

    const errorMsg = stderr || stdout || error.message;
    const failure = new Error(`git ${args.join(' ')} failed (exit ${exitCode}): ${errorMsg}`);
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

  // Fetch origin/${baseRef} and currentSha if missing
  try {
    const fetchArgs = ['fetch', 'origin'];
    if (!hasBase) {
      fetchArgs.push(`+refs/heads/${baseRef}:refs/remotes/origin/${baseRef}`);
    }
    if (!hasCurrent) {
      fetchArgs.push(currentSha);
    }
    if (fetchArgs.length > 2) {
      execGit(fetchArgs, execOptions);
    }
  } catch (error) {
    return {
      success: false,
      reason: 'FETCH_BASE_OR_HEAD_FAILED',
      details: error.message
    };
  }

  // Fetch approvedSha if missing
  if (!hasApproved) {
    try {
      execGit(['fetch', 'origin', approvedSha], execOptions);
    } catch (error) {
      return {
        success: false,
        reason: 'APPROVED_SHA_UNREACHABLE',
        unreachableSha: approvedSha,
        details: error.message
      };
    }
  }

  return { success: true };
}

/**
 * Shell Helper: Checks if currentSha is a fast-forward descendant of approvedSha.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @param {Object} opts - Subprocess options.
 * @returns {boolean} True if fast-forward.
 */
function isFastForwardApproval(approvedSha, currentSha, opts) {
  try {
    const res = execGit(
      ['merge-base', '--is-ancestor', approvedSha, currentSha],
      { ...opts, ignoreExitCode: true }
    );
    return res.status === 0;
  } catch (_) {
    return false;
  }
}

/**
 * Shell Helper: Returns the commit count for a given revision range.
 * @param {string} range - Revision range string.
 * @param {Object} opts - Subprocess options.
 * @returns {number}
 */
function getCommitCountInRange(range, opts) {
  try {
    const countStr = execGit(['rev-list', '--count', range], opts).trim();
    return parseInt(countStr, 10) || 0;
  } catch (_) {
    return 0;
  }
}

/**
 * Shell Helper: Computes the git patch-id for a commit.
 * @param {string} commitSha - Commit SHA to inspect.
 * @param {Object} opts - Subprocess options.
 * @returns {string|null}
 */
function getCommitPatchId(commitSha, opts) {
  try {
    const diff = execGit(['diff-tree', '-p', commitSha], opts);
    const patchIdOutput = execGit(['patch-id'], { ...opts, input: diff }).trim();
    return patchIdOutput.split(/\s+/)[0] || null;
  } catch (_) {
    return null;
  }
}

/**
 * Shell Helper: Indexes approved candidates by patch-id and commit subject.
 * @param {string} approvedRange - Revision range for approved commits.
 * @param {Object} opts - Subprocess options.
 * @returns {{ byPatchId: Map<string, string>, bySubject: Map<string, string> }}
 */
function indexApprovedCandidateCommits(approvedRange, opts) {
  const byPatchId = new Map();
  const bySubject = new Map();

  let listStr = '';
  try {
    listStr = execGit(['rev-list', '--reverse', approvedRange], opts).trim();
  } catch (_) {
    return { byPatchId, bySubject };
  }

  const candidates = listStr ? listStr.split(/\r?\n/).filter(Boolean) : [];
  for (const c of candidates) {
    const patchId = getCommitPatchId(c, opts);
    if (patchId) {
      byPatchId.set(patchId, c);
    }
    try {
      const subj = execGit(['log', '-1', '--format=%s', c], opts).trim();
      if (subj) {
        bySubject.set(subj, c);
      }
    } catch (_) {}
  }

  return { byPatchId, bySubject };
}

/**
 * Shell Helper: Correlates current commits against indexed approved commits to find the parent base.
 * @param {string[]} currCommits - Ordered commit SHAs in current branch.
 * @param {{ byPatchId: Map<string, string>, bySubject: Map<string, string> }} approvedMaps - Indexed candidates.
 * @param {Object} opts - Subprocess options.
 * @returns {string|null}
 */
function correlateStackedParentBase(currCommits, approvedMaps, opts) {
  for (let i = 0; i < currCommits.length; i++) {
    const cSha = currCommits[i];
    const patchId = getCommitPatchId(cSha, opts);
    let matched = patchId ? approvedMaps.byPatchId.get(patchId) : null;
    if (!matched) {
      try {
        const subj = execGit(['log', '-1', '--format=%s', cSha], opts).trim();
        if (subj) {
          matched = approvedMaps.bySubject.get(subj);
        }
      } catch (_) {}
    }

    if (matched) {
      const baseOffset = i + 1;
      try {
        return execGit(['rev-parse', '--verify', `${matched}~${baseOffset}`], opts).trim();
      } catch (_) {
        try {
          return execGit(['rev-parse', '--verify', `${matched}~1`], opts).trim();
        } catch (_) {}
      }
    }
  }
  return null;
}

/**
 * Shell Helper: Resolves scoped approved range when parent branch in stacked PR was rebased.
 * @param {string} currentRange - Current revision range.
 * @param {string} approvedRange - Default approved revision range.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {number} prCommitCount - Commit count in current PR branch.
 * @param {Object} opts - Subprocess options.
 * @returns {string}
 */
function resolveStackedApprovedRange(currentRange, approvedRange, approvedSha, prCommitCount, opts) {
  try {
    const currCommitsStr = execGit(['rev-list', '--reverse', currentRange], opts).trim();
    const currCommits = currCommitsStr ? currCommitsStr.split(/\r?\n/).filter(Boolean) : [];
    if (currCommits.length > 0) {
      const approvedMaps = indexApprovedCandidateCommits(approvedRange, opts);
      const correlatedBase = correlateStackedParentBase(currCommits, approvedMaps, opts);
      if (correlatedBase) {
        return `${correlatedBase}..${approvedSha}`;
      }
    }
  } catch (_) {
    // Proceed to fallback offset
  }

  try {
    execGit(['rev-parse', '--verify', `${approvedSha}~${prCommitCount}`], opts);
    return `${approvedSha}~${prCommitCount}..${approvedSha}`;
  } catch (_) {
    return approvedRange;
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
  if (approvedSha === currentSha) {
    return { approvedRange: currentRange, currentRange };
  }

  const opts = { encoding: 'utf8', ...execOptions, stdio: ['pipe', 'pipe', 'pipe'] };

  if (isFastForwardApproval(approvedSha, currentSha, opts)) {
    return {
      approvedRange: `origin/${baseRef}..${approvedSha}`,
      currentRange
    };
  }

  let approvedRange = `origin/${baseRef}..${approvedSha}`;
  const prCommitCount = getCommitCountInRange(currentRange, opts);
  const approvedBaseCount = getCommitCountInRange(approvedRange, opts);

  if (prCommitCount > 0 && approvedBaseCount > prCommitCount) {
    approvedRange = resolveStackedApprovedRange(currentRange, approvedRange, approvedSha, prCommitCount, opts);
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
  const opts = {
    encoding: 'utf8',
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS,
    stdio: ['pipe', 'pipe', 'pipe'],
    ...execOptions
  };

  try {
    const numstat = execGit(['diff-tree', '--numstat', '-r', '--no-renames', commitSha], opts);
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
  execGit,
  ensureGitRefs,
  isFastForwardApproval,
  getCommitCountInRange,
  getCommitPatchId,
  indexApprovedCandidateCommits,
  correlateStackedParentBase,
  resolveStackedApprovedRange,
  resolveCommitRanges,
  runGitRangeDiff,
  getCommitLineDelta,
  computeAddedCommitsDelta
};
