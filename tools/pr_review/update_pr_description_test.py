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

"""Unit tests for update_pr_description tool."""

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError
from tools.pr_review.update_pr_description import (
    parse_pr_identifier,
    update_pr_description,
)


class TestUpdatePRDescription(unittest.TestCase):
    """Test suite for update_pr_description functions."""

    def test_parse_pr_identifier_numeric(self):
        """Test parse_pr_identifier parses a numeric PR ID string."""
        result = parse_pr_identifier("148")
        self.assertEqual(
            result,
            {"owner": "google", "repo": "xmc", "pull_number": 148},
        )

    def test_parse_pr_identifier_url(self):
        """Test parse_pr_identifier parses a full GitHub PR URL."""
        result = parse_pr_identifier("https://github.com/my-org/my-repo/pull/42")
        self.assertEqual(
            result,
            {"owner": "my-org", "repo": "my-repo", "pull_number": 42},
        )

    def test_parse_pr_identifier_invalid(self):
        """Test parse_pr_identifier raises HandledError on malformed PR identifier."""
        with self.assertRaises(HandledError) as ctx:
            parse_pr_identifier("not-a-valid-pr")
        self.assertIn("Invalid PR identifier", ctx.exception.message)

    def test_update_pr_description_no_inputs_raises_handled_error(self):
        """Test update_pr_description raises HandledError if no title or body is given."""
        args = MagicMock()
        args.pr = "148"
        args.title = None
        args.body = None
        args.body_file = None

        with self.assertRaises(HandledError) as ctx:
            update_pr_description(args)
        self.assertIn("None of --title, --body, or --body-file was provided", ctx.exception.message)

    def test_update_pr_description_body_file_not_found(self):
        """Test update_pr_description raises HandledError when body file is missing."""
        args = MagicMock()
        args.pr = "148"
        args.title = None
        args.body = None
        args.body_file = "/non/existent/path/description.md"

        with self.assertRaises(HandledError) as ctx:
            update_pr_description(args)
        self.assertIn("Body file not found", ctx.exception.message)

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_body_inline_success(self, mock_call):
        """Test update_pr_description updates body from inline argument."""
        mock_call.return_value = {"html_url": "https://github.com/google/xmc/pull/148"}

        args = MagicMock()
        args.pr = "148"
        args.title = None
        args.body = "New body content"
        args.body_file = None

        res = update_pr_description(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["pull_number"], 148)
        self.assertEqual(res["updated_body_length"], len("New body content"))
        mock_call.assert_called_once_with(
            "/repos/google/xmc/pulls/148",
            method="PATCH",
            payload={"body": "New body content"},
            user_agent="XMC-PR-Updater-Agent",
        )

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_body_file_success(self, mock_call):
        """Test update_pr_description updates body from a markdown file."""
        mock_call.return_value = {"html_url": "https://github.com/google/xmc/pull/148"}

        with tempfile.TemporaryDirectory() as tmpdir:
            body_file = Path(tmpdir) / "pr_body.md"
            body_file.write_text("Markdown body from file", encoding="utf-8")

            args = MagicMock()
            args.pr = "148"
            args.title = None
            args.body = None
            args.body_file = str(body_file)

            res = update_pr_description(args)
            self.assertEqual(res["status"], "success")
            self.assertEqual(res["pull_number"], 148)
            self.assertEqual(res["updated_body_length"], len("Markdown body from file"))
            mock_call.assert_called_once_with(
                "/repos/google/xmc/pulls/148",
                method="PATCH",
                payload={"body": "Markdown body from file"},
                user_agent="XMC-PR-Updater-Agent",
            )

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_title_only_success(self, mock_call):
        """Test update_pr_description updates title only."""
        mock_call.return_value = {"html_url": "https://github.com/google/xmc/pull/148"}

        args = MagicMock()
        args.pr = "148"
        args.title = "feat: new title"
        args.body = None
        args.body_file = None

        res = update_pr_description(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["pull_number"], 148)
        self.assertEqual(res["title"], "feat: new title")
        self.assertNotIn("updated_body_length", res)
        mock_call.assert_called_once_with(
            "/repos/google/xmc/pulls/148",
            method="PATCH",
            payload={"title": "feat: new title"},
            user_agent="XMC-PR-Updater-Agent",
        )

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_title_and_body_success(self, mock_call):
        """Test update_pr_description updates both title and body."""
        mock_call.return_value = {"html_url": "https://github.com/google/xmc/pull/148"}

        args = MagicMock()
        args.pr = "148"
        args.title = "fix: resolve bug"
        args.body = "Updated description"
        args.body_file = None

        res = update_pr_description(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["pull_number"], 148)
        self.assertEqual(res["title"], "fix: resolve bug")
        self.assertEqual(res["updated_body_length"], len("Updated description"))
        mock_call.assert_called_once_with(
            "/repos/google/xmc/pulls/148",
            method="PATCH",
            payload={"title": "fix: resolve bug", "body": "Updated description"},
            user_agent="XMC-PR-Updater-Agent",
        )

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_auth_error(self, mock_call):
        """Test update_pr_description converts GithubAuthError to HandledError."""
        mock_call.side_effect = GithubAuthError("Authentication failed")

        args = MagicMock()
        args.pr = "148"
        args.title = "feat: title"
        args.body = None
        args.body_file = None

        with self.assertRaises(HandledError) as ctx:
            update_pr_description(args)
        self.assertIn("Authentication failed", ctx.exception.message)
        self.assertIn("gh auth login", ctx.exception.instruction)

    @patch("tools.pr_review.update_pr_description.call_github_api")
    def test_update_pr_description_api_error(self, mock_call):
        """Test update_pr_description converts GithubAPIError to HandledError."""
        mock_call.side_effect = GithubAPIError("Resource not found")

        args = MagicMock()
        args.pr = "148"
        args.title = "feat: title"
        args.body = None
        args.body_file = None

        with self.assertRaises(HandledError) as ctx:
            update_pr_description(args)
        self.assertIn("Resource not found", ctx.exception.message)
        self.assertIn("Ensure the PR exists", ctx.exception.instruction)


if __name__ == "__main__":
    unittest.main()
