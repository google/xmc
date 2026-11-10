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

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError
from tools.pr_review.verify_pr_comment_resolution import verify_resolution

# Comments are fetched via the shared github_utils.list_all_pages helper.
_GH_API = "tools.pr_review.github_utils.call_github_api"
_LOGIN = "tools.pr_review.verify_pr_comment_resolution.get_authenticated_login"


def _reply(comment_id: int, in_reply_to_id: int, login: str = "me") -> dict:
    """Builds a live review comment replying to `in_reply_to_id`."""
    return {"id": comment_id, "in_reply_to_id": in_reply_to_id, "user": {"login": login}}


@patch(_LOGIN, return_value="me")
class TestVerifyPRCommentResolution(unittest.TestCase):
    """Test suite for verify_pr_comment_resolution tool."""

    def test_missing_checklist_file(self, _mock_login):
        """Test verify_resolution raises HandledError when checklist file does not exist."""
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = "/path/does/not/exist.md"

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("does not exist", ctx.exception.message)

    def test_no_comment_ids_in_checklist(self, _mock_login):
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

    @patch(_GH_API)
    def test_verify_resolution_success(self, mock_api, _mock_login):
        """Test successful verification when all checklist IDs have inline replies."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n- [ ] **Comment ID `102`**\n")
            f_path = f.name

        mock_api.return_value = [_reply(201, 101), _reply(202, 102)]

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

    @patch(_GH_API)
    def test_verify_resolution_unresolved_comments(self, mock_api, _mock_login):
        """Test verify_resolution raises HandledError when some comment IDs lack replies."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n- [ ] **Comment ID `102`**\n")
            f_path = f.name

        mock_api.return_value = [_reply(201, 101)]

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pull_number = 145
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("1-to-1 Resolution Verification Failed", ctx.exception.message)
        self.assertIn("[102]", ctx.exception.message)

    @patch(_GH_API)
    def test_reply_on_second_page_is_found(self, mock_api, _mock_login):
        """Regression: a reply beyond the first 100 review comments counts."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n")
            f_path = f.name
        first_page = [{"id": n, "user": {"login": "reviewer"}} for n in range(1000, 1100)]
        mock_api.side_effect = [first_page, [_reply(201, 101)]]
        args = MagicMock(owner="google", repo="xmc", pull_number=145, checklist_file=f_path)

        res = verify_resolution(args)

        self.assertEqual(res["resolved_items"], 1)
        endpoints = [c.args[0] for c in mock_api.call_args_list]
        self.assertEqual(
            endpoints,
            [
                "/repos/google/xmc/pulls/145/comments?page=1&per_page=100",
                "/repos/google/xmc/pulls/145/comments?page=2&per_page=100",
            ],
        )

    @patch(_GH_API)
    def test_reply_by_another_user_is_rejected(self, mock_api, _mock_login):
        """Regression: only replies authored by the authenticated user count."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n")
            f_path = f.name
        mock_api.return_value = [_reply(201, 101, login="someone-else")]
        args = MagicMock(owner="google", repo="xmc", pull_number=145, checklist_file=f_path)

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("[101]", ctx.exception.message)

    @patch(_GH_API)
    def test_github_auth_error(self, mock_api, _mock_login):
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

    @patch(_GH_API)
    def test_github_api_error(self, mock_api, _mock_login):
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

    def test_login_api_error_gets_auth_hint(self, mock_login):
        """An unauthenticated /user failure (GithubAPIError) points at authentication."""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("# PR Checklist\n- [ ] **Comment ID `101`**\n")
            f_path = f.name
        mock_login.side_effect = GithubAPIError("401 Unauthorized")
        args = MagicMock(owner="google", repo="xmc", pull_number=145, checklist_file=f_path)

        with self.assertRaises(HandledError) as ctx:
            verify_resolution(args)

        self.assertIn("401 Unauthorized", ctx.exception.message)
        self.assertIn("gh auth login", str(ctx.exception.instruction))


if __name__ == "__main__":
    unittest.main()
