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

"""Unit tests for generate_review_checklist tool."""

import unittest
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.generate_review_checklist import (
    _parse_graphql_nodes,
    _parse_rest_comment,
    extract_severity,
    fetch_unresolved_comments,
    format_checklist_markdown,
    generate_review_checklist,
)
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError

# GraphQL review threads go through the shared github_utils.list_review_threads;
# the REST fallback calls the name imported into generate_review_checklist.
_GRAPHQL_API = "tools.pr_review.github_utils.call_github_api"
_REST_API = "tools.pr_review.generate_review_checklist.call_github_api"


def _thread(
    comment_id: int,
    resolved: bool = False,
    path: str = "file.py",
    line: int = 1,
    body: str = "Comment",
) -> dict[str, Any]:
    """Builds a GraphQL review thread node with a single comment."""
    return {
        "id": f"PRRT_{comment_id}",
        "isResolved": resolved,
        "path": path,
        "line": line,
        "comments": {
            "nodes": [
                {
                    "databaseId": comment_id,
                    "body": body,
                    "url": f"https://github.com/google/xmc/pull/145#discussion_r{comment_id}",
                    "author": {"login": "dev1"},
                }
            ]
        },
    }


def _threads_page(
    nodes: list[dict[str, Any] | None],
    has_next: bool = False,
    cursor: str | None = None,
) -> dict[str, Any]:
    """Builds one GraphQL reviewThreads response page."""
    return {
        "data": {
            "repository": {
                "pullRequest": {
                    "reviewThreads": {
                        "pageInfo": {"hasNextPage": has_next, "endCursor": cursor},
                        "nodes": nodes,
                    }
                }
            }
        }
    }


class TestGenerateReviewChecklist(unittest.TestCase):
    """Test suite for generate_review_checklist tool functions."""

    def test_parse_graphql_nodes(self):
        """Test parsing GraphQL nodes filters resolved threads."""
        nodes = [
            {
                "isResolved": True,
                "path": "file1.py",
                "line": 10,
                "comments": {
                    "nodes": [
                        {
                            "id": "c1",
                            "databaseId": 1,
                            "body": "Resolved",
                            "author": {"login": "user1"},
                        }
                    ]
                },
            },
            {
                "isResolved": False,
                "path": "file2.py",
                "line": 20,
                "comments": {
                    "nodes": [
                        {
                            "id": "c2",
                            "databaseId": 2,
                            "body": "Unresolved comment",
                            "url": "https://github.com/google/xmc/pull/1#discussion_r2",
                            "author": {"login": "user2"},
                        }
                    ]
                },
            },
        ]
        items = _parse_graphql_nodes(nodes)
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["comment_id"], 2)
        self.assertEqual(items[0]["author"], "user2")
        self.assertEqual(items[0]["file_path"], "file2.py")
        self.assertEqual(items[0]["line"], 20)
        self.assertEqual(items[0]["body"], "Unresolved comment")

    def test_parse_graphql_nodes_null_author(self):
        """Test parsing GraphQL nodes handles null author and null threads safely."""
        nodes: list[dict[str, Any] | None] = [
            None,
            {
                "isResolved": False,
                "path": "file.py",
                "line": 5,
                "comments": {
                    "nodes": [
                        {
                            "id": "c3",
                            "databaseId": 3,
                            "body": "Deleted user comment",
                            "url": "https://github.com/google/xmc/pull/1#discussion_r3",
                            "author": None,
                        }
                    ]
                },
            },
        ]
        items = _parse_graphql_nodes(nodes)
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["author"], "unknown")

    def test_parse_rest_comment_null_user(self):
        """Test parsing REST API comment dictionary with null user."""
        comment_raw = {
            "id": 101,
            "user": None,
            "path": "tools/script.py",
            "line": 50,
            "body": "Comment by deleted user",
            "html_url": "https://github.com/google/xmc/pull/1#discussion_r101",
        }
        item = _parse_rest_comment(comment_raw)
        self.assertEqual(item["comment_id"], 101)
        self.assertEqual(item["author"], "unknown")

    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_graphql(self, mock_api):
        """Test fetch_unresolved_comments returns GraphQL results when available."""
        mock_api.return_value = _threads_page(
            [_thread(101, path="main.go", line=15, body="Fix error handling")]
        )
        items = fetch_unresolved_comments("google", "xmc", 145)
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["comment_id"], 101)
        self.assertEqual(items[0]["file_path"], "main.go")
        self.assertEqual(items[0]["author"], "dev1")

    @patch(_REST_API)
    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_graphql_multi_page(self, mock_api, mock_rest):
        """Test GraphQL pagination follows endCursor across pages."""
        mock_api.side_effect = [
            _threads_page([_thread(1)], has_next=True, cursor="c1"),
            _threads_page([_thread(2, resolved=True), _thread(3)]),
        ]
        items = fetch_unresolved_comments("google", "xmc", 145)
        self.assertEqual([i["comment_id"] for i in items], [1, 3])
        self.assertEqual(mock_api.call_count, 2)
        second_vars = mock_api.call_args_list[1].kwargs["payload"]["variables"]
        self.assertEqual(second_vars["cursor"], "c1")
        mock_rest.assert_not_called()

    @patch(_REST_API)
    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_null_end_cursor_stops(self, mock_api, mock_rest):
        """Regression: hasNextPage with a null endCursor must not paginate forever."""
        mock_api.return_value = _threads_page([_thread(1)], has_next=True, cursor=None)
        items = fetch_unresolved_comments("google", "xmc", 145)
        self.assertEqual([i["comment_id"] for i in items], [1])
        mock_api.assert_called_once()
        mock_rest.assert_not_called()

    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_null_nodes_tolerated(self, mock_api):
        """Test null thread nodes, null comment lists, and null comments are skipped."""
        null_comments = _thread(2)
        null_comments["comments"] = None
        null_first_comment = _thread(3)
        null_first_comment["comments"]["nodes"] = [None]
        mock_api.return_value = _threads_page([None, _thread(1), null_comments, null_first_comment])
        items = fetch_unresolved_comments("google", "xmc", 145)
        self.assertEqual([i["comment_id"] for i in items], [1])

    @patch(_REST_API)
    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_rest_fallback(self, mock_api, mock_rest):
        """Test fetch_unresolved_comments falls back to REST API if GraphQL fails."""
        mock_api.side_effect = GithubAPIError("GraphQL error")
        mock_rest.return_value = [
            {
                "id": 201,
                "user": {"login": "dev2"},
                "path": "pkg/auth.go",
                "line": 30,
                "body": "Simplify check",
                "html_url": "https://github.com/owner/repo/pull/145#discussion_r201",
            }
        ]
        items = fetch_unresolved_comments("google", "xmc", 145)
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["comment_id"], 201)

    @patch(_REST_API)
    @patch(_GRAPHQL_API)
    def test_fetch_unresolved_comments_auth_error(self, mock_api, mock_rest):
        """Test fetch_unresolved_comments handles GithubAuthError."""
        mock_api.side_effect = GithubAuthError("Authentication failed")
        mock_rest.side_effect = GithubAuthError("Authentication failed")
        with self.assertRaises(HandledError) as ctx:
            fetch_unresolved_comments("google", "xmc", 145)
        self.assertIn("Authentication failed", ctx.exception.message)

    def test_extract_severity(self):
        """Test extract_severity accurately classifies SVG badges and text tags."""
        self.assertEqual(
            extract_severity(
                "![critical](https://www.gstatic.com/codereviewagent/critical-priority.svg)"
            ),
            "CRITICAL",
        )
        self.assertEqual(
            extract_severity("![high](https://www.gstatic.com/codereviewagent/high-priority.svg)"),
            "HIGH",
        )
        self.assertEqual(
            extract_severity(
                "![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)"
            ),
            "MEDIUM",
        )
        self.assertEqual(
            extract_severity("![low](https://www.gstatic.com/codereviewagent/low-priority.svg)"),
            "LOW",
        )
        self.assertEqual(extract_severity("Random comment text"), None)

    def test_format_checklist_markdown(self):
        """Test formatting markdown checklist with comments."""
        comments = [
            {
                "comment_id": 1,
                "author": "reviewer1",
                "file_path": "cmd/main.go",
                "line": 42,
                "body": "Fix this line\nSecond line comment",
                "url": "https://github.com/google/xmc/pull/145#discussion_r1",
                "severity": "HIGH",
            }
        ]
        md = format_checklist_markdown("google", "xmc", 145, comments)
        self.assertIn("# PR Review Checklist: google/xmc#145", md)
        self.assertIn("Total Unresolved Comments: 1 (High/Critical: 1)", md)
        expected_item = (
            "- [ ] **[cmd/main.go:42](https://github.com/google/xmc/pull/145#discussion_r1)**"
            " [HIGH] (@reviewer1) (Comment ID 1)"
        )
        self.assertIn(expected_item, md)
        self.assertIn("  > Fix this line", md)
        self.assertIn("  > Second line comment", md)

    def test_format_checklist_markdown_empty(self):
        """Test formatting markdown checklist with no comments."""
        md = format_checklist_markdown("google", "xmc", 145, [])
        self.assertIn("Total Unresolved Comments: 0 (High/Critical: 0)", md)
        self.assertIn("No unresolved review comments found.", md)

    def test_generate_review_checklist_invalid_pr(self):
        """Test generate_review_checklist raises HandledError for invalid PR number."""
        args = MagicMock()
        args.pr_number = "invalid"

        with self.assertRaises(HandledError) as ctx:
            generate_review_checklist(args)
        self.assertIn("Invalid PR number: invalid", ctx.exception.message)

    @patch("tools.pr_review.generate_review_checklist.fetch_unresolved_comments")
    @patch.object(Path, "write_text")
    def test_generate_review_checklist_success(self, mock_write_text, mock_fetch):
        """Test successful execution of generate_review_checklist."""
        mock_fetch.return_value = [
            {
                "comment_id": 1,
                "author": "reviewer1",
                "file_path": "cmd/main.go",
                "line": 42,
                "body": "![high](https://www.gstatic.com/codereviewagent/high-priority.svg)\nFix this",
                "url": "https://github.com/google/xmc/pull/145#discussion_r1",
                "severity": "HIGH",
            }
        ]

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = "145"
        args.output = "pr_review_checklist.md"

        result = generate_review_checklist(args)
        self.assertEqual(result["owner"], "google")
        self.assertEqual(result["repo"], "xmc")
        self.assertEqual(result["pr_number"], 145)
        self.assertEqual(result["unresolved_comment_count"], 1)
        self.assertEqual(result["high_critical_count"], 1)
        self.assertTrue(result["needs_rereview"])

        mock_write_text.assert_called_once()

    @patch("tools.pr_review.generate_review_checklist.fetch_unresolved_comments")
    @patch.object(Path, "write_text", side_effect=OSError("Permission denied"))
    def test_generate_review_checklist_os_error(self, mock_write_text, mock_fetch):
        """Test generate_review_checklist handles OSError on file write."""
        mock_fetch.return_value = []
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = "145"
        args.output = "/read_only/pr_review_checklist.md"

        with self.assertRaises(HandledError) as ctx:
            generate_review_checklist(args)
        self.assertIn("Failed to write output file", ctx.exception.message)


if __name__ == "__main__":
    unittest.main()
