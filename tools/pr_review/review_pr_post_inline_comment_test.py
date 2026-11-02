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

"""Unit tests for review_pr_post_inline_comment tool."""

import tempfile
import unittest
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAuthError
from tools.pr_review.review_pr_post_inline_comment import get_git_username, post_inline_comment

_MODULE = "tools.pr_review.review_pr_post_inline_comment"


class TestGetGitUsername(unittest.TestCase):
    """Test suite for get_git_username."""

    @patch(f"{_MODULE}.get_authenticated_login", return_value="reviewer1")
    def test_returns_authenticated_login(self, mock_login):
        self.assertEqual(get_git_username(), "reviewer1")
        mock_login.assert_called_once_with(user_agent="XMC-PR-Review-Agent")

    @patch.dict("os.environ", {"USER": "local_user"})
    @patch(f"{_MODULE}.get_authenticated_login", side_effect=GithubAuthError("no auth"))
    def test_falls_back_to_user_env_on_failure(self, _):
        self.assertEqual(get_git_username(), "local_user")


class TestReviewPRPostInlineComment(unittest.TestCase):
    """Test suite for review_pr_post_inline_comment tool."""

    def setUp(self):
        login_patch = patch(f"{_MODULE}.get_authenticated_login", return_value="reviewer1")
        login_patch.start()
        self.addCleanup(login_patch.stop)

    @patch("tools.pr_review.review_pr_post_inline_comment.call_github_api")
    def test_post_inline_comment_success_new_comment(self, mock_api):
        mock_api.side_effect = [
            {"id": 999, "html_url": "https://github.com/...", "line": 42},
        ]
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 15
        args.commit_id = "abc1234sha"
        args.path = "pkg/auth/handler.go"
        args.line = 42
        args.side = "RIGHT"
        args.in_reply_to = None
        args.message = "Please avoid nested else block."
        args.message_file = None

        res = post_inline_comment(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["comment_id"], 999)

    @patch("tools.pr_review.review_pr_post_inline_comment.call_github_api")
    def test_post_inline_comment_success_reply(self, mock_api):
        mock_api.side_effect = [
            {"id": 1000, "html_url": "https://github.com/...", "line": 42},
        ]
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 15
        args.commit_id = None
        args.path = None
        args.line = None
        args.in_reply_to = 101
        args.message = "Verified that fix is applied correctly."
        args.message_file = None

        res = post_inline_comment(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["comment_id"], 1000)

    def test_post_inline_comment_both_message_and_file_raises(self):
        args = MagicMock()
        args.message = "hello"
        args.message_file = "/tmp/msg.md"

        with self.assertRaises(HandledError):
            post_inline_comment(args)

    def test_post_inline_comment_missing_new_comment_fields_raises(self):
        args = MagicMock()
        args.message = "hello"
        args.message_file = None
        args.in_reply_to = None
        args.commit_id = None  # Missing!
        args.path = "pkg/a.go"
        args.line = 10

        with self.assertRaises(HandledError):
            post_inline_comment(args)

    @patch("tools.pr_review.review_pr_post_inline_comment.call_github_api")
    def test_post_inline_comment_from_file_success(self, mock_api):
        mock_api.side_effect = [
            {"id": 1001, "html_url": "https://github.com/...", "line": 10},
        ]
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write("Long comment content from file with markdown blocks.\n")
            f_path = f.name

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 15
        args.commit_id = "abc"
        args.path = "pkg/a.go"
        args.line = 10
        args.side = "RIGHT"
        args.in_reply_to = None
        args.message = None
        args.message_file = f_path

        res = post_inline_comment(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["comment_id"], 1001)

    @patch("tools.pr_review.review_pr_post_inline_comment.call_github_api")
    def test_post_inline_comment_long_with_backticks_and_codeblocks(self, mock_api):
        """Verify long comments (>10 KB) with inline backticks and multi-line fenced code blocks are preserved verbatim."""
        complex_markdown = (
            "# Deep Architectural Review Comment\n\n"
            "Please refactor `handleRequest()` in `pkg/auth/oauth2/handler.go` line 140.\n\n"
            "## Current Issue\n"
            "Using nested `if` blocks creates linear flow violations (`core_engineering.md#2`):\n"
            "```go\n"
            "func handleRequest(ctx context.Context, req *AuthRequest) (*AuthResponse, error) {\n"
            "\tif req != nil {\n"
            '\t\tif req.Token != "" {\n'
            "\t\t\t// ... 200 lines of logic ...\n"
            "\t\t}\n"
            "\t}\n"
            "}\n"
            "```\n\n"
            "## Suggested Diff Replacement\n"
            "```diff\n"
            "-if req != nil {\n"
            "+if req == nil {\n"
            '+    return nil, fmt.Errorf("request cannot be nil: %w", ErrInvalidArg)\n'
            "+}\n"
            "```\n\n" + ("Repeating detailed explanation line...\n" * 150)
        )

        mock_api.side_effect = [
            {
                "id": 2048,
                "html_url": "https://github.com/google/xmc/pull/15#discussion_r2048",
                "line": 140,
            },
        ]

        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write(complex_markdown)
            f_path = f.name

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 15
        args.commit_id = "sha_abc123"
        args.path = "pkg/auth/oauth2/handler.go"
        args.line = 140
        args.side = "RIGHT"
        args.in_reply_to = None
        args.message = None
        args.message_file = f_path

        res = post_inline_comment(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["comment_id"], 2048)

        # Verify that exact backticks and code blocks were passed to GitHub POST payload
        post_call = mock_api.call_args_list[0]
        posted_payload = post_call[1]["payload"]
        self.assertIn("```go\nfunc handleRequest", posted_payload["body"])
        self.assertIn("`handleRequest()`", posted_payload["body"])
        self.assertIn("```diff\n-if req != nil", posted_payload["body"])

    @patch("tools.pr_review.review_pr_post_inline_comment.resolve_review_thread")
    def test_post_inline_comment_resolve_thread_success(self, mock_resolve):
        mock_resolve.return_value = {
            "status": "success",
            "thread_id": "PRRT_12345",
            "is_resolved": True,
        }
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 490
        args.resolve_thread = "PRRT_12345"

        res = post_inline_comment(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["resolved_thread_id"], "PRRT_12345")
        self.assertTrue(res["is_resolved"])
        mock_resolve.assert_called_once_with(
            "google", "xmc", 490, "PRRT_12345", user_agent="XMC-PR-Review-Agent"
        )


if __name__ == "__main__":
    unittest.main()
