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

// Paths whose line changes are excluded from the code owner grace delta budget.
const TEST_FILE_PATTERNS = [
  /_test\.go$/i,
  /\.(test|spec)\.[cm]?[jt]sx?$/i,
  /(^|\/)test_[^/]*\.py$/i,
  /_test\.py$/i,
  /(^|\/)testdata\//i
];

/**
 * Pure function: Checks if a file path is a test file or test fixture.
 * @param {string} filePath - Repository-relative file path.
 * @returns {boolean}
 */
function isTestFile(filePath) {
  if (!filePath || typeof filePath !== 'string') {
    return false;
  }
  let normalized = filePath.replace(/\\/g, '/').trim();
  // Git wraps paths containing special or non-ASCII characters in double quotes.
  if (normalized.length >= 2 && normalized.startsWith('"') && normalized.endsWith('"')) {
    normalized = normalized.slice(1, -1);
  }
  return TEST_FILE_PATTERNS.some(pattern => pattern.test(normalized));
}

module.exports = {
  TEST_FILE_PATTERNS,
  isTestFile
};
