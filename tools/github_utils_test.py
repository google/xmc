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

"""Unit tests for github_utils module."""

import subprocess
import unittest
from unittest.mock import MagicMock, patch

from github_utils import (
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    parse_comment_url,
)


class TestGithubUtils(unittest.TestCase):
    """Test suite for github_utils helper functions."""

    def test_parse_comment_url_valid(self):
        """Test parsing valid review comment URL."""
        url = "https://github.com/google/xmc/pull/29#discussion_r3421091224"
        res = parse_comment_url(url)
        self.assertIsNotNone(res)
        self.assertEqual(res["type"], "review_comment")
        self.assertEqual(res["owner"], "google")
        self.assertEqual(res["repo"], "xmc")
        self.assertEqual(res["pull_number"], "29")
        self.assertEqual(res["comment_id"], 3421091224)

    def test_parse_comment_url_invalid(self):
        """Test parsing invalid comment URL returns None."""
        url = "https://github.com/google/xmc/pull/29"
        res = parse_comment_url(url)
        self.assertIsNone(res)

    @patch("shutil.which")
    @patch("subprocess.run")
    def test_call_github_api_gh_cli_success(self, mock_run, mock_which):
        """Test calling GitHub API using gh CLI."""
        mock_which.return_value = "/usr/bin/gh"

        mock_proc = MagicMock()
        mock_proc.stdout = '{"id": 1234, "body": "test comment"}'
        mock_run.return_value = mock_proc

        res = call_github_api("/repos/google/xmc/pulls/comments/1234")
        self.assertEqual(res["id"], 1234)
        self.assertEqual(res["body"], "test comment")

        mock_run.assert_called_once()
        self.assertIn("gh", mock_run.call_args[0][0])

    @patch("shutil.which")
    @patch("subprocess.run")
    @patch("os.environ.get")
    @patch("urllib.request.urlopen")
    def test_call_github_api_gh_cli_fails_fallback_to_token_success(
        self, mock_urlopen, mock_env_get, mock_run, mock_which
    ):
        """Test fallback to GITHUB_TOKEN when gh CLI fails."""
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(1, "gh")

        mock_env_get.side_effect = lambda key, default=None: (
            "mock_token" if key == "GITHUB_TOKEN" else default
        )

        mock_response = MagicMock()
        mock_response.read.return_value = b'{"id": 5678, "body": "fallback comment"}'
        mock_urlopen.return_value.__enter__.return_value = mock_response

        res = call_github_api("/repos/google/xmc/pulls/comments/5678")
        self.assertEqual(res["id"], 5678)
        self.assertEqual(res["body"], "fallback comment")

        mock_urlopen.assert_called_once()
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.headers.get("Authorization"), "token mock_token")

    @patch("shutil.which")
    @patch("os.environ.get")
    @patch("urllib.request.urlopen")
    def test_call_github_api_token_fails_fallback_to_unauth_get_success(
        self, mock_urlopen, mock_env_get, mock_which
    ):
        """Test fallback to unauthenticated GET when no auth mechanisms exist."""
        mock_which.return_value = None  # gh CLI not installed
        mock_env_get.return_value = None  # No GITHUB_TOKEN

        mock_response = MagicMock()
        mock_response.read.return_value = b'{"public_data": true}'
        mock_urlopen.return_value.__enter__.return_value = mock_response

        res = call_github_api("/repos/google/xmc/pulls/comments/999", method="GET")
        self.assertEqual(res["public_data"], True)

        req = mock_urlopen.call_args[0][0]
        self.assertNotIn("Authorization", req.headers)

    @patch("shutil.which")
    @patch("os.environ.get")
    def test_call_github_api_post_without_auth_raises_auth_error(self, mock_env_get, mock_which):
        """Test POST request without auth raises GithubAuthError."""
        mock_which.return_value = None
        mock_env_get.return_value = None

        with self.assertRaises(GithubAuthError) as ctx:
            call_github_api(
                "/repos/google/xmc/pulls/comments",
                method="POST",
                payload={"body": "hello"},
            )

        self.assertIn("GitHub authentication failed", str(ctx.exception))
        self.assertIn(
            "Unauthenticated requests not supported for non-GET methods",
            str(ctx.exception),
        )

    @patch("shutil.which")
    @patch("os.environ.get")
    @patch("urllib.request.urlopen")
    def test_call_github_api_unauth_get_fails_raises_api_error(
        self, mock_urlopen, mock_env_get, mock_which
    ):
        """Test API error handling when unauthenticated GET fails."""
        mock_which.return_value = None
        mock_env_get.return_value = None

        mock_urlopen.side_effect = Exception("HTTP 404 Not Found")

        with self.assertRaises(GithubAPIError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")

        self.assertIn("GitHub API call failed", str(ctx.exception))
        self.assertIn("[unauthenticated] Exception: HTTP 404 Not Found", str(ctx.exception))

    @patch("shutil.which")
    @patch("subprocess.run")
    @patch("os.environ.get")
    @patch("urllib.request.urlopen")
    def test_call_github_api_all_credentials_fail_raises_auth_error(
        self, mock_urlopen, mock_env_get, mock_run, mock_which
    ):
        """Test auth failure error raised when all auth methods fail."""
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(
            1,
            ["gh", "api"],
            stderr="Error: To authenticate, please run: gh auth login",
        )

        mock_env_get.side_effect = lambda key, default=None: (
            "bad_token" if key == "GITHUB_TOKEN" else default
        )
        mock_urlopen.side_effect = Exception("HTTP Error 401: Unauthorized")

        with self.assertRaises(GithubAuthError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")

        self.assertIn("GitHub authentication failed", str(ctx.exception))
        self.assertIn("To authenticate, please run: gh auth login", str(ctx.exception))
        self.assertIn(
            "[token_auth] Exception: HTTP Error 401: Unauthorized",
            str(ctx.exception),
        )

    @patch("shutil.which")
    @patch("subprocess.run")
    @patch("os.environ.get")
    @patch("urllib.request.urlopen")
    def test_call_github_api_all_credentials_fail_raises_api_error_for_non_auth_failure(
        self, mock_urlopen, mock_env_get, mock_run, mock_which
    ):
        """Test API error raised when non-auth failure occurs across methods."""
        mock_which.return_value = "/usr/bin/gh"
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["gh", "api"], stderr="gh: Base URI not found (HTTP 404)"
        )

        mock_env_get.side_effect = lambda key, default=None: (
            "mock_token" if key == "GITHUB_TOKEN" else default
        )
        mock_urlopen.side_effect = Exception("HTTP Error 404: Not Found")

        with self.assertRaises(GithubAPIError) as ctx:
            call_github_api("/repos/google/xmc/pulls/comments/123", method="GET")

        self.assertIn("GitHub API call failed", str(ctx.exception))
        self.assertIn("gh: Base URI not found (HTTP 404)", str(ctx.exception))
        self.assertIn(
            "[token_auth] Exception: HTTP Error 404: Not Found",
            str(ctx.exception),
        )

    @patch("github_utils.call_github_api")
    def test_resolve_review_thread_with_node_id_success(self, mock_api):
        """Test resolving review thread with direct GraphQL node ID."""
        mock_api.return_value = {
            "data": {"resolveReviewThread": {"thread": {"id": "PRRT_12345", "isResolved": True}}}
        }
        from github_utils import resolve_review_thread

        res = resolve_review_thread("google", "xmc", 10, "PRRT_12345")
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["thread_id"], "PRRT_12345")
        self.assertTrue(res["is_resolved"])

    @patch("github_utils.call_github_api")
    def test_resolve_review_thread_with_comment_id_success(self, mock_api):
        """Test resolving review thread by numeric comment ID."""
        mock_api.side_effect = [
            # 1. Query threads
            {
                "data": {
                    "repository": {
                        "pullRequest": {
                            "reviewThreads": {
                                "nodes": [
                                    {
                                        "id": "PRRT_node_999",
                                        "isResolved": False,
                                        "comments": {"nodes": [{"databaseId": 3820938617}]},
                                    }
                                ]
                            }
                        }
                    }
                }
            },
            # 2. Mutation
            {
                "data": {
                    "resolveReviewThread": {"thread": {"id": "PRRT_node_999", "isResolved": True}}
                }
            },
        ]
        from github_utils import resolve_review_thread

        res = resolve_review_thread("google", "xmc", 490, 3820938617)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["thread_id"], "PRRT_node_999")
        self.assertTrue(res["is_resolved"])

    @patch("github_utils.call_github_api")
    def test_resolve_review_thread_comment_id_not_found_raises(self, mock_api):
        """Test error raised when numeric comment ID is not in review threads."""
        mock_api.return_value = {
            "data": {
                "repository": {
                    "pullRequest": {
                        "reviewThreads": {
                            "nodes": [
                                {
                                    "id": "PRRT_node_999",
                                    "isResolved": False,
                                    "comments": {"nodes": [{"databaseId": 11111}]},
                                }
                            ]
                        }
                    }
                }
            }
        }
        from github_utils import resolve_review_thread

        with self.assertRaises(GithubAPIError):
            resolve_review_thread("google", "xmc", 490, 99999)

    def test_resolve_review_thread_invalid_target_format_raises(self):
        """Test error raised when target string is not a valid node ID or integer."""
        from github_utils import resolve_review_thread

        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, "not-a-valid-id")
        self.assertIn("Invalid thread or comment ID format", str(ctx.exception))

    @patch("github_utils.call_github_api")
    def test_resolve_review_thread_query_error_raises(self, mock_api):
        """Test error raised when GraphQL query returns errors."""
        mock_api.return_value = {"errors": [{"message": "Could not resolve to a Repository"}]}
        from github_utils import resolve_review_thread

        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, 3820938617)
        self.assertIn("GraphQL query failed", str(ctx.exception))

    @patch("github_utils.call_github_api")
    def test_resolve_review_thread_mutation_error_raises(self, mock_api):
        """Test error raised when GraphQL mutation returns errors."""
        mock_api.return_value = {
            "errors": [
                {"message": "Could not resolve to a node with the global id of 'PRRT_node_999'"}
            ]
        }
        from github_utils import resolve_review_thread

        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, "PRRT_node_999")
        self.assertIn("GraphQL mutation failed", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
