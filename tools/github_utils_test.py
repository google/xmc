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
from unittest.mock import MagicMock, patch, mock_open
import json
import subprocess
import urllib.request
import io

from github_utils import parse_comment_url, call_github_api, GithubAuthError, GithubAPIError

class TestGithubUtils(unittest.TestCase):

    def test_parse_comment_url_valid(self):
        url = "https://github.com/google/xmc/pull/29#discussion_r3421091224"
        res = parse_comment_url(url)
        self.assertIsNotNone(res)
        self.assertEqual(res["type"], "review_comment")
        self.assertEqual(res["owner"], "google")
        self.assertEqual(res["repo"], "xmc")
        self.assertEqual(res["pull_number"], "29")
        self.assertEqual(res["comment_id"], 3421091224)

    def test_parse_comment_url_invalid(self):
        url = "https://github.com/google/xmc/pull/29"
        res = parse_comment_url(url)
        self.assertIsNone(res)

    @patch('shutil.which')
    @patch('subprocess.run')
    def test_call_github_api_gh_cli_success(self, mock_run, mock_which):
        mock_which.return_value = "/usr/bin/gh"
        
        mock_proc = MagicMock()
        mock_proc.stdout = '{"id": 1234, "body": "test comment"}'
        mock_run.return_value = mock_proc

        res = call_github_api("/repos/google/xmc/pulls/comments/1234")
        self.assertEqual(res["id"], 1234)
        self.assertEqual(res["body"], "test comment")
        
        mock_run.assert_called_once()
        self.assertIn("gh", mock_run.call_args[0][0])

    @patch('shutil.which')
    @patch('subprocess.run')
    @patch('os.environ.get')
    @patch('urllib.request.urlopen')
    def test_call_github_api_gh_cli_fails_fallback_to_token_success(self, mock_urlopen, mock_env_get, mock_run, mock_which):
        # gh CLI is installed but fails
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(1, "gh")
        
        # GITHUB_TOKEN is available
        mock_env_get.side_effect = lambda key, default=None: "mock_token" if key == "GITHUB_TOKEN" else default

        # Mock urllib response
        mock_response = MagicMock()
        mock_response.read.return_value = b'{"id": 5678, "body": "fallback comment"}'
        mock_urlopen.return_value.__enter__.return_value = mock_response

        res = call_github_api("/repos/google/xmc/pulls/comments/5678")
        self.assertEqual(res["id"], 5678)
        self.assertEqual(res["body"], "fallback comment")

        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.headers.get("Authorization"), "token mock_token")

    @patch('shutil.which')
    @patch('os.environ.get')
    @patch('urllib.request.urlopen')
    def test_call_github_api_token_fails_fallback_to_unauth_get_success(self, mock_urlopen, mock_env_get, mock_which):
        mock_which.return_value = None # gh CLI not installed
        mock_env_get.return_value = None # No GITHUB_TOKEN

        mock_response = MagicMock()
        mock_response.read.return_value = b'{"public_data": true}'
        mock_urlopen.return_value.__enter__.return_value = mock_response

        res = call_github_api("/repos/google/xmc/pulls/comments/999", method="GET")
        self.assertEqual(res["public_data"], True)
        
        req = mock_urlopen.call_args[0][0]
        self.assertNotIn("Authorization", req.headers)

    @patch('shutil.which')
    @patch('os.environ.get')
    def test_call_github_api_post_without_auth_raises_auth_error(self, mock_env_get, mock_which):
        mock_which.return_value = None
        mock_env_get.return_value = None

        with self.assertRaises(GithubAuthError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments", method="POST", payload={"body": "hello"})
        
        self.assertIn("GitHub authentication failed", str(ctx.exception))
        self.assertIn("Unauthenticated requests not supported for non-GET methods", str(ctx.exception))

    @patch('shutil.which')
    @patch('os.environ.get')
    @patch('urllib.request.urlopen')
    def test_call_github_api_unauth_get_fails_raises_api_error(self, mock_urlopen, mock_env_get, mock_which):
        mock_which.return_value = None
        mock_env_get.return_value = None
        
        mock_urlopen.side_effect = Exception("HTTP 404 Not Found")

        with self.assertRaises(GithubAPIError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")
        
        self.assertIn("GitHub API call failed", str(ctx.exception))
        self.assertIn("[unauthenticated] Exception: HTTP 404 Not Found", str(ctx.exception))

    @patch('shutil.which')
    @patch('subprocess.run')
    @patch('os.environ.get')
    @patch('urllib.request.urlopen')
    def test_call_github_api_all_credentials_fail_raises_auth_error(self, mock_urlopen, mock_env_get, mock_run, mock_which):
        # gh CLI is installed but fails with auth error
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["gh", "api"], stderr="Error: To authenticate, please run: gh auth login"
        )
        
        # GITHUB_TOKEN is available but fails with 401 Unauthorized
        mock_env_get.side_effect = lambda key, default=None: "bad_token" if key == "GITHUB_TOKEN" else default
        mock_urlopen.side_effect = Exception("HTTP Error 401: Unauthorized")

        with self.assertRaises(GithubAuthError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")
            
        self.assertIn("GitHub authentication failed", str(ctx.exception))
        self.assertIn("[gh_cli] CalledProcessError: Command '['gh', 'api']' returned non-zero exit status 1. (stderr: Error: To authenticate, please run: gh auth login)", str(ctx.exception))
        self.assertIn("[token_auth] Exception: HTTP Error 401: Unauthorized", str(ctx.exception))

    @patch('shutil.which')
    @patch('subprocess.run')
    @patch('os.environ.get')
    @patch('urllib.request.urlopen')
    def test_call_github_api_all_credentials_fail_raises_api_error_for_non_auth_failure(self, mock_urlopen, mock_env_get, mock_run, mock_which):
        # gh CLI is installed but fails with 404 (not auth related)
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["gh", "api"], stderr="gh: Base URI not found (HTTP 404)"
        )
        
        # GITHUB_TOKEN is available but fails with 404
        mock_env_get.side_effect = lambda key, default=None: "mock_token" if key == "GITHUB_TOKEN" else default
        mock_urlopen.side_effect = Exception("HTTP Error 404: Not Found")

        with self.assertRaises(GithubAPIError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")
            
        self.assertIn("GitHub API call failed", str(ctx.exception))
        self.assertIn("[gh_cli] CalledProcessError: Command '['gh', 'api']' returned non-zero exit status 1. (stderr: gh: Base URI not found (HTTP 404))", str(ctx.exception))
        self.assertIn("[token_auth] Exception: HTTP Error 404: Not Found", str(ctx.exception))

if __name__ == '__main__':
    unittest.main()
