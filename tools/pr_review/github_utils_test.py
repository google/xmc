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
from typing import Any
from unittest.mock import MagicMock, patch

from tools.pr_review.github_utils import (
    GithubAPIError,
    GithubAuthError,
    call_github_api,
    get_authenticated_login,
    list_all_pages,
    list_review_threads,
    parse_comment_url,
    resolve_review_thread,
)


class TestListAllPages(unittest.TestCase):
    """Tests for list_all_pages REST pagination."""

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_follows_full_pages_until_short_page(self, mock_api):
        mock_api.side_effect = [list(range(100)), [100, 101]]
        self.assertEqual(list_all_pages("/repos/o/r/pulls/1/comments"), list(range(102)))
        self.assertEqual(
            [c.args[0] for c in mock_api.call_args_list],
            [
                "/repos/o/r/pulls/1/comments?page=1&per_page=100",
                "/repos/o/r/pulls/1/comments?page=2&per_page=100",
            ],
        )

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_existing_query_string_and_user_agent(self, mock_api):
        mock_api.return_value = [1]
        self.assertEqual(list_all_pages("/x?state=all", user_agent="ua"), [1])
        mock_api.assert_called_once_with("/x?state=all&page=1&per_page=100", user_agent="ua")

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_stops_on_empty_or_non_list_page(self, mock_api):
        mock_api.side_effect = [list(range(100)), {"message": "unexpected"}]
        self.assertEqual(list_all_pages("/x"), list(range(100)))
        mock_api.side_effect = [[]]
        self.assertEqual(list_all_pages("/x"), [])


def _threads_page(nodes, has_next=False, end_cursor=None):
    """Builds a GraphQL reviewThreads response page."""
    return {
        "data": {
            "repository": {
                "pullRequest": {
                    "reviewThreads": {
                        "pageInfo": {"hasNextPage": has_next, "endCursor": end_cursor},
                        "nodes": nodes,
                    }
                }
            }
        }
    }


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
    @patch("tools.pr_review.github_utils.run_command")
    def test_call_github_api_gh_cli_success(self, mock_run, mock_which):
        """Test calling GitHub API using gh CLI."""
        mock_which.return_value = "/usr/bin/gh"

        mock_proc = MagicMock()
        mock_proc.stdout = '{"id": 1234, "body": "test comment"}'
        mock_run.return_value = mock_proc

        res = call_github_api("/repos/google/xmc/pulls/comments/1234")
        self.assertEqual(res["id"], 1234)
        self.assertEqual(res["body"], "test comment")

        mock_run.assert_called_once_with(
            ["gh", "api", "-X", "GET", "/repos/google/xmc/pulls/comments/1234"],
            capture_output=True,
            log_cmd=False,
            log_on_error=False,
            input_text=None,
        )

    @patch("shutil.which")
    @patch("tools.pr_review.github_utils.run_command")
    def test_call_github_api_gh_cli_sends_payload_on_stdin(self, mock_run, mock_which):
        """A payload is passed to gh as JSON on stdin via --input -."""
        mock_which.return_value = "/usr/bin/gh"
        mock_run.return_value = MagicMock(stdout="{}")

        call_github_api("/repos/o/r/issues/1/comments", method="POST", payload={"body": "hi"})

        self.assertEqual(mock_run.call_args.args[0][-2:], ["--input", "-"])
        self.assertEqual(mock_run.call_args.kwargs["input_text"], '{"body": "hi"}')

    @patch("shutil.which")
    @patch("tools.pr_review.github_utils.run_command")
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
    @patch("tools.pr_review.github_utils.run_command")
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
    @patch("tools.pr_review.github_utils.run_command")
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

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_with_node_id_success(self, mock_api):
        """Test resolving review thread with direct GraphQL node ID."""
        mock_api.return_value = {
            "data": {"resolveReviewThread": {"thread": {"id": "PRRT_12345", "isResolved": True}}}
        }
        res = resolve_review_thread("google", "xmc", 10, "PRRT_12345")
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["thread_id"], "PRRT_12345")
        self.assertTrue(res["is_resolved"])

    @patch("tools.pr_review.github_utils.call_github_api")
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
        res = resolve_review_thread("google", "xmc", 490, 3820938617)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["thread_id"], "PRRT_node_999")
        self.assertTrue(res["is_resolved"])

    @patch("tools.pr_review.github_utils.call_github_api")
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
        with self.assertRaises(GithubAPIError):
            resolve_review_thread("google", "xmc", 490, 99999)

    def test_resolve_review_thread_invalid_target_format_raises(self):
        """Test error raised when target string is not a valid node ID or integer."""
        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, "not-a-valid-id")
        self.assertIn("Invalid thread or comment ID format", str(ctx.exception))

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_query_error_raises(self, mock_api):
        """Test error raised when GraphQL query returns errors."""
        mock_api.return_value = {"errors": [{"message": "Could not resolve to a Repository"}]}
        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, 3820938617)
        self.assertIn("GraphQL query failed", str(ctx.exception))

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_mutation_error_raises(self, mock_api):
        """Test error raised when GraphQL mutation returns errors."""
        mock_api.return_value = {
            "errors": [
                {"message": "Could not resolve to a node with the global id of 'PRRT_node_999'"}
            ]
        }
        with self.assertRaises(GithubAPIError) as ctx:
            resolve_review_thread("google", "xmc", 490, "PRRT_node_999")
        self.assertIn("GraphQL mutation failed", str(ctx.exception))

    @patch("tools.pr_review.github_utils.call_github_api", return_value={"login": "me"})
    def test_get_authenticated_login_success(self, mock_api):
        """Test get_authenticated_login returns the /user login."""
        self.assertEqual(get_authenticated_login(user_agent="UA"), "me")
        mock_api.assert_called_once_with("/user", user_agent="UA")

    @patch(
        "tools.pr_review.github_utils.call_github_api",
        side_effect=GithubAuthError("no auth"),
    )
    def test_get_authenticated_login_auth_error_propagates(self, _):
        """Test get_authenticated_login propagates GithubAuthError."""
        with self.assertRaises(GithubAuthError):
            get_authenticated_login(user_agent="UA")

    @patch(
        "tools.pr_review.github_utils.call_github_api",
        side_effect=GithubAPIError("api down"),
    )
    def test_get_authenticated_login_api_error_propagates(self, _):
        """Test get_authenticated_login propagates GithubAPIError."""
        with self.assertRaises(GithubAPIError):
            get_authenticated_login(user_agent="UA")

    def test_get_authenticated_login_missing_or_invalid_login_raises(self):
        """Test get_authenticated_login raises GithubAPIError without a string login."""
        responses: list[Any] = [{}, {"login": ""}, {"login": 123}, None, []]
        for response in responses:
            with (
                self.subTest(response=response),
                patch("tools.pr_review.github_utils.call_github_api", return_value=response),
                self.assertRaises(GithubAPIError) as ctx,
            ):
                get_authenticated_login(user_agent="UA")
            self.assertIn("no login", str(ctx.exception))

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_list_review_threads_follows_pagination(self, mock_api):
        """Test list_review_threads concatenates every page using endCursor."""
        mock_api.side_effect = [
            _threads_page([{"id": "PRRT_1"}], has_next=True, end_cursor="CUR_1"),
            _threads_page([{"id": "PRRT_2"}]),
        ]
        threads = list_review_threads("google", "xmc", 7)
        self.assertEqual([t["id"] for t in threads], ["PRRT_1", "PRRT_2"])
        cursors = [c.kwargs["payload"]["variables"]["cursor"] for c in mock_api.call_args_list]
        self.assertEqual(cursors, [None, "CUR_1"])

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_list_review_threads_drops_null_thread_nodes(self, mock_api):
        """Test list_review_threads filters out null thread nodes."""
        mock_api.return_value = _threads_page([None, {"id": "PRRT_1"}, None])
        self.assertEqual(list_review_threads("google", "xmc", 7), [{"id": "PRRT_1"}])

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_skips_null_comment_nodes(self, mock_api):
        """Test comment ID lookup tolerates null comment nodes inside a thread."""
        mock_api.side_effect = [
            _threads_page([{"id": "PRRT_2", "comments": {"nodes": [None, {"databaseId": 2}]}}]),
            {"data": {"resolveReviewThread": {"thread": {"id": "PRRT_2", "isResolved": True}}}},
        ]
        res = resolve_review_thread("google", "xmc", 7, 2)
        self.assertEqual(res["thread_id"], "PRRT_2")

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_list_review_threads_stops_when_end_cursor_missing(self, mock_api):
        """Test pagination stops instead of re-fetching page 1 when endCursor is missing."""
        mock_api.return_value = _threads_page([{"id": "PRRT_1"}], has_next=True, end_cursor=None)
        threads = list_review_threads("google", "xmc", 7)
        self.assertEqual([t["id"] for t in threads], ["PRRT_1"])
        self.assertEqual(mock_api.call_count, 1)

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_skips_threads_with_null_comments(self, mock_api):
        """Test comment ID lookup tolerates GraphQL `comments: null` thread nodes."""
        mock_api.side_effect = [
            _threads_page(
                [
                    {"id": "PRRT_null", "comments": None},
                    {"id": "PRRT_2", "comments": {"nodes": [{"databaseId": 2}]}},
                ]
            ),
            {"data": {"resolveReviewThread": {"thread": {"id": "PRRT_2", "isResolved": True}}}},
        ]
        res = resolve_review_thread("google", "xmc", 7, 2)
        self.assertEqual(res["thread_id"], "PRRT_2")

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_list_review_threads_query_error_raises(self, mock_api):
        """Test list_review_threads raises on GraphQL errors."""
        mock_api.return_value = {"errors": [{"message": "boom"}]}
        with self.assertRaises(GithubAPIError) as ctx:
            list_review_threads("google", "xmc", 7)
        self.assertIn("GraphQL query failed", str(ctx.exception))

    @patch("tools.pr_review.github_utils.call_github_api")
    def test_resolve_review_thread_comment_on_second_page(self, mock_api):
        """Test comment ID lookup finds threads beyond the first page."""
        mock_api.side_effect = [
            _threads_page(
                [{"id": "PRRT_1", "comments": {"nodes": [{"databaseId": 1}]}}],
                has_next=True,
                end_cursor="CUR_1",
            ),
            _threads_page([{"id": "PRRT_2", "comments": {"nodes": [{"databaseId": 2}]}}]),
            {"data": {"resolveReviewThread": {"thread": {"id": "PRRT_2", "isResolved": True}}}},
        ]
        res = resolve_review_thread("google", "xmc", 7, 2)
        self.assertEqual(res["thread_id"], "PRRT_2")


if __name__ == "__main__":
    unittest.main()
