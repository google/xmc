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

from fetch_pr_comment import fetch_comment
from tool_utils import HandledError
from github_utils import GithubAuthError, GithubAPIError

class TestFetchPRComment(unittest.TestCase):

    @patch('fetch_pr_comment.parse_comment_url')
    def test_fetch_comment_invalid_url(self, mock_parse):
        mock_parse.return_value = None
        
        args = MagicMock()
        args.comment_url = "https://invalid.url"

        with self.assertRaises(HandledError) as ctx:
            fetch_comment(args)
        
        self.assertIn("Could not parse Pull Request review comment URL.", ctx.exception.message)

    @patch('fetch_pr_comment.parse_comment_url')
    @patch('fetch_pr_comment.call_github_api')
    def test_fetch_comment_success(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        
        mock_call.return_value = {
            "id": 1234,
            "user": {"login": "janzak-dev"},
            "body": "Fix this please",
            "path": "tools/build_oci.py",
            "line": 76,
            "diff_hunk": "@@ -55,30 +56,36 @@...",
            "commit_id": "abcdef123"
        }

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"

        res = fetch_comment(args)
        self.assertEqual(res["comment_id"], 1234)
        self.assertEqual(res["author"], "janzak-dev")
        self.assertEqual(res["body"], "Fix this please")
        self.assertEqual(res["file_path"], "tools/build_oci.py")
        self.assertEqual(res["line"], 76)
        self.assertEqual(res["commit_id"], "abcdef123")

    @patch('fetch_pr_comment.parse_comment_url')
    @patch('fetch_pr_comment.call_github_api')
    def test_fetch_comment_auth_error(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        mock_call.side_effect = GithubAuthError("auth failed")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"

        with self.assertRaises(HandledError) as ctx:
            fetch_comment(args)
        
        self.assertIn("auth failed", ctx.exception.message)
        self.assertIn("Please authenticate using the 'gh' CLI", ctx.exception.instruction)

    @patch('fetch_pr_comment.parse_comment_url')
    @patch('fetch_pr_comment.call_github_api')
    def test_fetch_comment_api_error(self, mock_call, mock_parse):
        mock_parse.return_value = {
            "type": "review_comment",
            "owner": "google",
            "repo": "xmc",
            "pull_number": "29",
            "comment_id": 1234
        }
        mock_call.side_effect = GithubAPIError("api failed")

        args = MagicMock()
        args.comment_url = "https://github.com/google/xmc/pull/29#discussion_r1234"

        with self.assertRaises(HandledError) as ctx:
            fetch_comment(args)
        
        self.assertIn("api failed", ctx.exception.message)
        self.assertIn("Ensure the repository and comment ID exist", ctx.exception.instruction)

if __name__ == '__main__':
    unittest.main()
