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
 * Reads all JSON result files from a directory and parses them.
 *
 * @param {string} resultsDir - Directory containing result JSON files.
 * @returns {Array<object>} Parsed result objects sorted by file path.
 */
function loadResultsFromDir(resultsDir) {
  if (!fs.existsSync(resultsDir)) {
    return [];
  }

  const files = fs.readdirSync(resultsDir)
    .filter(file => file.endsWith('.json'))
    .sort();

  const results = [];
  for (const file of files) {
    const filePath = path.join(resultsDir, file);
    try {
      const content = fs.readFileSync(filePath, 'utf8');
      results.push(JSON.parse(content));
    } catch (err) {
      console.error(`Error reading or parsing ${filePath}: ${err.message}`);
    }
  }
  return results;
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
 * Aggregates result objects into an XID mapping and markdown summary.
 * Formats a clean single-workload summary or a multi-workload matrix table.
 *
 * @param {Array<object>} rawResults - Array of result objects.
 * @param {object} [options]
 * @param {string} [options.title] - Optional custom markdown title.
 * @returns {{ xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean }}
 */
function aggregateResults(rawResults, { title } = {}) {
  const results = (rawResults || []).filter(Boolean);
  const xidMap = {};
  let allPassed = true;

  for (const r of results) {
    if (r.id && r.xid && r.xid !== 'skipped' && r.xid !== 'N/A') {
      xidMap[r.id] = r.xid;
    }
    if (r.status !== 'PASSED' && r.status !== 'SKIPPED') {
      allPassed = false;
    }
  }

  const defaultTitle = '## 🌟 Nightly Examples Test Summary\n';
  const isMultiple = results.length > 1;

  let statusHeader;
  if (results.length === 0) {
    statusHeader = '### ⚠️ Overall Status: NO RESULTS FOUND\n';
  } else if (isMultiple) {
    statusHeader = allPassed
      ? '### ✅ Overall Status: ALL PASSED\n'
      : '### ⚠️ Overall Status: SOME WORKLOADS FAILED\n';
  } else {
    statusHeader = allPassed
      ? '### ✅ Overall Status: PASSED\n'
      : '### ❌ Overall Status: FAILED\n';
  }

  const summaryLines = [
    title || defaultTitle,
    statusHeader
  ];

  if (results.length === 0) {
    summaryLines.push('_No example test results found._');
  } else if (isMultiple) {
    summaryLines.push('| Example ID | Name | Status | Launch | Verify | Experiment / Identifier |');
    summaryLines.push('| --- | --- | --- | --- | --- | --- |');

    for (const r of results) {
      const id = r.id || '';
      const name = r.name || '';
      const statusBadge = formatStatusBadge(r.status);
      const launchStatus = r.launch_status || 'N/A';
      const verifyStatus = r.verify_status?.toLowerCase() === 'skipped' ? '⏭️ Skipped' : r.verify_status || 'N/A';
      const xid = r.xid || 'N/A';

      summaryLines.push(
        `| \`${id}\` | ${name} | ${statusBadge} | \`${launchStatus}\` | \`${verifyStatus}\` | \`${xid}\` |`
      );
    }
  } else {
    const r = results[0];
    const id = r.id || 'N/A';
    const name = r.name || 'N/A';
    const statusBadge = formatStatusBadge(r.status);
    const launchStatus = r.launch_status || 'N/A';
    const verifyStatus = r.verify_status?.toLowerCase() === 'skipped' ? '⏭️ Skipped' : r.verify_status || 'N/A';
    const xid = r.xid || 'N/A';

    summaryLines.push('| Property | Value |');
    summaryLines.push('| --- | --- |');
    summaryLines.push(`| **Example ID** | \`${id}\` |`);
    summaryLines.push(`| **Name** | ${name} |`);
    summaryLines.push(`| **Status** | ${statusBadge} |`);
    summaryLines.push(`| **Launch Step** | \`${launchStatus}\` |`);
    summaryLines.push(`| **Verify Step** | \`${verifyStatus}\` |`);
    summaryLines.push(`| **Experiment / Identifier** | \`${xid}\` |`);
  }

  const summaryMarkdown = summaryLines.join('\n') + '\n';
  return { xidMap, summaryMarkdown, allPassed };
}

/**
 * Main entrypoint invoked by GitHub Actions runner.
 *
 * @param {object} [options]
 * @param {string} [options.resultsDir='results']
 * @param {string} [options.stepSummaryPath]
 * @param {string} [options.githubOutputPath]
 * @param {string} [options.title]
 * @returns {{ results: Array<object>, xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean }}
 */
function run({
  resultsDir = 'results',
  stepSummaryPath = process.env.GITHUB_STEP_SUMMARY,
  githubOutputPath = process.env.GITHUB_OUTPUT,
  title
} = {}) {
  const results = loadResultsFromDir(resultsDir);
  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results, { title });

  if (stepSummaryPath) {
    fs.appendFileSync(stepSummaryPath, summaryMarkdown, 'utf8');
  }

  const xidsJson = JSON.stringify(xidMap);
  console.log(`Aggregated launched_xids: ${xidsJson}`);
  console.log(summaryMarkdown);

  if (githubOutputPath) {
    fs.appendFileSync(githubOutputPath, `launched_xids=${xidsJson}\n`, 'utf8');
  }

  return { results, xidMap, summaryMarkdown, allPassed };
}

if (require.main === module) {
  const { allPassed } = run();
  if (!allPassed) {
    console.error('::error::One or more example workloads failed launch or verification.');
    process.exit(1);
  }
}

module.exports = {
  loadResultsFromDir,
  formatStatusBadge,
  aggregateResults,
  run
};
