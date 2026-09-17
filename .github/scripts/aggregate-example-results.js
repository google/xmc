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
 * Aggregates result objects into an XID mapping and markdown summary.
 *
 * @param {Array<object>} results - Array of result objects { id, name, status, xid }.
 * @returns {{ xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean }}
 */
function aggregateResults(results) {
  const xidMap = {};
  let allPassed = true;

  for (const r of results) {
    if (r && r.xid && r.xid !== 'skipped' && r.xid !== 'N/A') {
      xidMap[r.id] = r.xid;
    }
    if (r && r.status === 'FAILED') {
      allPassed = false;
    }
  }

  const summaryLines = [
    '## 🌟 All Example Workloads Execution Summary\n',
    allPassed
      ? '### ✅ Overall Status: ALL PASSED\n'
      : '### ⚠️ Overall Status: SOME WORKLOADS FAILED\n',
    '| Example ID | Name | Status | Experiment / Identifier |',
    '| --- | --- | --- | --- |'
  ];

  for (const r of results) {
    const id = r.id || '';
    const name = r.name || '';
    const status = r.status || '';
    const xid = r.xid || '';
    const statusBadge = status === 'PASSED'
      ? '✅ PASSED'
      : (status === 'SKIPPED' ? '⏭️ SKIPPED' : '❌ FAILED');

    summaryLines.push(`| \`${id}\` | ${name} | ${statusBadge} | \`${xid}\` |`);
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
 * @returns {{ results: Array<object>, xidMap: Record<string, string>, summaryMarkdown: string, allPassed: boolean }}
 */
function run({
  resultsDir = 'results',
  stepSummaryPath = process.env.GITHUB_STEP_SUMMARY,
  githubOutputPath = process.env.GITHUB_OUTPUT
} = {}) {
  const results = loadResultsFromDir(resultsDir);
  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  if (stepSummaryPath) {
    fs.appendFileSync(stepSummaryPath, summaryMarkdown, 'utf8');
  }

  const xidsJson = JSON.stringify(xidMap);
  console.log(`Aggregated launched_xids: ${xidsJson}`);

  if (githubOutputPath) {
    fs.appendFileSync(githubOutputPath, `launched_xids=${xidsJson}\n`, 'utf8');
  }

  return { results, xidMap, summaryMarkdown, allPassed };
}

if (require.main === module) {
  run();
}

module.exports = {
  loadResultsFromDir,
  aggregateResults,
  run
};
