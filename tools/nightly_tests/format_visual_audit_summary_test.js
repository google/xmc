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
 * @fileoverview Unit tests for format_visual_audit_summary.js.
 */

const { describe, it, beforeEach, afterEach } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const formatter = require('./format_visual_audit_summary');

describe('format_visual_audit_summary', () => {
  let tmpDir;
  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'visual-audit-test-'));
  });
  afterEach(() => {
    if (tmpDir && fs.existsSync(tmpDir)) fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  describe('parseManifest', () => {
    it('reads and parses manifest from file path', () => {
      const file = path.join(tmpDir, 'manifest.json');
      fs.writeFileSync(file, JSON.stringify({ xid: '123', ui_base_url: 'https://xmc.test' }));
      const parsed = formatter.parseManifest(file);
      assert.deepEqual(parsed, { xid: '123', ui_base_url: 'https://xmc.test' });
    });

    it('throws on non-existent file or malformed json', () => {
      assert.throws(() => formatter.parseManifest(path.join(tmpDir, 'missing.json')));
      const bad = path.join(tmpDir, 'bad.json');
      fs.writeFileSync(bad, '{not-json');
      assert.throws(() => formatter.parseManifest(bad));
    });
  });

  describe('formatRoutesTable', () => {
    it('formats captures into markdown table rows', () => {
      const manifest = {
        captures: [
          { id: '01_exp', title: 'Experiments List', route: '/experiments', filename: '01_exp.png' },
          { id: '03_wu', title: 'Work Units', route: '/experiments/42', filename: '03_wu.png' },
        ],
      };
      const table = formatter.formatRoutesTable(manifest);
      assert.match(table, /\| `01_exp` \| Experiments List \| `\/experiments` \| `screenshots\/01_exp\.png` \|/);
      assert.match(table, /\| `03_wu` \| Work Units \| `\/experiments\/42` \| `screenshots\/03_wu\.png` \|/);
    });

    it('handles empty captures gracefully', () => {
      const table = formatter.formatRoutesTable({});
      assert.equal(table, '| Route ID | Page Title | Route Path | Screenshot |\n| --- | --- | --- | --- |');
    });

    it('formats captures with omitted fields without rendering undefined', () => {
      const table = formatter.formatRoutesTable({ captures: [{}] });
      assert.equal(table, '| Route ID | Page Title | Route Path | Screenshot |\n| --- | --- | --- | --- |\n| `` |  | `` | `screenshots/` |');
    });
  });

  describe('writeGitHubOutputs', () => {
    it('writes xid, ui_url, and routes_table to specified output path', () => {
      const out = path.join(tmpDir, 'out.txt');
      const manifest = {
        xid: '456',
        ui_base_url: 'https://xmc.test',
        captures: [{ id: '01', title: 'Title', route: '/', filename: '01.png' }],
      };
      formatter.writeGitHubOutputs(manifest, out);
      const content = fs.readFileSync(out, 'utf8');
      assert.match(content, /^xid=456$/m);
      assert.match(content, /^ui_url=https:\/\/xmc\.test$/m);
      assert.match(content, /^routes_table<<EOF/m);
      assert.match(content, /\| `01` \| Title \| `\/` \| `screenshots\/01\.png` \|/);
    });

    it('handles null or undefined manifest gracefully', () => {
      const out = path.join(tmpDir, 'out-null.txt');
      assert.doesNotThrow(() => formatter.writeGitHubOutputs(null, out));
      const content = fs.readFileSync(out, 'utf8');
      assert.match(content, /^xid=$/m);
      assert.match(content, /^ui_url=$/m);
    });

    it('noop if outputPath is not provided', () => {
      assert.doesNotThrow(() => formatter.writeGitHubOutputs({ xid: '1' }, null));
    });
  });

  describe('CLI execution', () => {
    it('runs end-to-end and writes outputs to GITHUB_OUTPUT', () => {
      const manifestPath = path.join(tmpDir, 'manifest.json');
      const outputPath = path.join(tmpDir, 'out.txt');
      fs.writeFileSync(
        manifestPath,
        JSON.stringify({
          xid: '2026',
          ui_base_url: 'https://override.xmc.dev',
          captures: [{ id: '01', title: 'H', route: '/', filename: '1.png' }],
        })
      );

      execFileSync(
        process.execPath,
        [path.join(__dirname, 'format_visual_audit_summary.js'), manifestPath],
        {
          encoding: 'utf8',
          env: { ...process.env, GITHUB_OUTPUT: outputPath },
        }
      );

      const content = fs.readFileSync(outputPath, 'utf8');
      assert.match(content, /xid=2026/);
      assert.match(content, /ui_url=https:\/\/override\.xmc\.dev/);
      assert.match(content, /routes_table<<EOF/);
    });

    it('prints table to stdout when GITHUB_OUTPUT is not set', () => {
      const manifestPath = path.join(tmpDir, 'manifest.json');
      fs.writeFileSync(
        manifestPath,
        JSON.stringify({
          xid: '2026',
          captures: [{ id: '01', title: 'H', route: '/', filename: '1.png' }],
        })
      );

      const env = { ...process.env };
      delete env.GITHUB_OUTPUT;
      const stdout = execFileSync(
        process.execPath,
        [path.join(__dirname, 'format_visual_audit_summary.js'), manifestPath],
        {
          encoding: 'utf8',
          env,
        }
      );

      assert.match(stdout, /\| Route ID \| Page Title \| Route Path \| Screenshot \|/);
      assert.match(stdout, /\| `01` \| H \| `\/` \| `screenshots\/1\.png` \|/);
    });

    it('exits with status 1 on missing manifest argument', () => {
      assert.throws(
        () =>
          execFileSync(
            process.execPath,
            [path.join(__dirname, 'format_visual_audit_summary.js')],
            { encoding: 'utf8', stdio: 'pipe' }
          ),
        (err) => err.status === 1
      );
    });
  });
});
