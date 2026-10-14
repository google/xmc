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
 * @fileoverview Aggregates result JSON artifacts from parallel matrix example runs,
 * generates formatted GitHub Step Summary markdown, and emits launched XIDs map.
 */

const fs = require('node:fs');
const path = require('node:path');

/**
 * Parses a single JSON result file, returning a synthetic FAILED result object
 * if the file is unreadable or contains malformed JSON.
 *
 * @param {string} filePath - Path to the result JSON file.
 * @returns {object} Parsed result object or synthetic FAILED result entry.
 */
function parseResultFile(filePath) {
  const fallbackId = path.basename(filePath, '.json');
  try {
    const parsed = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed;
    }
    throw new Error('Result JSON is not an object');
  } catch (err) {
    console.error(`Error reading or parsing ${filePath}: ${err.message}`);
    return {
      id: fallbackId,
      name: path.basename(filePath),
      status: 'FAILED',
      launch_status: 'parse_error',
      verify_status: 'N/A',
      xid: 'N/A',
    };
  }
}

/**
 * Reads all JSON result files from a directory and parses them.
 *
 * @param {string} resultsDir - Directory containing result JSON files.
 * @returns {Array<object>} Parsed result objects sorted by file path.
 */
function loadResultsFromDir(resultsDir) {
  if (!fs.existsSync(resultsDir)) {
    return [];
  }

  return fs.readdirSync(resultsDir)
    .filter(file => file.endsWith('.json'))
    .sort()
    .map(file => parseResultFile(path.join(resultsDir, file)));
}

/**
 * Formats a status badge for GitHub step summary tables.
 *
 * @param {string} status - Raw workload execution status string.
 * @returns {string} Formatted emoji badge.
 */
function formatStatusBadge(status) {
  switch (status) {
    case 'PASSED':
      return '✅ PASSED';
    case 'SKIPPED':
      return '⏭️ SKIPPED';
    default:
      return '❌ FAILED';
  }
}

/**
 * Resolves the markdown status header line based on result counts and pass state.
 *
 * @param {Array<object>} results
 * @param {boolean} allPassed
 * @returns {string}
 */
function resolveStatusHeader(results, allPassed) {
  if (results.length === 0) {
    return '### ⚠️ Overall Status: NO RESULTS FOUND\n';
  }
  if (results.length > 1) {
    return allPassed
      ? '### ✅ Overall Status: ALL PASSED\n'
      : '### ⚠️ Overall Status: SOME WORKLOADS FAILED\n';
  }
  return allPassed
    ? '### ✅ Overall Status: PASSED\n'
    : '### ❌ Overall Status: FAILED\n';
}

/**
 * Formats a 6-column markdown table for multiple workload results.
 *
 * @param {Array<object>} results
 * @returns {Array<string>}
 */
function formatMultiResultTable(results) {
  const rows = [
    '| Example ID | Name | Status | Launch | Verify | Experiment / Identifier |',
    '| --- | --- | --- | --- | --- | --- |',
  ];
  for (const r of results) {
    const id = r.id || '';
    const name = r.name || '';
    const statusBadge = formatStatusBadge(r.status);
    const launchStatus = r.launch_status || 'N/A';
    const verifyStatus = r.verify_status?.toLowerCase() === 'skipped' ? '⏭️ Skipped' : r.verify_status || 'N/A';
    const xid = r.xid || 'N/A';
    rows.push(`| \`${id}\` | ${name} | ${statusBadge} | \`${launchStatus}\` | \`${verifyStatus}\` | \`${xid}\` |`);
  }
  return rows;
}

/**
 * Formats a 2-column property table for a single workload result.
 *
 * @param {object} r
 * @returns {Array<string>}
 */
function formatSingleResultTable(r) {
  const id = r.id || 'N/A';
  const name = r.name || 'N/A';
  const statusBadge = formatStatusBadge(r.status);
  const launchStatus = r.launch_status || 'N/A';
  const verifyStatus = r.verify_status?.toLowerCase() === 'skipped' ? '⏭️ Skipped' : r.verify_status || 'N/A';
  const xid = r.xid || 'N/A';
  return [
    '| Property | Value |',
    '| --- | --- |',
    `| **Example ID** | \`${id}\` |`,
    `| **Name** | ${name} |`,
    `| **Status** | ${statusBadge} |`,
    `| **Launch Step** | \`${launchStatus}\` |`,
    `| **Verify Step** | \`${verifyStatus}\` |`,
    `| **Experiment / Identifier** | \`${xid}\` |`,
  ];
}

/**
 * Aggregates result objects into an XID mapping and markdown summary.
 * Formats a clean single-workload summary or a multi-workload matrix table.
 *
 * @param {Array<object>} rawResults - Array of result objects.
 * @param {object} [options]
 * @param {string} [options.title] - Optional custom markdown title.
 * @param {string} [options.matrixResult] - Upstream matrix job result (e.g. 'success', 'failure').
 * @returns {{ xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean }}
 */
function aggregateResults(rawResults, { title, matrixResult } = {}) {
  const results = (rawResults || []).filter(Boolean);
  const xidMap = {};
  for (const r of results) {
    if (r.id && r.xid && r.xid !== 'skipped' && r.xid !== 'N/A') {
      xidMap[r.id] = r.xid;
    }
  }

  const hasPassed = results.some(r => r.status === 'PASSED');
  const hasFailed = results.some(r => r.status !== 'PASSED' && r.status !== 'SKIPPED');
  const matrixSucceeded = !matrixResult || matrixResult === 'success';
  const allPassed = results.length > 0 && hasPassed && !hasFailed && matrixSucceeded;

  const defaultTitle = '## 🌟 Nightly Examples Test Summary\n';
  const statusHeader = resolveStatusHeader(results, allPassed);
  const bodyLines = results.length === 0
    ? ['_No example test results found._']
    : results.length > 1
      ? formatMultiResultTable(results)
      : formatSingleResultTable(results[0]);

  const summaryMarkdown = [title || defaultTitle, statusHeader, ...bodyLines].join('\n') + '\n';
  return { xidMap, summaryMarkdown, allPassed };
}

/**
 * Resolves the GitHub Actions error annotation string when aggregation fails.
 *
 * @param {Array<object>} results
 * @param {string} [matrixResult]
 * @returns {string}
 */
function resolveFailureAnnotation(results, matrixResult) {
  if (results.length === 0) {
    return '::error::No example workload results found.';
  }
  const failedIds = results
    .filter(r => r && r.status !== 'PASSED' && r.status !== 'SKIPPED')
    .map(r => `${r.id || 'unknown'} (${r.status || 'FAILED'})`);
  if (failedIds.length > 0) {
    return `::error::One or more example workloads failed launch or verification: ${failedIds.join(', ')}`;
  }
  if (matrixResult && matrixResult !== 'success') {
    return `::error::One or more example workloads failed launch or verification (matrix status: ${matrixResult}).`;
  }
  return '::error::No example workloads passed (all workloads were skipped).';
}

/**
 * Main entrypoint invoked by GitHub Actions runner.
 *
 * @param {object} [options]
 * @param {string} [options.resultsDir='results']
 * @param {string} [options.stepSummaryPath]
 * @param {string} [options.githubOutputPath]
 * @param {string} [options.title]
 * @param {string} [options.matrixResult]
 * @returns {{ results: Array<object>, xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean, matrixResult: string | undefined }}
 */
function run({
  resultsDir = 'results',
  stepSummaryPath = process.env.GITHUB_STEP_SUMMARY,
  githubOutputPath = process.env.GITHUB_OUTPUT,
  title,
  matrixResult = process.env.MATRIX_RESULT,
} = {}) {
  const results = loadResultsFromDir(resultsDir);
  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results, { title, matrixResult });

  if (stepSummaryPath) {
    fs.appendFileSync(stepSummaryPath, summaryMarkdown, 'utf8');
  }

  const xidsJson = JSON.stringify(xidMap);
  console.log(`Aggregated launched_xids: ${xidsJson}`);
  console.log(summaryMarkdown);

  if (githubOutputPath) {
    fs.appendFileSync(githubOutputPath, `launched_xids=${xidsJson}\n`, 'utf8');
  }

  return { results, xidMap, summaryMarkdown, allPassed, matrixResult };
}

if (require.main === module) {
  const { results, allPassed, matrixResult } = run();
  if (!allPassed) {
    console.error(resolveFailureAnnotation(results, matrixResult));
    process.exit(1);
  }
}

module.exports = {
  parseResultFile,
  loadResultsFromDir,
  formatStatusBadge,
  resolveFailureAnnotation,
  aggregateResults,
  run,
};
