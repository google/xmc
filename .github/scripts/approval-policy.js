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
const { GIT_EXEC_TIMEOUT_MS, GIT_MAX_BUFFER_BYTES } = require('./git-range-diff');

// Default maximum lines of diff delta (additions + deletions) permitted for code owners to auto-reapprove.
const DEFAULT_MAX_OWNER_GRACE_LINES = 10;

/**
 * Pure function: Resolves maximum owner grace lines from env or fallback default.
 * @param {string|number|undefined} [envVal] - Raw value from environment variable.
 * @param {number} [defaultValue=DEFAULT_MAX_OWNER_GRACE_LINES] - Fallback default.
 * @returns {number}
 */
function getMaxOwnerGraceLines(envVal, defaultValue = DEFAULT_MAX_OWNER_GRACE_LINES) {
  if (envVal !== undefined && envVal !== null && envVal !== '') {
    const parsed = parseInt(String(envVal), 10);
    if (!isNaN(parsed) && parsed >= 0) {
      return parsed;
    }
  }
  return defaultValue;
}

// Maximum lines of diff delta (additions + deletions) permitted for code owners to auto-reapprove.
const MAX_OWNER_GRACE_LINES = getMaxOwnerGraceLines(process.env.MAX_OWNER_GRACE_LINES);

const PROTECTED_FILE_PATTERNS = [
  /^\.github\//i,
  /^OWNERS$/i,
  /^\.githooks\//i
];

/**
 * Pure function: Checks if a file path is a sensitive CI/security configuration file.
 * @param {string} filePath - Path of modified file.
 * @returns {boolean}
 */
function isProtectedFile(filePath) {
  if (!filePath || typeof filePath !== 'string') {
    return false;
  }
  const normalized = filePath.replace(/\\/g, '/').trim();
  return PROTECTED_FILE_PATTERNS.some(pattern => pattern.test(normalized));
}

/**
 * Shell Helper: Checks if any sensitive CI or repo configuration files were modified between commits.
 * @param {string} approvedSha - Previous approved commit SHA.
 * @param {string} currentSha - Current HEAD commit SHA.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {boolean} True if any sensitive file was modified.
 */
function checkProtectedFileChanges(approvedSha, currentSha, execOptions = {}) {
  const opts = {
    encoding: 'utf8',
    maxBuffer: GIT_MAX_BUFFER_BYTES,
    timeout: GIT_EXEC_TIMEOUT_MS,
    stdio: ['pipe', 'pipe', 'pipe'],
    ...execOptions
  };

  try {
    const diffOutput = childProcess.execFileSync('git', ['diff', '--name-only', approvedSha, currentSha], opts);
    const files = diffOutput.split(/\r?\n/).filter(f => f.trim().length > 0);
    return files.some(isProtectedFile);
  } catch (error) {
    const stderrInfo = error.stderr ? ' | Stderr: ' + error.stderr.trim() : '';
    console.log('[incremental-approval] Failed to check modified files between ' + approvedSha + '..' + currentSha + ': ' + error.message + stderrInfo);
    return true;
  }
}

/**
 * Pure function: Evaluates whether a synchronized PR should be auto-reapproved.
 * @param {Object} [params={}]
 * @param {boolean} [params.hasPriorApproval]
 * @param {string} [params.approvedSha]
 * @param {string} [params.currentSha]
 * @param {boolean} [params.isAuthorOwner]
 * @param {number} [params.deltaLines]
 * @param {boolean} [params.isCleanRebase]
 * @param {boolean} [params.hasRemovedCommits=false]
 * @param {boolean} [params.hasProtectedFileChanges=false]
 * @param {boolean} [params.isDismissed=false]
 * @param {number} [params.maxGraceLines=10]
 * @returns {{ shouldReapprove: boolean, reason: string, trigger: string }}
 */
function evaluateIncrementalApproval({
  hasPriorApproval,
  approvedSha,
  currentSha,
  isAuthorOwner,
  deltaLines,
  isCleanRebase,
  hasRemovedCommits = false,
  hasProtectedFileChanges = false,
  isDismissed = false,
  maxGraceLines = MAX_OWNER_GRACE_LINES
} = {}) {
  if (!hasPriorApproval || !approvedSha) {
    return {
      shouldReapprove: false,
      reason: 'No prior active approval found on this PR.',
      trigger: 'NO_PRIOR_APPROVAL'
    };
  }

  if (hasProtectedFileChanges) {
    return {
      shouldReapprove: false,
      reason: 'PR modifies sensitive CI workflows, scripts, or ownership files (.github/, OWNERS, .githooks/). Manual human review is required.',
      trigger: 'PROTECTED_FILES_MODIFIED'
    };
  }

  if (approvedSha === currentSha) {
    if (!isDismissed) {
      return {
        shouldReapprove: false,
        reason: 'PR HEAD is already approved at this commit.',
        trigger: 'ALREADY_APPROVED'
      };
    }
    return {
      shouldReapprove: true,
      reason: 'PR HEAD matches previously approved commit whose approval was dismissed.',
      trigger: 'RESTORE_DISMISSED_APPROVAL'
    };
  }

  const validDelta = typeof deltaLines === 'number' && !isNaN(deltaLines) && deltaLines >= 0;
  if (!validDelta) {
    return {
      shouldReapprove: false,
      reason: `Invalid delta lines count: ${deltaLines}. Manual review required.`,
      trigger: 'INVALID_DELTA'
    };
  }

  if (isCleanRebase && validDelta && deltaLines === 0 && !hasRemovedCommits) {
    return {
      shouldReapprove: true,
      reason: 'Clean rebase detected with zero semantic patch lines changed.',
      trigger: 'CLEAN_REBASE'
    };
  }

  if (hasRemovedCommits) {
    return {
      shouldReapprove: false,
      reason: 'PR contains removed or rewritten commits that were dropped from the approved history. Manual review required.',
      trigger: 'REMOVED_COMMITS'
    };
  }

  if (isAuthorOwner && validDelta && deltaLines <= maxGraceLines) {
    return {
      shouldReapprove: true,
      reason: `PR author is a code owner and changes since last approval (${deltaLines} line${deltaLines === 1 ? '' : 's'}) are within the ${maxGraceLines}-line grace delta.`,
      trigger: 'OWNER_GRACE_DELTA'
    };
  }

  if (isAuthorOwner) {
    return {
      shouldReapprove: false,
      reason: `Changes since last approval (${deltaLines} lines) exceed the ${maxGraceLines}-line grace delta for code owners. Manual review required.`,
      trigger: 'EXCEEDS_OWNER_GRACE'
    };
  }

  return {
    shouldReapprove: false,
    reason: `Non-owner author pushed ${deltaLines} changed line${deltaLines === 1 ? '' : 's'} since last approval. Manual review required.`,
    trigger: 'NON_OWNER_MODIFICATION'
  };
}

/**
 * Pure function: Formats markdown comment body for automated re-approval reviews.
 * @param {string} reason - Justification for re-approval.
 * @param {string} approver - Original approving reviewer login.
 * @param {string} approvedSha - Previously approved commit SHA.
 * @returns {string} Formatted review comment body.
 */
function formatReapprovalReviewBody(reason, approver, approvedSha) {
  return `🤖 **Incremental PR Approval**\n\n${reason}\n\n*Preserving prior approval by @${approver} on commit \`${approvedSha.slice(0, 7)}\`.*`;
}

module.exports = {
  DEFAULT_MAX_OWNER_GRACE_LINES,
  MAX_OWNER_GRACE_LINES,
  PROTECTED_FILE_PATTERNS,
  getMaxOwnerGraceLines,
  isProtectedFile,
  checkProtectedFileChanges,
  evaluateIncrementalApproval,
  formatReapprovalReviewBody
};
