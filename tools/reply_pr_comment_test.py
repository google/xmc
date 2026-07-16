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

"""Unit tests for reply_pr_comment tool."""

import unittest
from unittest.mock import MagicMock, patch

from github_utils import GithubAPIError, GithubAuthError
from reply_pr_comment import reply_comment
from tool_utils import HandledError


class TestReplyPRComment(unittest.TestCase):
    """Test suite for reply_pr_comment tool."""

    @patch("reply_pr_comment.parse_comment_url")
    def test_reply_comment_invalid_url(self, mock_parse):
        """Test reply_comment raises HandledError when URL is invalid."""
        mock_parse.return_value = None

        args = MagicMock()
        args.comment_url = "https://invalid.url"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)

        self.assertIn(
            "Could not parse Pull Request review comment URL.",
            ctx.exception.message,
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    @patch("reply_pr_comment.datetime")
    @patch("reply_pr_comment.subprocess.check_output")
    def test_reply_comment_success(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test successful execution of reply_comment with full telemetry."""
        mock_git.return_value = "mock_skill_commit_id"

        mock_now = MagicMock()
        mock_now.strftime.side_effect = lambda fmt: {
            "%Y-%m-%d": "2026-06-22",
            "%H:%M:%S UTC": "12:00:00 UTC",
        }[fmt]
        mock_datetime.now.return_value = mock_now

        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }

        mock_call.side_effect = [
            {"login": "test_user"},  # response for GET /user
            {"id": 5678, "body": "done"},  # response for POST comment
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)

        mock_call.assert_any_call("/user", user_agent="XMC-Comment-Replier-Agent")

        expected_body = (
            'done\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "address_pr_comment",'
            ' "skill_commit_id": "mock_skill_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    @patch("reply_pr_comment.datetime")
    @patch("reply_pr_comment.subprocess.check_output")
    def test_reply_comment_user_fetch_fails_gracefully(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test reply_comment handles user info fetch API failure gracefully."""
        mock_git.return_value = "mock_skill_commit_id"

        mock_now = MagicMock()
        mock_now.strftime.side_effect = lambda fmt: {
            "%Y-%m-%d": "2026-06-22",
            "%H:%M:%S UTC": "12:00:00 UTC",
        }[fmt]
        mock_datetime.now.return_value = mock_now

        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }

        mock_call.side_effect = [
            Exception("GitHub API down"),  # response for GET /user
            {"id": 5678, "body": "done"},  # response for POST comment
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)

        expected_body = (
            'done\n\n<!-- xmc-telemetry: {"git_username": "unknown", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "address_pr_comment",'
            ' "skill_commit_id": "mock_skill_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    @patch("reply_pr_comment.datetime")
    @patch("reply_pr_comment.subprocess.check_output")
    def test_reply_comment_git_empty_hash(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test reply_comment when git log returns empty hash."""
        mock_git.return_value = ""

        mock_now = MagicMock()
        mock_now.strftime.side_effect = lambda fmt: {
            "%Y-%m-%d": "2026-06-22",
            "%H:%M:%S UTC": "12:00:00 UTC",
        }[fmt]
        mock_datetime.now.return_value = mock_now

        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }

        mock_call.side_effect = [
            {"login": "test_user"},  # response for GET /user
            {"id": 5678, "body": "done"},  # response for POST comment
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)

        expected_body = (
            'done\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "address_pr_comment",'
            ' "skill_commit_id": "unknown"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    @patch("reply_pr_comment.datetime")
    @patch("reply_pr_comment.subprocess.check_output")
    def test_reply_comment_git_fails(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test reply_comment when git command raises exception."""
        mock_git.side_effect = Exception("git failed")

        mock_now = MagicMock()
        mock_now.strftime.side_effect = lambda fmt: {
            "%Y-%m-%d": "2026-06-22",
            "%H:%M:%S UTC": "12:00:00 UTC",
        }[fmt]
        mock_datetime.now.return_value = mock_now

        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }

        mock_call.side_effect = [
            {"login": "test_user"},  # response for GET /user
            {"id": 5678, "body": "done"},  # response for POST comment
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)

        expected_body = (
            'done\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "address_pr_comment",'
            ' "skill_commit_id": "unknown"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    def test_reply_comment_auth_error(self, mock_call, mock_parse):
        """Test reply_comment handles GithubAuthError."""
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }
        mock_call.side_effect = GithubAuthError("auth error")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)

        self.assertIn("auth error", ctx.exception.message)
        self.assertIn(
            "Please authenticate using the 'gh' CLI", ctx.exception.instruction
        )

    @patch("reply_pr_comment.parse_comment_url")
    @patch("reply_pr_comment.call_github_api")
    def test_reply_comment_api_error(self, mock_call, mock_parse):
        """Test reply_comment handles GithubAPIError."""
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }
        mock_call.side_effect = GithubAPIError("api error")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)

        self.assertIn("api error", ctx.exception.message)
        self.assertIn(
            "Ensure the repository and comment ID exist",
            ctx.exception.instruction,
        )


if __name__ == "__main__":
    unittest.main()
