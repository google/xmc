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

"""Unit tests for verify_pr_comment_resolution tool."""

import tempfile
import unittest
from unittest.mock import MagicMock, patch

from github_utils import GithubAPIError, GithubAuthError
from tool_utils import HandledError
from verify_pr_comment_resolution import verify_resolution


class TestVerifyPRCommentResolution(unittest.TestCase):
    """Test suite for verify_pr_comment_resolution tool."""

    def test_missing_checklist_file(self):
        """Test verify_resolution raises HandledError when checklist file does not exist."""
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = "/path/does/not/exist.md"

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("does not exist", ctx.exception.message)

    def test_no_comment_ids_in_checklist(self):
        """Test verify_resolution raises HandledError when no comment IDs are parsed."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# Checklist\n- [ ] Item without ID\n")
            f_path = f.name

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("No valid comment IDs found", ctx.exception.message)

    @patch("verify_pr_comment_resolution.call_github_api")
    def test_verify_resolution_success(self, mock_api):
        """Test successful verification when all checklist IDs have inline replies."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n- [ ] **Comment ID `102`**\n")
            f_path = f.name

        mock_api.return_value = [
            {"id": 201, "in_reply_to_id": 101},
            {"id": 202, "in_reply_to_id": 102},
        ]

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        res = verify_resolution(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["total_checklist_items"], 2)
        self.assertEqual(res["resolved_items"], 2)
        self.assertEqual(res["unresolved_ids"], [])

    @patch("verify_pr_comment_resolution.call_github_api")
    def test_verify_resolution_unresolved_comments(self, mock_api):
        """Test verify_resolution raises HandledError when some comment IDs lack replies."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n- [ ] **Comment ID `102`**\n")
            f_path = f.name

        mock_api.return_value = [
            {"id": 201, "in_reply_to_id": 101},
        ]

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("1-to-1 Resolution Verification Failed", ctx.exception.message)
        self.assertIn("[102]", ctx.exception.message)

    @patch("verify_pr_comment_resolution.call_github_api")
    def test_github_auth_error(self, mock_api):
        """Test verify_resolution handles GithubAuthError."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n")
            f_path = f.name

        mock_api.side_effect = GithubAuthError("Auth error")

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("Auth error", ctx.exception.message)

    @patch("verify_pr_comment_resolution.call_github_api")
    def test_github_api_error(self, mock_api):
        """Test verify_resolution handles GithubAPIError."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n")
            f_path = f.name

        mock_api.side_effect = GithubAPIError("API error")

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("API error", ctx.exception.message)


if __name__ == "__main__":
    unittest.main()
