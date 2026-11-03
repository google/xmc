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

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError
from tools.pr_review.reply_pr_comment import reply_comment


class TestReplyPRComment(unittest.TestCase):
    """Test suite for reply_pr_comment tool."""

    def setUp(self):
        login_patch = patch(
            "tools.pr_review.reply_pr_comment.get_authenticated_login",
            return_value="test_user",
        )
        self.mock_login = login_patch.start()
        self.addCleanup(login_patch.stop)

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
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

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
    def test_reply_comment_success(self, mock_git, mock_datetime, mock_call, mock_parse):
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
            {"id": 5678, "body": "done"},  # response for POST comment
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)

        self.mock_login.assert_called_once_with(user_agent="XMC-Comment-Replier-Agent")

        expected_body = (
            'done\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "mock_skill_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
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

        self.mock_login.side_effect = GithubAPIError("GitHub API down")
        mock_call.side_effect = [
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
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "mock_skill_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
    def test_reply_comment_git_empty_hash(self, mock_git, mock_datetime, mock_call, mock_parse):
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
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "unknown"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
    def test_reply_comment_git_fails(self, mock_git, mock_datetime, mock_call, mock_parse):
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
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "unknown"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
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
        self.assertIn("Please authenticate using the 'gh' CLI", ctx.exception.instruction)

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
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

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
    def test_reply_comment_with_message_file_success(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test reply_comment successfully reads message from message_file."""
        mock_git.return_value = "mock_commit_id"
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
            {"id": 5678, "body": "file content"},
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = None
        args.message_file = "msg.txt"

        with patch("builtins.open", unittest.mock.mock_open(read_data="message from file")):
            res = reply_comment(args)

        self.assertEqual(res["id"], 5678)
        expected_body = (
            'message from file\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "mock_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    def test_reply_comment_with_message_file_read_error(self, mock_parse):
        """Test reply_comment raises HandledError when message_file cannot be read."""
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234,
        }

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message_file = "non_existent.txt"

        with patch("builtins.open", side_effect=OSError("File not found")):
            with self.assertRaises(HandledError) as ctx:
                reply_comment(args)

        self.assertIn("Failed to read message file 'non_existent.txt'", ctx.exception.message)
        self.assertIn("Ensure the message file path exists", ctx.exception.instruction)

    def test_reply_comment_mutual_exclusion_error(self):
        """Test reply_comment raises HandledError when both message and message_file are supplied."""
        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "hello"
        args.message_file = "msg.txt"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)

        self.assertIn(
            "Cannot specify both a message string and a message file", ctx.exception.message
        )

    @patch("tools.pr_review.reply_pr_comment.parse_comment_url")
    @patch("tools.pr_review.reply_pr_comment.call_github_api")
    @patch("tools.pr_review.reply_pr_comment.datetime")
    @patch("tools.pr_review.reply_pr_comment.subprocess.check_output")
    def test_reply_comment_empty_string_message(
        self, mock_git, mock_datetime, mock_call, mock_parse
    ):
        """Test reply_comment preserves explicit empty string message."""
        mock_git.return_value = "mock_commit_id"
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
            {"id": 5678, "body": ""},
        ]

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = ""
        args.message_file = None

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)
        expected_body = (
            '\n\n<!-- xmc-telemetry: {"git_username": "test_user", "date":'
            ' "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29",'
            ' "comment_id": 1234, "skill_name": "xmc_address_pr_comment",'
            ' "skill_commit_id": "mock_commit_id"} -->'
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": expected_body, "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent",
        )


class TestReplyPRCommentResolve(unittest.TestCase):
    """Test suite for reply_pr_comment --resolve."""

    def setUp(self):
        parse_patch = patch(
            "tools.pr_review.reply_pr_comment.parse_comment_url",
            return_value={
                "type": "review_comment",
                "owner": "google",
                "repo": "xmc",
                "pull_number": "29",
                "comment_id": 1234,
            },
        )
        call_patch = patch(
            "tools.pr_review.reply_pr_comment.call_github_api",
            return_value={
                "id": 5678,
                "html_url": "https://github.com/google/xmc/pull/29#discussion_r5678",
                "in_reply_to_id": 1234,
                "diff_hunk": "@@ -1,3 +1,3 @@\n" + "-old\n+new\n" * 200,
                "body": "done",
                "user": {"login": "test_user"},
            },
        )
        login_patch = patch(
            "tools.pr_review.reply_pr_comment.get_authenticated_login",
            return_value="test_user",
        )
        git_patch = patch(
            "tools.pr_review.reply_pr_comment.subprocess.check_output",
            return_value="mock_commit_id",
        )
        for p in (parse_patch, call_patch, login_patch, git_patch):
            p.start()
            self.addCleanup(p.stop)
        self.args = MagicMock()
        self.args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        self.args.message = "done"
        self.args.message_file = None

    @patch("tools.pr_review.reply_pr_comment.resolve_review_thread")
    def test_resolve_flag_resolves_thread_after_reply(self, mock_resolve):
        """Test --resolve resolves the replied comment's thread via github_utils."""
        mock_resolve.return_value = {
            "status": "success",
            "thread_id": "PRRT_1",
            "is_resolved": True,
        }
        self.args.resolve = True

        res = reply_comment(self.args)

        mock_resolve.assert_called_once_with(
            "google", "xmc", 29, 1234, user_agent="XMC-Comment-Replier-Agent"
        )
        self.assertEqual(
            res,
            {
                "id": 5678,
                "html_url": "https://github.com/google/xmc/pull/29#discussion_r5678",
                "in_reply_to": 1234,
                "resolved": True,
            },
        )

    @patch("tools.pr_review.reply_pr_comment.resolve_review_thread")
    def test_without_resolve_flag_does_not_resolve(self, mock_resolve):
        """Test the thread is left untouched when --resolve is not set."""
        self.args.resolve = False

        res = reply_comment(self.args)

        mock_resolve.assert_not_called()
        self.assertFalse(res["resolved"])

    def test_output_omits_raw_response_fields(self):
        """Test bulky API response fields (diff_hunk, body, user) are not echoed."""
        self.args.resolve = False

        res = reply_comment(self.args)

        self.assertEqual(set(res), {"id", "html_url", "in_reply_to", "resolved"})
        self.assertEqual(res["in_reply_to"], 1234)

    @patch("tools.pr_review.reply_pr_comment.resolve_review_thread")
    def test_resolve_failure_reports_posted_reply(self, mock_resolve):
        """Test a resolve failure raises HandledError carrying the posted reply ID."""
        mock_resolve.side_effect = GithubAPIError("forbidden")
        self.args.resolve = True

        with self.assertRaises(HandledError) as ctx:
            reply_comment(self.args)

        self.assertIn("Reply posted but failed to resolve", ctx.exception.message)
        self.assertIn("Do NOT re-post the reply", ctx.exception.instruction)
        self.assertIn("--resolve-thread 1234", ctx.exception.instruction)
        self.assertEqual(ctx.exception.details, {"reply_id": 5678, "comment_id": 1234})


if __name__ == "__main__":
    unittest.main()
