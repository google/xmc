# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for extract_release_prs.py."""

import argparse
import json
import subprocess
import unittest
from unittest import mock

try:
    from tools import extract_release_prs, tool_utils
except ImportError:
    import extract_release_prs
    import tool_utils


class ExtractReleasePrsTest(unittest.TestCase):
    def test_parse_scope_from_title(self):
        self.assertEqual(
            extract_release_prs.parse_scope_from_title("feat(xdash): support custom presets"),
            "xdash",
        )
        self.assertEqual(
            extract_release_prs.parse_scope_from_title("fix(rod): optimize metrics query"),
            "rod",
        )
        self.assertEqual(
            extract_release_prs.parse_scope_from_title("chore: update github workflows"),
            None,
        )
        self.assertEqual(
            extract_release_prs.parse_scope_from_title(
                "refactor(core/orchestration): cleanup jobsets"
            ),
            "core/orchestration",
        )
        self.assertEqual(
            extract_release_prs.parse_scope_from_title(
                "Plain commit message without conventional prefix"
            ),
            None,
        )

    def test_parse_pr_body_sections(self):
        body = """
## Summary
This is the summary of the PR.

## Key Changes
- Item 1
- Item 2

## Verification & Testing
Passed all tests.
"""
        sections = extract_release_prs.parse_pr_body_sections(body)
        self.assertIn("summary", sections)
        self.assertEqual(sections["summary"], "This is the summary of the PR.")
        self.assertIn("key changes", sections)
        self.assertIn("- Item 1", sections["key changes"])
        self.assertIn("verification & testing", sections)

    def test_parse_pr_body_sections_empty(self):
        self.assertEqual(extract_release_prs.parse_pr_body_sections(""), {})

    def test_parse_iso_date(self):
        dt = extract_release_prs._parse_iso_date("2026-08-20T10:00:00Z", "--since")
        self.assertEqual(dt.year, 2026)
        self.assertEqual(dt.month, 8)
        self.assertEqual(dt.day, 20)

        with self.assertRaises(tool_utils.HandledError):
            extract_release_prs._parse_iso_date("invalid-date", "--since")

    def test_calculate_time_window(self):
        since_dt, until_dt = extract_release_prs._calculate_time_window(
            "2026-08-19", "2026-08-26", 7
        )
        self.assertIsNotNone(since_dt)
        self.assertIsNotNone(until_dt)
        self.assertEqual(since_dt.day, 19)
        self.assertEqual(until_dt.day, 26)

    def test_build_gh_search_cmd(self):
        cmd = extract_release_prs._build_gh_search_cmd(
            "2026-08-19T00:00:00Z", "2026-08-26T00:00:00Z", 50
        )
        self.assertIn("gh", cmd)
        self.assertIn("merged:>=2026-08-19", cmd[-1])
        self.assertIn("merged:<=2026-08-26", cmd[-1])

    def test_parse_pr_item(self):
        item = {
            "number": 123,
            "title": "feat(auth): add spanner token cache",
            "body": "## Summary\nCached tokens.",
            "author": {"login": "tester"},
            "mergedAt": "2026-08-25T14:00:00Z",
            "url": "https://github.com/google/xmc/pull/123",
            "labels": [{"name": "backend"}],
            "files": [{"path": "pkg/auth/token.go"}],
        }
        parsed = extract_release_prs._parse_pr_item(item)
        self.assertEqual(parsed["number"], 123)
        self.assertEqual(parsed["scope"], "auth")
        self.assertEqual(parsed["author"], "tester")
        self.assertEqual(parsed["labels"], ["backend"])
        self.assertEqual(parsed["files"], ["pkg/auth/token.go"])
        self.assertEqual(parsed["sections"].get("summary"), "Cached tokens.")

    @mock.patch("shutil.which", return_value="/usr/bin/gh")
    @mock.patch("subprocess.run")
    def test_extract_prs_success(self, mock_run, mock_which):
        sample_gh_output = [
            {
                "number": 589,
                "title": "feat(xdash): reference charts by ID",
                "mergedAt": "2026-08-24T12:00:00Z",
                "author": {"login": "alice"},
                "url": "https://github.com/google/xmc/pull/589",
                "labels": [{"name": "area/dashboard"}],
                "files": [{"path": "pkg/dashboard/service.go"}],
                "body": "## Summary\nDecoupled charts.",
            },
            {
                "number": 547,
                "title": "feat(rod): add pagination to queue jobs",
                "mergedAt": "2026-08-22T10:00:00Z",
                "author": {"login": "bob"},
                "url": "https://github.com/google/xmc/pull/547",
                "labels": [],
                "files": [{"path": "cmd/ui/src/app/queue-jobs.component.ts"}],
                "body": "## Summary\nAdded pagination.",
            },
        ]
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout=json.dumps(sample_gh_output)
        )

        args = argparse.Namespace(days=7, since="2026-08-19", until="2026-08-26", limit=50)
        res = extract_release_prs.extract_prs(args)

        self.assertEqual(res["total_prs"], 2)
        self.assertEqual(len(res["prs"]), 2)
        self.assertEqual(res["prs"][0]["number"], 589)
        self.assertEqual(res["prs"][0]["scope"], "xdash")
        self.assertEqual(res["prs"][0]["files"], ["pkg/dashboard/service.go"])
        self.assertEqual(res["prs"][1]["number"], 547)
        self.assertEqual(res["prs"][1]["scope"], "rod")

    @mock.patch("shutil.which", return_value=None)
    def test_extract_prs_missing_gh(self, mock_which):
        args = argparse.Namespace(days=7, since=None, until=None, limit=50)
        with self.assertRaises(tool_utils.HandledError):
            extract_release_prs.extract_prs(args)

    @mock.patch("shutil.which", return_value="/usr/bin/gh")
    @mock.patch(
        "subprocess.run", side_effect=subprocess.CalledProcessError(1, "gh", stderr="auth error")
    )
    def test_extract_prs_gh_error(self, mock_run, mock_which):
        args = argparse.Namespace(days=7, since=None, until=None, limit=50)
        with self.assertRaises(tool_utils.HandledError):
            extract_release_prs.extract_prs(args)


if __name__ == "__main__":
    unittest.main()
