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
    trimmedLine.startsWith('## Metadata ##') ||
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
 * When a rename crosses the test file boundary, the non-test side is returned so that
 * the changed lines are never credited as test lines (fail-safe).
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
  const paths = body.split(' => ').map(part => part.trim());
  const nonTestPath = paths.find(path => !isTestFile(path));
  const path = nonTestPath !== undefined ? nonTestPath : paths[paths.length - 1];
  if (!path || path.startsWith('-') || path.startsWith('+') || path.endsWith('@@')) {
    return null;
  }
  return path;
}

/**
 * Pure function: Classifies a single range-diff line by its raw column layout.
 * Range-diff indents patch lines by 4 spaces followed by the outer diff column. Source code
 * lines always carry an inner diff column (`+`, `-`, ` `), so structural lines that lack it
 * (inner `@@` hunk headers, empty separators) cannot be forged by file contents.
 * @param {string} line - Range-diff line with ANSI color codes removed.
 * @returns {{ kind: string, outer?: string, text?: string }} kind is one of
 *   outerHeader, separator, innerHunkHeader, sectionHeader, noise, code, other.
 */
function classifyRangeDiffLine(line) {
  if (!line.startsWith('    ') || line.length < 5) {
    return { kind: 'other' };
  }
  const outer = line[4];
  const rest = line.slice(5);
  if (outer === '@') {
    return { kind: 'outerHeader', text: line.trim() };
  }
  if (outer !== '+' && outer !== '-' && outer !== ' ') {
    return { kind: 'other' };
  }
  if (rest === '') {
    return { kind: 'separator', outer };
  }
  if (rest.startsWith('@@')) {
    return { kind: 'innerHunkHeader', outer };
  }
  if (rest.startsWith(' ## ') && rest.endsWith(' ##')) {
    return { kind: 'sectionHeader', outer, text: rest.trim() };
  }
  if (rest.startsWith('\\ No newline') || /^ ?Binary files /.test(rest)) {
    return { kind: 'noise', outer };
  }
  return { kind: 'code', outer };
}

// Line kinds after which a `## ... ##` line is accepted as a genuine section header. Outer hunk
// headers are deliberately excluded: an outer hunk may start at a forged context line.
const SECTION_HEADER_PREDECESSORS = new Set(['separator', 'sectionHeader']);

/**
 * Pure function: Parses git range-diff output into structured additions, deletions, and commit modifications.
 * Lines belonging to test files (see test-file-classifier.js) are additionally counted in testDelta.
 * A file header only takes effect once confirmed by an unforgeable inner `@@` hunk header; otherwise
 * the current file becomes unknown and subsequent lines are counted as non-test (fail-safe).
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
  const state = { additions: 0, deletions: 0, testDelta: 0, currentFile: null, pendingFile: undefined, inCodeDiffHunk: false, prevKind: 'other' };
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
      Object.assign(state, { inCodeDiffHunk: false, currentFile: null, pendingFile: undefined, prevKind: 'other' });
      continue;
    }
    applyRangeDiffLine(state, classifyRangeDiffLine(cleanLine));
  }

  const { additions, deletions, testDelta } = state;
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

/**
 * Applies a structural (header) line to the parser state.
 * @param {Object} state - Mutable parser state.
 * @param {{ kind: string, text?: string }} classified - Output of classifyRangeDiffLine.
 */
function applyRangeDiffHeader(state, classified) {
  if (classified.kind === 'innerHunkHeader') {
    if (state.pendingFile !== undefined) {
      state.currentFile = state.pendingFile;
      state.pendingFile = undefined;
    }
    return;
  }
  state.pendingFile = undefined;
  if (isIgnoredRangeDiffHeader(classified.text)) {
    state.inCodeDiffHunk = false;
    state.currentFile = null;
    return;
  }
  state.inCodeDiffHunk = true;
  if (classified.kind === 'outerHeader') {
    state.currentFile = extractRangeDiffFilePath(classified.text);
    return;
  }
  state.currentFile = null;
  state.pendingFile = extractRangeDiffFilePath(classified.text);
}

/**
 * Applies one classified range-diff line to the mutable parser state.
 * @param {Object} state - Mutable parser state.
 * @param {{ kind: string, outer?: string, text?: string }} classified - Output of classifyRangeDiffLine.
 */
function applyRangeDiffLine(state, classified) {
  const prevKind = state.prevKind;
  state.prevKind = classified.kind;
  const isHeader = classified.kind === 'outerHeader' || classified.kind === 'innerHunkHeader' ||
    (classified.kind === 'sectionHeader' && SECTION_HEADER_PREDECESSORS.has(prevKind));
  if (isHeader) {
    applyRangeDiffHeader(state, classified);
    return;
  }
  if (state.pendingFile !== undefined) {
    state.pendingFile = undefined;
    state.currentFile = null;
  }
  if (!state.inCodeDiffHunk || classified.kind === 'other' || classified.kind === 'noise') {
    return;
  }
  if (classified.outer === '+') {
    state.additions++;
  } else if (classified.outer === '-') {
    state.deletions++;
  } else {
    return;
  }
  if (isTestFile(state.currentFile)) {
    state.testDelta++;
  }
}

module.exports = {
  extractRangeDiffFilePath,
  isIgnoredRangeDiffHeader,
  parseRangeDiff
};
