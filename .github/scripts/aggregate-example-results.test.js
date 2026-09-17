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

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {
  loadResultsFromDir,
  aggregateResults,
  run
} = require('./aggregate-example-results');

test('loadResultsFromDir returns empty array when directory does not exist', () => {
  const nonExistent = path.join(os.tmpdir(), 'xmc-non-existent-dir-' + Date.now());
  const results = loadResultsFromDir(nonExistent);
  assert.deepStrictEqual(results, []);
});

test('loadResultsFromDir loads and parses all JSON files in sorted order', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-results-test-'));
  try {
    fs.writeFileSync(
      path.join(tmpDir, 'simple_job.json'),
      JSON.stringify({ id: 'simple_job', name: 'Simple Job', status: 'PASSED', xid: '100' })
    );
    fs.writeFileSync(
      path.join(tmpDir, 'sidecar.json'),
      JSON.stringify({ id: 'sidecar', name: 'Sidecar', status: 'PASSED', xid: '101' })
    );
    fs.writeFileSync(
      path.join(tmpDir, 'ignored.txt'),
      'not a json file'
    );

    const results = loadResultsFromDir(tmpDir);
    assert.strictEqual(results.length, 2);
    assert.strictEqual(results[0].id, 'sidecar');
    assert.strictEqual(results[1].id, 'simple_job');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('loadResultsFromDir gracefully handles malformed JSON files', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-results-test-'));
  try {
    fs.writeFileSync(path.join(tmpDir, 'bad.json'), 'invalid json {[');
    fs.writeFileSync(
      path.join(tmpDir, 'good.json'),
      JSON.stringify({ id: 'good', name: 'Good', status: 'PASSED', xid: '200' })
    );

    const results = loadResultsFromDir(tmpDir);
    assert.strictEqual(results.length, 1);
    assert.strictEqual(results[0].id, 'good');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('aggregateResults builds correct xidMap and passes status when all workloads pass', () => {
  const results = [
    { id: 'simple_job', name: 'Simple Multi-Unit Job', status: 'PASSED', xid: '6018' },
    { id: 'sidecar', name: 'Sidecar Container Job', status: 'PASSED', xid: '6019' },
    { id: 'skipped_job', name: 'Skipped Job', status: 'SKIPPED', xid: 'skipped' }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, true);
  assert.deepStrictEqual(xidMap, {
    simple_job: '6018',
    sidecar: '6019'
  });
  assert.ok(summaryMarkdown.includes('### ✅ Overall Status: ALL PASSED'));
  assert.ok(summaryMarkdown.includes('| `simple_job` | Simple Multi-Unit Job | ✅ PASSED | `6018` |'));
  assert.ok(summaryMarkdown.includes('| `skipped_job` | Skipped Job | ⏭️ SKIPPED | `skipped` |'));
});

test('aggregateResults marks allPassed false when any workload fails', () => {
  const results = [
    { id: 'simple_job', name: 'Simple Multi-Unit Job', status: 'PASSED', xid: '6018' },
    { id: 'failed_job', name: 'Failed Job', status: 'FAILED', xid: 'N/A' }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, { simple_job: '6018' });
  assert.ok(summaryMarkdown.includes('### ⚠️ Overall Status: SOME WORKLOADS FAILED'));
  assert.ok(summaryMarkdown.includes('| `failed_job` | Failed Job | ❌ FAILED | `N/A` |'));
});

test('run entrypoint writes to GITHUB_STEP_SUMMARY and GITHUB_OUTPUT files', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-run-test-'));
  const resultsDir = path.join(tmpDir, 'results');
  const summaryFile = path.join(tmpDir, 'step_summary.md');
  const outputFile = path.join(tmpDir, 'github_output.txt');

  try {
    fs.mkdirSync(resultsDir);
    fs.writeFileSync(
      path.join(resultsDir, 'job1.json'),
      JSON.stringify({ id: 'job1', name: 'Job One', status: 'PASSED', xid: '1234' })
    );

    const { xidMap, allPassed } = run({
      resultsDir,
      stepSummaryPath: summaryFile,
      githubOutputPath: outputFile
    });

    assert.strictEqual(allPassed, true);
    assert.deepStrictEqual(xidMap, { job1: '1234' });

    const summaryContent = fs.readFileSync(summaryFile, 'utf8');
    assert.ok(summaryContent.includes('## 🌟 All Example Workloads Execution Summary'));
    assert.ok(summaryContent.includes('| `job1` | Job One | ✅ PASSED | `1234` |'));

    const outputContent = fs.readFileSync(outputFile, 'utf8');
    assert.strictEqual(outputContent.trim(), 'launched_xids={"job1":"1234"}');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});
