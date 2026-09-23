#!/usr/bin/env node
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
 * @fileoverview Formats visual UI audit manifest into GitHub Actions step outputs.
 */

const fs = require('node:fs');

/** Parses a manifest.json file from disk. */
function parseManifest(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

/** Formats captures from visual audit manifest into a markdown table. */
function formatRoutesTable(manifest) {
  const captures = (manifest && manifest.captures) || [];
  const rows = captures.map(
    (c) => `| \`${c.id || ''}\` | ${c.title || ''} | \`${c.route || ''}\` | \`screenshots/${c.filename || ''}\` |`
  );
  return ['| Route ID | Page Title | Route Path | Screenshot |', '| --- | --- | --- | --- |', ...rows].join('\n');
}

/** Writes outputs to GITHUB_OUTPUT file. */
function writeGitHubOutputs(manifest, outputPath = process.env.GITHUB_OUTPUT) {
  if (!outputPath) return;
  const m = manifest || {};
  const lines = [
    `xid=${m.xid || ''}`,
    `ui_url=${m.ui_base_url || ''}`,
    'routes_table<<EOF',
    formatRoutesTable(m),
    'EOF',
  ].join('\n') + '\n';
  fs.appendFileSync(outputPath, lines, 'utf8');
}

/** Main CLI execution entrypoint. */
function run(argv = process.argv.slice(2)) {
  const manifestPath = argv[0];
  if (!manifestPath) {
    console.error('Usage: node format_visual_audit_summary.js <manifest.json>');
    process.exit(1);
  }

  const manifest = parseManifest(manifestPath);
  if (process.env.GITHUB_OUTPUT) {
    writeGitHubOutputs(manifest);
  } else {
    console.log(formatRoutesTable(manifest));
  }
}

if (require.main === module) {
  run();
}

module.exports = {
  parseManifest,
  formatRoutesTable,
  writeGitHubOutputs,
  run,
};
