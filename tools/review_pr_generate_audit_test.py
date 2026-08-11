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

"""Unit tests for review_pr_generate_audit tool."""

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from github_utils import GithubAPIError
from review_pr_generate_audit import (
    extract_task_references,
    format_initial_audit_markdown,
    format_rereview_audit_markdown,
    main,
    resolve_output_path,
)
from tool_utils import HandledError


class TestReviewPRGenerateAudit(unittest.TestCase):
    """Test suite for review_pr_generate_audit tool."""

    def test_extract_task_references(self):
        desc = "Fixes bug b/123456 and b/7890. Also references https://github.com/google/xmc/issues/42."
        refs = extract_task_references(desc)
        self.assertIn("b/123456", refs)
        self.assertIn("b/7890", refs)
        self.assertIn("https://github.com/google/xmc/issues/42", refs)

    def test_extract_task_references_empty(self):
        self.assertEqual(extract_task_references(""), [])
        self.assertEqual(extract_task_references(None), [])

    def test_resolve_output_path_explicit(self):
        p = resolve_output_path("/tmp/custom.md", "", "default.md")
        self.assertEqual(p, Path("/tmp/custom.md"))

    def test_resolve_output_path_conv_id(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(os.environ, {"JETSKI_ARTIFACT_DIR": tmpdir}):
                p = resolve_output_path("", "test-conv-123", "audit.md")
                self.assertEqual(p, Path(tmpdir) / "audit.md")

    def test_format_initial_audit_markdown(self):
        data = {
            "pr_info": {
                "title": "feat: add oauth handling",
                "body": "Fixes b/999. Implements new flow.",
                "head": {"sha": "abc1234", "ref": "feat/oauth"},
                "base": {"ref": "main"},
            },
            "files": [{"filename": "pkg/auth/handler.go", "additions": 20, "deletions": 5}],
            "comments": [
                {
                    "id": 101,
                    "path": "pkg/auth/handler.go",
                    "line": 15,
                    "user": {"login": "alice"},
                    "body": "Consider validating nil here.",
                }
            ],
        }
        md = format_initial_audit_markdown("google", "xmc", 42, data)
        self.assertIn("# PR Review Audit Checklist & Direction Plan", md)
        self.assertIn("b/999", md)
        self.assertIn("pkg/auth/handler.go", md)
        self.assertIn("Comment ID `101`", md)

    def test_format_rereview_audit_markdown(self):
        data = {
            "pr_info": {
                "title": "feat: add oauth handling",
                "head": {"sha": "abc1234"},
            },
            "comments": [
                {
                    "id": 101,
                    "path": "pkg/auth/handler.go",
                    "line": 15,
                    "user": {"login": "alice"},
                    "body": "Consider validating nil here.",
                },
                {
                    "id": 201,
                    "in_reply_to_id": 101,
                    "user": {"login": "bob"},
                    "body": "Done, added check.",
                },
            ],
        }
        md = format_rereview_audit_markdown("google", "xmc", 42, data)
        self.assertIn("# PR Re-Review Audit Report & Thread Resolution Plan", md)
        self.assertIn("Thread ID `101`", md)
        self.assertIn("Done, added check.", md)

    @patch("review_pr_generate_audit.call_github_api")
    def test_main_initial_success(self, mock_api):
        mock_api.side_effect = [
            {"title": "test", "body": "desc", "head": {"sha": "123"}},
            [{"filename": "a.py"}],
            [],
        ]
        with tempfile.TemporaryDirectory() as tmpdir:
            out_file = str(Path(tmpdir) / "test_audit.md")
            args = MagicMock()
            args.owner = "google"
            args.repo = "xmc"
            args.pr_number = 10
            args.mode = "initial"
            args.output = out_file
            args.conv_id = None

            res = main(args)
            self.assertEqual(res["status"], "success")
            self.assertEqual(res["file_count"], 1)
            self.assertTrue(Path(out_file).exists())

    @patch("review_pr_generate_audit.call_github_api")
    def test_main_api_error_raises_handled_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("404 Not Found")
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 10
        args.mode = "initial"
        args.output = "/tmp/out.md"
        args.conv_id = None

        with self.assertRaises(HandledError):
            main(args)


if __name__ == "__main__":
    unittest.main()
