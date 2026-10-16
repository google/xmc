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

const { getPrChangedFiles, getCommitModifiedFiles } = require('./git-range-diff');

// Default maximum lines of non-test diff delta (additions + deletions) permitted for code owners to auto-reapprove.
// Test file changes (see test-file-classifier.js) do not count against this budget.
const DEFAULT_MAX_OWNER_GRACE_LINES = 50;

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
  /^\.githooks\//i,
  // Refreshing the protobuf breaking-change baseline accepts breaking API changes.
  /^api\/testdata\/protos_baseline\.binpb$/i,
  // The breaking-change check's rules (buf.yaml) and test definition must be protected like the baseline.
  /^api\/buf\.yaml$/i,
  /^api\/BUILD(\.bazel)?$/i
];

/**
 * Pure function: Checks if a file path is a sensitive CI/security configuration file or part of the protobuf breaking-change check.
 * @param {string} filePath - Path of modified file.
 * @returns {boolean}
 */
function isProtectedFile(filePath) {
  if (!filePath || typeof filePath !== 'string') {
    return false;
  }
  let normalized = filePath.replace(/\\/g, '/').trim();
  // Git wraps paths containing special or non-ASCII characters in double quotes.
  if (normalized.length >= 2 && normalized.startsWith('"') && normalized.endsWith('"')) {
    normalized = normalized.slice(1, -1);
  }
  return PROTECTED_FILE_PATTERNS.some(pattern => pattern.test(normalized));
}

/**
 * Pure function: Checks if any file in the provided list is a protected file (see PROTECTED_FILE_PATTERNS).
 * @param {Array<string>} filePaths - List of file paths to inspect.
 * @returns {boolean} True if any file matches protected patterns.
 */
function hasProtectedFile(filePaths) {
  if (!Array.isArray(filePaths)) {
    return false;
  }
  return filePaths.some(isProtectedFile);
}

/**
 * Shell Helper: Checks if any protected files were modified.
 * Delegates git file retrieval to git-range-diff.getPrChangedFiles and evaluates against hasProtectedFile.
 * @param {string} baseRefOrApprovedSha - Target base branch name or approved SHA.
 * @param {string} approvedShaOrCurrentSha - Approved SHA or current SHA.
 * @param {string} [currentSha] - Current HEAD commit SHA.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {boolean} True if any sensitive file was modified.
 */
function checkProtectedFileChanges(baseRefOrApprovedSha, approvedShaOrCurrentSha, currentSha, execOptions = {}) {
  try {
    const files = getPrChangedFiles(baseRefOrApprovedSha, approvedShaOrCurrentSha, currentSha, execOptions);
    return hasProtectedFile(files);
  } catch (error) {
    const stderrInfo = error.stderr ? ' | Stderr: ' + error.stderr.trim() : '';
    console.log('[incremental-approval] Failed to check modified files: ' + error.message + stderrInfo);
    return true;
  }
}

/**
 * Shell Helper: Checks if any newly added commits modify protected files.
 * @param {Array<string>} addedCommitShas - List of commit SHAs to check.
 * @param {Object} [execOptions={}] - Subprocess options.
 * @returns {boolean} True if any commit modifies protected files.
 */
function checkAddedCommitsForProtectedFiles(addedCommitShas, execOptions = {}) {
  if (!Array.isArray(addedCommitShas) || addedCommitShas.length === 0) return false;
  for (const sha of addedCommitShas) {
    try {
      const files = getCommitModifiedFiles(sha, execOptions);
      if (hasProtectedFile(files)) return true;
    } catch (err) {
      console.log(`[incremental-approval] Failed to verify files for commit ${sha}: ${err.message}`);
      return true;
    }
  }
  return false;
}

/**
 * Pure function: Evaluates whether a synchronized PR should be auto-reapproved.
 * @param {Object} [params={}]
 * @param {boolean} [params.hasPriorApproval]
 * @param {string} [params.approvedSha]
 * @param {string} [params.currentSha]
 * @param {boolean} [params.isAuthorOwner]
 * @param {number} [params.deltaLines] - Total changed lines since last approval, including test files.
 * @param {number} [params.testDeltaLines=0] - Subset of deltaLines in test files; excluded from the owner grace budget only.
 * @param {boolean} [params.isCleanRebase]
 * @param {boolean} [params.hasRemovedCommits=false]
 * @param {boolean} [params.hasProtectedFileChanges=false]
 * @param {boolean} [params.isDismissed=false]
 * @param {number} [params.maxGraceLines=MAX_OWNER_GRACE_LINES]
 * @returns {{ shouldReapprove: boolean, reason: string, trigger: string }}
 */
function evaluateIncrementalApproval({
  hasPriorApproval,
  approvedSha,
  currentSha,
  isAuthorOwner,
  deltaLines,
  testDeltaLines = 0,
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
    const isUnchangedRebase = approvedSha !== currentSha && isCleanRebase && !hasRemovedCommits && deltaLines === 0;
    if (isUnchangedRebase) {
      return {
        shouldReapprove: true,
        reason: 'Clean rebase detected with zero semantic patch lines changed. PR touches protected files, but the approved patches are unchanged.',
        trigger: 'CLEAN_REBASE'
      };
    }
    return {
      shouldReapprove: false,
      reason: 'PR modifies sensitive CI workflows, scripts, ownership files, or the protobuf breaking-change baseline or its configuration (.github/, OWNERS, .githooks/, api/testdata/protos_baseline.binpb, api/buf.yaml, api/BUILD.bazel). Manual human review is required.',
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
  const validTestDelta = typeof testDeltaLines === 'number' && !isNaN(testDeltaLines) &&
    testDeltaLines >= 0 && validDelta && testDeltaLines <= deltaLines;
  if (!validDelta || !validTestDelta) {
    return {
      shouldReapprove: false,
      reason: `Invalid delta lines count: ${deltaLines} (test file lines: ${testDeltaLines}). Manual review required.`,
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

  if (isAuthorOwner) {
    const nonTestDelta = deltaLines - testDeltaLines;
    const testSuffix = testDeltaLines > 0 ? `, excluding ${testDeltaLines} test file line${testDeltaLines === 1 ? '' : 's'}` : '';
    if (nonTestDelta <= maxGraceLines) {
      return {
        shouldReapprove: true,
        reason: `PR author is a code owner and changes since last approval (${nonTestDelta} line${nonTestDelta === 1 ? '' : 's'}${testSuffix}) are within the ${maxGraceLines}-line grace delta.`,
        trigger: 'OWNER_GRACE_DELTA'
      };
    }
    return {
      shouldReapprove: false,
      reason: `Changes since last approval (${nonTestDelta} lines${testSuffix}) exceed the ${maxGraceLines}-line grace delta for code owners. Manual review required.`,
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
  hasProtectedFile,
  checkProtectedFileChanges,
  checkAddedCommitsForProtectedFiles,
  evaluateIncrementalApproval,
  formatReapprovalReviewBody
};
