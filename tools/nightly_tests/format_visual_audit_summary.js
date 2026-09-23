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
 * @fileoverview Formats visual UI audit manifest into GitHub Actions step outputs and summaries.
 */

const fs = require('node:fs');

/** Parses a manifest file or raw JSON content into an object. */
function parseManifest(input) {
  if (typeof input === 'object' && input !== null) return input;
  if (typeof input !== 'string' || !input.trim()) {
    throw new Error('Manifest path or content cannot be empty.');
  }
  const trimmed = input.trim();
  const isJson = trimmed.startsWith('{') || trimmed.startsWith('[');
  const raw = (!isJson && fs.existsSync(input)) ? fs.readFileSync(input, 'utf8') : input;
  try {
    return JSON.parse(raw);
  } catch (err) {
    throw new Error(`Failed to parse manifest JSON: ${err.message}`);
  }
}

/** Formats captures from visual audit manifest into a markdown table. */
function formatRoutesTable(manifestOrCaptures) {
  const captures = Array.isArray(manifestOrCaptures)
    ? manifestOrCaptures
    : (manifestOrCaptures && manifestOrCaptures.captures) || [];
  const esc = (val) => (val != null ? String(val).replace(/\r?\n/g, ' ').replace(/\|/g, '\\|') : '');
  const rows = captures.map(
    (c) => `| \`${esc(c.id)}\` | ${esc(c.title)} | \`${esc(c.route)}\` | \`screenshots/${esc(c.filename)}\` |`
  );
  return ['| Route ID | Page Title | Route Path | Screenshot |', '| --- | --- | --- | --- |', ...rows].join('\n');
}

/** Formats GitHub Step Summary markdown. */
function formatStepSummary(options = {}) {
  const m = options.manifest || {};
  const pageUrl = options.pageUrl || 'N/A';
  const xid = options.xid || m.xid || 'N/A';
  const uiUrl = options.uiUrl || m.ui_base_url || 'N/A';
  const table = options.routesTable || formatRoutesTable(m);
  return [
    '## 📸 Weekly Visual UI Gallery Summary\n',
    '- **Actions Run Artifact**: `weekly-visual-gallery`',
    `- **Live Interactive Gallery URL**: ${pageUrl}`,
    `- **Audited Experiment XID**: \`${xid}\``,
    `- **Target UI Base URL**: \`${uiUrl}\`\n`,
    table,
    '',
  ].join('\n');
}

/** Writes outputs to GITHUB_OUTPUT file. */
function writeGitHubOutputs(options) {
  const opts = options && typeof options === 'object' && options.manifest ? options : { manifest: options };
  const target = opts.outputPath || process.env.GITHUB_OUTPUT;
  if (!target) return;
  const m = opts.manifest || {};
  const xid = m.xid != null ? String(m.xid) : '';
  const resolvedUi = opts.uiUrl || m.ui_base_url || '';
  const table = formatRoutesTable(m);
  fs.appendFileSync(target, `xid=${xid}\nui_url=${resolvedUi}\nroutes_table<<EOF\n${table}\nEOF\n`, 'utf8');
}

/** Main CLI execution entrypoint. */
function run(argv = process.argv.slice(2)) {
  let manifestPath = '';
  let uiUrl = process.env.UI_URL || '';
  let outputPath = process.env.GITHUB_OUTPUT || '';

  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--manifest' && argv[i + 1]) manifestPath = argv[++i];
    else if (a.startsWith('--manifest=')) manifestPath = a.slice(11);
    else if (a === '--ui-url' && argv[i + 1]) uiUrl = argv[++i];
    else if (a.startsWith('--ui-url=')) uiUrl = a.slice(9);
    else if (a === '--github-output' && argv[i + 1]) outputPath = argv[++i];
    else if (a.startsWith('--github-output=')) outputPath = a.slice(16);
    else if (!a.startsWith('-') && !manifestPath) manifestPath = a;
  }

  if (!manifestPath) {
    console.error('Usage: node format_visual_audit_summary.js <manifest.json> [--ui-url <url>]');
    process.exit(1);
  }

  const manifest = parseManifest(manifestPath);
  if (outputPath) {
    writeGitHubOutputs({ manifest, uiUrl, outputPath });
  } else {
    console.log(formatRoutesTable(manifest));
  }
}

if (require.main === module) {
  try {
    run();
  } catch (err) {
    console.error(`Error: ${err.message}`);
    process.exit(1);
  }
}

module.exports = {
  parseManifest,
  formatRoutesTable,
  formatStepSummary,
  writeGitHubOutputs,
  run,
};
