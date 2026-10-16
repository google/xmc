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

const { isTestFile } = require('./test-file-classifier');

/**
 * Pure function: Detects whether a diff line is metadata or commit header in git range-diff output.
 * @param {string} trimmedLine - Trimmed line content from range-diff.
 * @returns {boolean} True if line is a metadata header.
 */
function isIgnoredRangeDiffHeader(trimmedLine) {
  return (
    trimmedLine.startsWith('@@ Metadata') ||
    trimmedLine.startsWith('Author:') ||
    trimmedLine.startsWith('Commit:') ||
    trimmedLine.startsWith('## Commit message ##') ||
    trimmedLine.startsWith('## Notes')
  );
}

/**
 * Pure function: Extracts the file path from a range-diff file header.
 * Handles outer hunk headers (`@@ path: context`, `@@ path (new)`) and inner file headers
 * (`## path ##`, `## old => new ##`, `## path (deleted) ##`).
 * @param {string} header - Header text with the leading `@@` or surrounding `##` markers.
 * @returns {string|null} File path, or null when the header names no file.
 */
function extractRangeDiffFilePath(header) {
  let body = header.trim();
  if (body.startsWith('@@')) {
    body = body.slice(2).trim().split(/:(?:\s|$)/)[0];
  } else if (body.startsWith('## ') && body.endsWith(' ##')) {
    body = body.slice(3, -3);
  } else {
    return null;
  }
  body = body.replace(/\s+\([^)]*\)$/, '').trim();
  const renameParts = body.split(' => ');
  const path = renameParts[renameParts.length - 1].trim();
  return path || null;
}

/**
 * Pure function: Parses git range-diff output into structured additions, deletions, and commit modifications.
 * Lines belonging to test files (see test-file-classifier.js) are additionally counted in testDelta.
 * @param {string} output - Raw output of `git range-diff`.
 * @returns {{ additions: number, deletions: number, totalDelta: number, testDelta: number, isCleanRebase: boolean, hasRemovedCommits: boolean, addedCommitShas: string[] }}
 */
function parseRangeDiff(output) {
  if (!output || typeof output !== 'string' || output.trim() === '') {
    return {
      additions: 0,
      deletions: 0,
      totalDelta: 0,
      testDelta: 0,
      isCleanRebase: true,
      hasRemovedCommits: false,
      addedCommitShas: []
    };
  }

  const lines = output.split(/\r?\n/);
  let additions = 0;
  let deletions = 0;
  let testDelta = 0;
  let currentFile = null;
  let inCodeDiffHunk = false;
  let hasModifiedCommits = false;
  let hasRemovedCommits = false;
  const addedCommitShas = [];

  for (const line of lines) {
    const cleanLine = line.replace(/\x1b\[[0-9;]*m/g, '');
    const commitMatch = cleanLine.match(/^\s*(?:\d+|-):\s+([a-f0-9-]+)\s+([=!<>])\s+(?:\d+|-):\s+([a-f0-9-]+)/);
    if (commitMatch) {
      const status = commitMatch[2];
      if (status !== '=') {
        hasModifiedCommits = true;
      }
      if (status === '<') {
        hasRemovedCommits = true;
      } else if (status === '>') {
        const addedSha = commitMatch[3];
        if (addedSha && addedSha !== '--------') {
          addedCommitShas.push(addedSha);
        }
      }
      inCodeDiffHunk = false;
      currentFile = null;
      continue;
    }

    const trimmed = cleanLine.trim();
    if (isIgnoredRangeDiffHeader(trimmed)) {
      inCodeDiffHunk = false;
      currentFile = null;
      continue;
    }

    if ((trimmed.startsWith('## ') && trimmed.endsWith(' ##')) || trimmed.startsWith('@@')) {
      inCodeDiffHunk = true;
      currentFile = extractRangeDiffFilePath(trimmed) || currentFile;
      continue;
    }

    if (inCodeDiffHunk) {
      const hunkMatch = cleanLine.match(/^ {4}([-+ ])([-+ ])?(.*)$/);
      if (!hunkMatch) {
        continue;
      }

      const outerCol = hunkMatch[1];
      const content = (hunkMatch[3] || '').trim();

      if (content.startsWith('## ')) {
        currentFile = extractRangeDiffFilePath(content) || currentFile;
        continue;
      }
      if (content.startsWith('\\ No newline') || content.startsWith('Binary files')) {
        continue;
      }

      if (outerCol === '+') {
        additions++;
      } else if (outerCol === '-') {
        deletions++;
      } else {
        continue;
      }
      if (isTestFile(currentFile)) {
        testDelta++;
      }
    }
  }

  const totalDelta = additions + deletions;
  const isCleanRebase = !hasModifiedCommits && !hasRemovedCommits && addedCommitShas.length === 0 && totalDelta === 0;

  return {
    additions,
    deletions,
    totalDelta,
    testDelta,
    isCleanRebase,
    hasRemovedCommits,
    addedCommitShas
  };
}

module.exports = {
  extractRangeDiffFilePath,
  isIgnoredRangeDiffHeader,
  parseRangeDiff
};
