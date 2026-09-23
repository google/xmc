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
    it('handles objects, JSON strings, and file paths', () => {
      assert.deepEqual(formatter.parseManifest({ xid: '1' }), { xid: '1' });
      assert.equal(formatter.parseManifest('{"xid":"2"}').xid, '2');
      const file = path.join(tmpDir, 'manifest.json');
      fs.writeFileSync(file, JSON.stringify({ xid: '3' }));
      assert.equal(formatter.parseManifest(file).xid, '3');
    });

    it('does not check filesystem when input is a JSON string', () => {
      let existsCalled = false;
      const originalExistsSync = fs.existsSync;
      fs.existsSync = (p) => {
        existsCalled = true;
        return originalExistsSync(p);
      };
      try {
        const parsed = formatter.parseManifest('{"xid": "999"}');
        assert.equal(parsed.xid, '999');
        assert.equal(existsCalled, false);
      } finally {
        fs.existsSync = originalExistsSync;
      }
    });

    it('throws on invalid or empty input', () => {
      assert.throws(() => formatter.parseManifest('{bad'), /Failed to parse manifest JSON/);
      assert.throws(() => formatter.parseManifest(''), /Manifest path or content cannot be empty/);
    });
  });

  describe('formatRoutesTable', () => {
    it('formats captures into markdown table and escapes pipes', () => {
      const captures = [{ id: '01', title: 'List | Table', route: '/exp', filename: '01.png' }];
      const table = formatter.formatRoutesTable({ captures });
      assert.match(table, /\| `01` \| List \\\| Table \| `\/exp` \| `screenshots\/01\.png` \|/);
      assert.match(formatter.formatRoutesTable([]), /\| Route ID \| Page Title \| Route Path \| Screenshot \|/);
    });

    it('sanitizes newlines in table cells', () => {
      const captures = [
        {
          id: '01',
          title: 'Line 1\nLine 2\r\nLine 3',
          route: '/path\nwith\nnewline',
          filename: '01.png',
        },
      ];
      const table = formatter.formatRoutesTable({ captures });
      assert.match(table, /\| Line 1 Line 2 Line 3 \|/);
      assert.match(table, /`\/path with newline`/);
      assert.doesNotMatch(table, /\nLine 2/);
    });
  });

  describe('formatStepSummary', () => {
    it('generates summary with metadata and falls back gracefully', () => {
      const summary = formatter.formatStepSummary({
        manifest: { xid: '55', ui_base_url: 'https://xmc.dev' },
        pageUrl: 'https://pages.dev',
      });
      assert.match(summary, /## 📸 Weekly Visual UI Gallery Summary/);
      assert.match(summary, /Live Interactive Gallery URL\*\*: https:\/\/pages\.dev/);
      assert.match(formatter.formatStepSummary({}), /Live Interactive Gallery URL\*\*: N\/A/);
    });
  });

  describe('writeGitHubOutputs', () => {
    it('writes outputs to target file', () => {
      const out = path.join(tmpDir, 'out.txt');
      formatter.writeGitHubOutputs({ manifest: { xid: '101' }, uiUrl: 'https://xmc.dev', outputPath: out });
      const content = fs.readFileSync(out, 'utf8');
      assert.match(content, /^xid=101/m);
      assert.match(content, /^ui_url=https:\/\/xmc\.dev/m);
      assert.match(content, /routes_table<<EOF/);
    });
  });

  describe('CLI execution', () => {
    it('runs end-to-end and writes outputs', () => {
      const manifestPath = path.join(tmpDir, 'manifest.json');
      const outputPath = path.join(tmpDir, 'out.txt');
      fs.writeFileSync(manifestPath, JSON.stringify({ xid: '2026', captures: [{ id: '01', title: 'H', route: '/', filename: '1.png' }] }));

      execFileSync(
        process.execPath,
        [
          path.join(__dirname, 'format_visual_audit_summary.js'),
          '--manifest', manifestPath,
          '--ui-url', 'https://override.xmc.dev',
          '--github-output', outputPath,
        ],
        { encoding: 'utf8' }
      );

      assert.match(fs.readFileSync(outputPath, 'utf8'), /xid=2026/);
      assert.match(fs.readFileSync(outputPath, 'utf8'), /ui_url=https:\/\/override\.xmc\.dev/);
    });
  });
});
