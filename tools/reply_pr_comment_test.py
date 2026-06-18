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

import unittest
from unittest.mock import MagicMock, patch

from reply_pr_comment import reply_comment
from tool_utils import HandledError
from github_utils import GithubAuthError, GithubAPIError

class TestReplyPRComment(unittest.TestCase):

    @patch('reply_pr_comment.parse_comment_url')
    def test_reply_comment_invalid_url(self, mock_parse):
        mock_parse.return_value = None
        
        args = MagicMock()
        args.comment_url = "https://invalid.url"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)
        
        self.assertIn("Could not parse Pull Request review comment URL.", ctx.exception.message)

    @patch('reply_pr_comment.parse_comment_url')
    @patch('reply_pr_comment.call_github_api')
    def test_reply_comment_success(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        
        mock_call.return_value = {"id": 5678, "body": "done"}

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        res = reply_comment(args)
        self.assertEqual(res["id"], 5678)
        
        mock_call.assert_called_once_with(
            "/repos/google/xmc/pulls/29/comments",
            method="POST",
            payload={"body": "done", "in_reply_to": 1234},
            user_agent="XMC-Comment-Replier-Agent"
        )

    @patch('reply_pr_comment.parse_comment_url')
    @patch('reply_pr_comment.call_github_api')
    def test_reply_comment_auth_error(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        mock_call.side_effect = GithubAuthError("auth error")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)
        
        self.assertIn("auth error", ctx.exception.message)
        self.assertIn("Please authenticate using the 'gh' CLI", ctx.exception.instruction)

    @patch('reply_pr_comment.parse_comment_url')
    @patch('reply_pr_comment.call_github_api')
    def test_reply_comment_api_error(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        mock_call.side_effect = GithubAPIError("api error")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"
        args.message = "done"

        with self.assertRaises(HandledError) as ctx:
            reply_comment(args)
        
        self.assertIn("api error", ctx.exception.message)
        self.assertIn("Ensure the repository and comment ID exist", ctx.exception.instruction)

if __name__ == '__main__':
    unittest.main()
