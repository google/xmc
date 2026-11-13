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
const { spawnSync } = require('node:child_process');
const {
  loadResultsFromDir,
  formatStatusBadge,
  aggregateResults,
  resolveFailureAnnotation,
  run
} = require('./aggregate-example-results');

test('formatStatusBadge returns appropriate badge for each status', () => {
  assert.strictEqual(formatStatusBadge('PASSED'), '✅ PASSED');
  assert.strictEqual(formatStatusBadge('SKIPPED'), '⏭️ SKIPPED');
  assert.strictEqual(formatStatusBadge('FAILED'), '❌ FAILED');
  assert.strictEqual(formatStatusBadge('VERIFY_FAILED'), '❌ FAILED');
  assert.strictEqual(formatStatusBadge('UNKNOWN'), '❌ FAILED');
});

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

test('loadResultsFromDir pushes synthetic FAILED entry for malformed JSON files', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-results-test-'));
  try {
    fs.writeFileSync(path.join(tmpDir, 'bad.json'), 'invalid json {[');
    fs.writeFileSync(
      path.join(tmpDir, 'good.json'),
      JSON.stringify({ id: 'good', name: 'Good', status: 'PASSED', xid: '200' })
    );

    const results = loadResultsFromDir(tmpDir);
    assert.strictEqual(results.length, 2);
    assert.deepStrictEqual(results[0], {
      id: 'bad',
      name: 'bad.json',
      status: 'FAILED',
      launch_status: 'parse_error',
      verify_status: 'N/A',
      xid: 'N/A',
    });
    assert.strictEqual(results[1].id, 'good');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('aggregateResults builds 2-column property table for single test result (passed)', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Multi-Unit Job',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '6018'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, true);
  assert.deepStrictEqual(xidMap, {
    simple_job: '6018'
  });
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ✅ Overall Status: PASSED'));
  assert.ok(summaryMarkdown.includes('| Property | Value |'));
  assert.ok(summaryMarkdown.includes('| **Example ID** | `simple_job` |'));
  assert.ok(summaryMarkdown.includes('| **Name** | Simple Multi-Unit Job |'));
  assert.ok(summaryMarkdown.includes('| **Status** | ✅ PASSED |'));
  assert.ok(summaryMarkdown.includes('| **Launch Step** | `success` |'));
  assert.ok(summaryMarkdown.includes('| **Verify Step** | `success` |'));
  assert.ok(summaryMarkdown.includes('| **Experiment / Identifier** | `6018` |'));
});

test('aggregateResults builds 2-column property table for single test result (failed)', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Multi-Unit Job',
      status: 'FAILED',
      launch_status: 'failure',
      verify_status: 'skipped',
      xid: 'N/A'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, {});
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ❌ Overall Status: FAILED'));
  assert.ok(summaryMarkdown.includes('| **Status** | ❌ FAILED |'));
  assert.ok(summaryMarkdown.includes('| **Launch Step** | `failure` |'));
  assert.ok(summaryMarkdown.includes('| **Verify Step** | `⏭️ Skipped` |'));
  assert.ok(summaryMarkdown.includes('| **Experiment / Identifier** | `N/A` |'));
});

test('aggregateResults builds 6-column matrix table for multiple test results (all passed)', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Multi-Unit Job',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '6018'
    },
    {
      id: 'sidecar',
      name: 'Sidecar Container Job',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '6019'
    },
    {
      id: 'skipped_job',
      name: 'Skipped Job',
      status: 'SKIPPED',
      launch_status: 'skipped',
      verify_status: 'skipped',
      xid: 'skipped'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, true);
  assert.deepStrictEqual(xidMap, {
    simple_job: '6018',
    sidecar: '6019'
  });
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ✅ Overall Status: ALL PASSED'));
  assert.ok(
    summaryMarkdown.includes('| Example ID | Name | Status | Launch | Verify | Experiment / Identifier |')
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `simple_job` | Simple Multi-Unit Job | ✅ PASSED | `success` | `success` | `6018` |'
    )
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `sidecar` | Sidecar Container Job | ✅ PASSED | `success` | `success` | `6019` |'
    )
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `skipped_job` | Skipped Job | ⏭️ SKIPPED | `skipped` | `⏭️ Skipped` | `skipped` |'
    )
  );
});

test('aggregateResults builds 6-column matrix table for multiple test results (some failed)', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Job (3 Multi-Unit Batch)',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '8001'
    },
    {
      id: 'sidecar',
      name: 'Sidecar Container Job',
      status: 'VERIFY_FAILED',
      launch_status: 'success',
      verify_status: 'failure',
      xid: '8002'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, {
    simple_job: '8001',
    sidecar: '8002'
  });
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ⚠️ Overall Status: SOME WORKLOADS FAILED'));
  assert.ok(
    summaryMarkdown.includes('| Example ID | Name | Status | Launch | Verify | Experiment / Identifier |')
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `simple_job` | Simple Job (3 Multi-Unit Batch) | ✅ PASSED | `success` | `success` | `8001` |'
    )
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `sidecar` | Sidecar Container Job | ❌ FAILED | `success` | `failure` | `8002` |'
    )
  );
});

test('aggregateResults handles empty results directory / array as failure', () => {
  const { xidMap, summaryMarkdown, allPassed } = aggregateResults([]);

  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, {});
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ⚠️ Overall Status: NO RESULTS FOUND'));
  assert.ok(summaryMarkdown.includes('_No example test results found._'));
});

test('aggregateResults uses custom title when provided', () => {
  const results = [
    { id: 'simple_job', name: 'Simple Job', status: 'PASSED', xid: '9000' }
  ];
  const { summaryMarkdown } = aggregateResults(results, { title: '## Custom Test Summary\n' });
  assert.ok(summaryMarkdown.startsWith('## Custom Test Summary\n'));
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
    assert.ok(summaryContent.includes('## 🌟 Nightly Examples Test Summary'));
    assert.ok(summaryContent.includes('| **Example ID** | `job1` |'));

    const outputContent = fs.readFileSync(outputFile, 'utf8');
    assert.strictEqual(outputContent.trim(), 'launched_xids={"job1":"1234"}');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('CLI execution exits with code 0 on all workloads passing', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-cli-pass-test-'));
  const resultsDir = path.join(tmpDir, 'results');
  const scriptPath = path.join(__dirname, 'aggregate-example-results.js');

  try {
    fs.mkdirSync(resultsDir);
    fs.writeFileSync(
      path.join(resultsDir, 'job1.json'),
      JSON.stringify({ id: 'job1', name: 'Job One', status: 'PASSED', xid: '1234' })
    );

    const proc = spawnSync(process.execPath, [scriptPath], {
      cwd: tmpDir,
      encoding: 'utf8'
    });

    assert.strictEqual(proc.status, 0);
    assert.ok(proc.stdout.includes('Aggregated launched_xids: {"job1":"1234"}'));
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('CLI execution exits with code 1 and error annotation when workloads fail', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-cli-fail-test-'));
  const resultsDir = path.join(tmpDir, 'results');
  const scriptPath = path.join(__dirname, 'aggregate-example-results.js');

  try {
    fs.mkdirSync(resultsDir);
    fs.writeFileSync(
      path.join(resultsDir, 'job1.json'),
      JSON.stringify({
        id: 'job1',
        name: 'Job One',
        status: 'VERIFY_FAILED',
        launch_status: 'success',
        verify_status: 'failure',
        xid: '1234'
      })
    );

    const proc = spawnSync(process.execPath, [scriptPath], {
      cwd: tmpDir,
      encoding: 'utf8'
    });

    assert.strictEqual(proc.status, 1);
    assert.ok(
      proc.stderr.includes(
        '::error::One or more example workloads failed launch or verification: job1 (VERIFY_FAILED)'
      )
    );
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('aggregateResults treats all-SKIPPED results as failure', () => {
  const results = [
    { id: 'job1', name: 'Job 1', status: 'SKIPPED', xid: 'skipped' },
    { id: 'job2', name: 'Job 2', status: 'SKIPPED', xid: 'skipped' }
  ];
  const { allPassed, summaryMarkdown } = aggregateResults(results);
  assert.strictEqual(allPassed, false);
  assert.ok(summaryMarkdown.includes('### ⚠️ Overall Status: SOME WORKLOADS FAILED'));
  assert.strictEqual(
    resolveFailureAnnotation(results),
    '::error::No example workloads passed (all workloads were skipped).'
  );
});

test('aggregateResults treats non-success matrixResult as failure even if uploaded results passed', () => {
  const results = [
    { id: 'job1', name: 'Job 1', status: 'PASSED', xid: '100' }
  ];
  const { allPassed, summaryMarkdown } = aggregateResults(results, { matrixResult: 'failure' });
  assert.strictEqual(allPassed, false);
  assert.ok(summaryMarkdown.includes('### ❌ Overall Status: FAILED'));
  assert.strictEqual(
    resolveFailureAnnotation(results, 'failure'),
    '::error::One or more example workloads failed launch or verification (matrix status: failure).'
  );
});

test('CLI execution exits with code 1 and error annotation when results directory is empty', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'xmc-cli-empty-test-'));
  const resultsDir = path.join(tmpDir, 'results');
  const scriptPath = path.join(__dirname, 'aggregate-example-results.js');

  try {
    fs.mkdirSync(resultsDir);

    const proc = spawnSync(process.execPath, [scriptPath], {
      cwd: tmpDir,
      encoding: 'utf8'
    });

    assert.strictEqual(proc.status, 1);
    assert.ok(
      proc.stderr.includes('::error::No example workload results found.')
    );
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('aggregateResults handles null, undefined, missing id, and invalid entries gracefully', () => {
  const rawResults = [
    null,
    undefined,
    { id: 'job1', name: 'Job 1', status: 'PASSED', xid: '100' },
    { xid: '200', name: 'No ID Job', status: 'PASSED' },
    { id: 'job2', name: 'Job 2', status: 'SKIPPED', xid: 'skipped' },
    { id: 'job3', name: 'Job 3', status: 'FAILED', xid: 'N/A' }
  ];

  const { xidMap, allPassed } = aggregateResults(rawResults);
  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, { job1: '100' });
  assert.strictEqual('undefined' in xidMap, false);
});


test('aggregateResults handles launch-only mode (verify skipped but status passed)', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Multi-Unit Job',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'skipped',
      xid: '8008'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, true);
  assert.deepStrictEqual(xidMap, {
    simple_job: '8008'
  });
  assert.ok(summaryMarkdown.includes('## 🌟 Nightly Examples Test Summary'));
  assert.ok(summaryMarkdown.includes('### ✅ Overall Status: PASSED'));
  assert.ok(summaryMarkdown.includes('| **Status** | ✅ PASSED |'));
  assert.ok(summaryMarkdown.includes('| **Launch Step** | `success` |'));
  assert.ok(summaryMarkdown.includes('| **Verify Step** | `⏭️ Skipped` |'));
  assert.ok(summaryMarkdown.includes('| **Experiment / Identifier** | `8008` |'));
});

test('aggregateResults renders XManager Version column and disambiguates xidMap for cross-version results', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Job',
      xmanager_version: '0.8.5.post1',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '9001'
    },
    {
      id: 'simple_job',
      name: 'Simple Job',
      xmanager_version: '0.8.4',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '9002'
    },
    {
      id: 'admin_panel_ui',
      name: 'Admin Panel (Playwright UI)',
      xmanager_version: '0.8.5.post1',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: 'N/A'
    },
    {
      id: 'admin_panel_ui',
      name: 'Admin Panel (Playwright UI)',
      xmanager_version: '0.8.4',
      status: 'SKIPPED',
      launch_status: 'skipped',
      verify_status: 'skipped',
      xid: 'skipped'
    },
    {
      id: 'sidecar',
      name: 'Sidecar Job',
      xmanager_version: '0.8.5.post1',
      status: 'PASSED',
      launch_status: 'success',
      verify_status: 'success',
      xid: '9003'
    },
    {
      id: 'sidecar',
      name: 'Sidecar Job',
      xmanager_version: '0.8.4',
      status: 'SKIPPED',
      launch_status: 'skipped',
      verify_status: 'skipped',
      xid: 'skipped'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, true);
  assert.deepStrictEqual(xidMap, {
    'simple_job@0.8.5.post1': '9001',
    'simple_job@0.8.4': '9002',
    sidecar: '9003'
  });
  assert.ok(
    summaryMarkdown.includes(
      '| Example ID | Name | XManager Version | Status | Launch | Verify | Experiment / Identifier |'
    )
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `simple_job` | Simple Job | `0.8.5.post1` | ✅ PASSED | `success` | `success` | `9001` |'
    )
  );
  assert.ok(
    summaryMarkdown.includes(
      '| `simple_job` | Simple Job | `0.8.4` | ✅ PASSED | `success` | `success` | `9002` |'
    )
  );
});

test('aggregateResults single result includes XManager Version row and resolveFailureAnnotation includes version', () => {
  const results = [
    {
      id: 'simple_job',
      name: 'Simple Job',
      xmanager_version: '0.8.4',
      status: 'VERIFY_FAILED',
      launch_status: 'success',
      verify_status: 'failure',
      xid: '9010'
    }
  ];

  const { xidMap, summaryMarkdown, allPassed } = aggregateResults(results);

  assert.strictEqual(allPassed, false);
  assert.deepStrictEqual(xidMap, { simple_job: '9010' });
  assert.ok(summaryMarkdown.includes('| **XManager Version** | `0.8.4` |'));
  assert.strictEqual(
    resolveFailureAnnotation(results),
    '::error::One or more example workloads failed launch or verification: simple_job@0.8.4 (VERIFY_FAILED)'
  );
});
