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

"""Unit tests for review_pr_generate_audit tool."""

import json
import os
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError
from tools.pr_review.review_pr_generate_audit import (
    build_audit_sidecar,
    build_parser,
    extract_task_references,
    format_initial_audit_markdown,
    format_rereview_audit_markdown,
    main,
    resolve_output_path,
)

_GRAPHQL_API = "tools.pr_review.github_utils.call_github_api"
_AUDIT = "tools.pr_review.review_pr_generate_audit"


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


def _thread(first_comment_id: int, resolved: bool) -> dict[str, Any]:
    """Builds a GraphQL review thread node keyed by its first comment ID."""
    return {
        "id": f"PRRT_{first_comment_id}",
        "isResolved": resolved,
        "comments": {"nodes": [{"databaseId": first_comment_id}]},
    }


def _comment(cid: int, path: str = "a.go", line: int = 1) -> dict[str, Any]:
    """Builds a REST review comment."""
    return {"id": cid, "path": path, "line": line, "user": {"login": "alice"}, "body": "Nit."}


class TestReviewPRGenerateAudit(unittest.TestCase):
    """Test suite for review_pr_generate_audit tool."""

    def setUp(self):
        # Review threads come from the shared github_utils pagination helper;
        # keep every test hermetic by stubbing its underlying API call.
        graphql_patch = patch(_GRAPHQL_API, return_value=_threads_page([]))
        self.mock_graphql = graphql_patch.start()
        self.addCleanup(graphql_patch.stop)

    def test_extract_task_references(self):
        desc = "Fixes task #123456 and bug #7890. Also references https://github.com/google/xmc/issues/42."
        refs = extract_task_references(desc)
        self.assertIn("task #123456", refs)
        self.assertIn("bug #7890", refs)
        self.assertIn("https://github.com/google/xmc/issues/42", refs)

    def test_extract_task_references_empty(self):
        self.assertEqual(extract_task_references(""), [])
        self.assertEqual(extract_task_references(None), [])

    def test_resolve_output_path_explicit(self):
        p = resolve_output_path("/tmp/custom.md", "", "default.md")
        self.assertEqual(p, Path("/tmp/custom.md"))

    def test_resolve_output_path_conv_id(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(os.environ, {"ARTIFACT_DIR": tmpdir}):
                p = resolve_output_path("", "test-conv-123", "audit.md")
                self.assertEqual(p, Path(tmpdir) / "audit.md")

        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(os.environ, {"JETSKI_ARTIFACT_DIR": tmpdir}):
                p = resolve_output_path("", "test-conv-123", "audit.md")
                self.assertEqual(p, Path(tmpdir) / "audit.md")

    def test_format_initial_audit_markdown(self):
        data = {
            "pr_info": {
                "title": "feat: add oauth handling",
                "body": "Fixes task #999. Implements new flow.",
                "head": {"sha": "abc1234", "ref": "feat/oauth"},
                "base": {"ref": "main"},
            },
            "files": [{"filename": "pkg/auth/handler.go", "additions": 20, "deletions": 5}],
            "comments": [
                {
                    "id": 101,
                    "path": "pkg/auth/handler.go",
                    "line": 15,
                    "user": {"login": "alice"},
                    "body": "Consider validating nil here.",
                }
            ],
        }
        md = format_initial_audit_markdown("google", "xmc", 42, data)
        self.assertIn("# PR Review Audit Checklist & Direction Plan", md)
        self.assertIn("task #999", md)
        self.assertIn("pkg/auth/handler.go", md)
        self.assertIn("Comment ID `101`", md)

    def test_format_rereview_audit_markdown(self):
        data = {
            "pr_info": {
                "title": "feat: add oauth handling",
                "head": {"sha": "abc1234"},
            },
            "comments": [
                {
                    "id": 101,
                    "path": "pkg/auth/handler.go",
                    "line": 15,
                    "user": {"login": "alice"},
                    "body": "Consider validating nil here.",
                },
                {
                    "id": 201,
                    "in_reply_to_id": 101,
                    "user": {"login": "bob"},
                    "body": "Done, added check.",
                },
            ],
        }
        md = format_rereview_audit_markdown("google", "xmc", 42, data)
        self.assertIn("# PR Re-Review Audit Report & Thread Resolution Plan", md)
        self.assertIn("Thread ID `101`", md)
        self.assertIn("Done, added check.", md)

    @patch("tools.pr_review.review_pr_generate_audit.call_github_api")
    def test_main_initial_success(self, mock_api):
        mock_api.return_value = {"title": "test", "body": "desc", "head": {"sha": "123"}}
        # github_utils serves the paginated files and comments, then review threads.
        self.mock_graphql.side_effect = [[{"filename": "a.py"}], [], _threads_page([])]
        with tempfile.TemporaryDirectory() as tmpdir:
            out_file = str(Path(tmpdir) / "test_audit.md")
            args = build_parser().parse_args(["google", "xmc", "10", "-o", out_file])

            res = main(args)
            self.assertEqual(res["status"], "success")
            self.assertEqual(res["file_count"], 1)
            self.assertTrue(Path(out_file).exists())
            sidecar = json.loads(Path(res["sidecar_file"]).read_text(encoding="utf-8"))
            self.assertEqual(Path(res["sidecar_file"]), Path(out_file).with_suffix(".json"))
            self.assertEqual(sidecar["mode"], "initial")
            self.assertEqual(sidecar["head_sha"], "123")
            self.assertEqual(sidecar["items"], [])

    def test_build_audit_sidecar_rereview_lists_root_threads(self):
        data = {
            "pr_info": {"head": {"sha": "abc"}},
            "comments": [
                _comment(101, path="pkg/a.go", line=15),
                {**_comment(201), "in_reply_to_id": 101},
                {**_comment(102), "line": None, "original_line": 7},
            ],
        }
        sidecar = build_audit_sidecar("google", "xmc", 42, "re-review", data)
        self.assertEqual(
            sidecar["items"],
            [
                {
                    "item_id": "101",
                    "type": "re_review_thread",
                    "thread_id": 101,
                    "file_path": "pkg/a.go",
                    "line": 15,
                    "action": None,
                    "status": "Pending",
                },
                {
                    "item_id": "102",
                    "type": "re_review_thread",
                    "thread_id": 102,
                    "file_path": "a.go",
                    "line": 7,
                    "action": None,
                    "status": "Pending",
                },
            ],
        )

    @patch("tools.pr_review.review_pr_generate_audit.call_github_api")
    def test_main_api_error_raises_handled_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("404 Not Found")
        args = build_parser().parse_args(["google", "xmc", "10", "-o", "/tmp/out.md"])

        with self.assertRaises(HandledError):
            main(args)

    @patch(f"{_AUDIT}.prepare_review_workspace")
    @patch(f"{_AUDIT}.measure_rereview_delta")
    @patch(f"{_AUDIT}.call_github_api")
    def test_main_rereview_with_out_dir_prepares_workspace(self, mock_api, mock_delta, mock_ws):
        mock_api.return_value = {"title": "t", "head": {"sha": "head1"}}
        self.mock_graphql.side_effect = [[], [_comment(101)], _threads_page([])]
        delta = {
            "since_sha": "old1",
            "changed_lines": 500,
            "changed_files": ["a.go"],
            "full_review_recommended": True,
            "reason": "500 changed lines > threshold 200.",
        }
        mock_delta.return_value = delta
        mock_ws.return_value = {"checked_out": True, "diff_file": "/x/pr.diff"}
        with tempfile.TemporaryDirectory() as tmpdir:
            argv = ["google", "xmc", "10", "--mode", "re-review", "--out-dir", tmpdir]
            res = main(build_parser().parse_args([*argv, "--checkout", "--since-sha", "old1"]))

            report = Path(res["output_file"])
            self.assertEqual(report, Path(tmpdir).resolve() / "pr_rereview_audit_report.md")
            self.assertIn("FULL REVIEW RECOMMENDED", report.read_text(encoding="utf-8"))
        mock_delta.assert_called_once_with(
            "google", "xmc", 10, "head1", since_sha="old1", threshold=200
        )
        self.assertEqual(mock_ws.call_args.kwargs["checkout"], True)
        self.assertEqual(mock_ws.call_args.kwargs["rereview_delta"], delta)
        self.assertEqual(mock_ws.call_args.args[3]["threads"], [])
        self.assertEqual(res["rereview_delta"], delta)
        self.assertTrue(res["checked_out"])
        self.assertEqual(res["diff_file"], "/x/pr.diff")

    @patch(f"{_AUDIT}.prepare_review_workspace")
    @patch(f"{_AUDIT}.call_github_api")
    def test_main_without_out_dir_skips_workspace(self, mock_api, mock_ws):
        mock_api.return_value = {"title": "t", "head": {"sha": "123"}}
        self.mock_graphql.side_effect = [[], [], GithubAPIError("GraphQL down")]
        with tempfile.TemporaryDirectory() as tmpdir:
            out_file = str(Path(tmpdir) / "audit.md")
            res = main(build_parser().parse_args(["google", "xmc", "10", "-o", out_file]))
        mock_ws.assert_not_called()
        self.assertNotIn("diff_file", res)
        self.assertNotIn("rereview_delta", res)
        self.assertEqual(res["threads_error"], "GraphQL down")

    @patch(f"{_AUDIT}.call_github_api")
    def test_main_checkout_without_out_dir_raises_before_fetching(self, mock_api):
        with self.assertRaisesRegex(HandledError, "--checkout requires --out-dir"):
            main(build_parser().parse_args(["google", "xmc", "10", "--checkout"]))
        mock_api.assert_not_called()
        self.mock_graphql.assert_not_called()

    @patch(f"{_AUDIT}.prepare_review_workspace")
    @patch(f"{_AUDIT}.measure_rereview_delta")
    @patch(f"{_AUDIT}.call_github_api")
    def test_main_rereview_without_out_dir_still_reports_delta(self, mock_api, mock_delta, mock_ws):
        mock_api.return_value = {"title": "t", "head": {"sha": "head1"}}
        self.mock_graphql.side_effect = [[], [], _threads_page([])]
        mock_delta.return_value = {
            "since_sha": None,
            "full_review_recommended": True,
            "reason": "No earlier review found.",
        }
        with tempfile.TemporaryDirectory() as tmpdir:
            out_file = Path(tmpdir) / "report.md"
            argv = ["google", "xmc", "10", "--mode", "re-review", "-o", str(out_file)]
            res = main(build_parser().parse_args(argv))
            report = out_file.read_text(encoding="utf-8")
        mock_ws.assert_not_called()
        self.assertTrue(res["rereview_delta"]["full_review_recommended"])
        self.assertIn("- **Delta Since Last Review** (`none`): FULL REVIEW RECOMMENDED", report)

    def test_resolve_output_path_out_dir_precedence(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            out_dir = str(Path(tmpdir) / "session")
            self.assertEqual(
                resolve_output_path("", "conv", "a.md", out_dir), Path(out_dir) / "a.md"
            )
            self.assertTrue(Path(out_dir).is_dir())
            self.assertEqual(
                resolve_output_path("/tmp/x.md", "", "a.md", out_dir), Path("/tmp/x.md")
            )

    def test_format_rereview_without_delta_has_no_delta_line(self):
        md = format_rereview_audit_markdown(
            "google", "xmc", 42, {"pr_info": {"title": "t"}, "comments": [], "threads": []}
        )
        self.assertNotIn("Delta Since Last Review", md)
        self.mock_graphql.assert_not_called()

    def _initial_data(self, comments: list[dict[str, Any]]) -> dict[str, Any]:
        return {"pr_info": {"title": "t", "body": "b"}, "files": [], "comments": comments}

    def test_resolved_threads_follow_pagination(self):
        """A resolved thread on page 2 is listed as resolved, in the unchanged format."""
        self.mock_graphql.side_effect = [
            _threads_page([_thread(101, resolved=False)], has_next=True, cursor="c1"),
            _threads_page([_thread(102, resolved=True)]),
        ]
        md = format_initial_audit_markdown(
            "google", "xmc", 42, self._initial_data([_comment(101), _comment(102)])
        )
        self.assertEqual(self.mock_graphql.call_count, 2)
        self.assertEqual(
            self.mock_graphql.call_args_list[1].kwargs["payload"]["variables"]["cursor"], "c1"
        )
        open_part, resolved_part = md.split("**Already Resolved Threads on GitHub UI")
        self.assertIn(
            "- **Comment ID `101`** | File: `a.go` | Line: `1` | Author: `alice` | Text: *Nit.*",
            open_part,
        )
        self.assertIn("- **Comment ID `102`**", resolved_part)
        self.assertNotIn("Comment ID `101`", resolved_part)

    def test_resolved_threads_null_end_cursor_stops(self):
        """Regression: hasNextPage with a null endCursor must stop after one page."""
        self.mock_graphql.return_value = _threads_page(
            [_thread(101, resolved=True)], has_next=True, cursor=None
        )
        md = format_rereview_audit_markdown(
            "google", "xmc", 42, {"pr_info": {"title": "t"}, "comments": [_comment(101)]}
        )
        self.mock_graphql.assert_called_once()
        self.assertIn("Already Resolved on GitHub UI (Bucket 1)", md)

    def test_resolved_threads_null_nodes_tolerated(self):
        """Null thread nodes, comment lists, and first comments are skipped."""
        null_comments = _thread(102, resolved=True)
        null_comments["comments"] = None
        null_first = _thread(103, resolved=True)
        null_first["comments"]["nodes"] = [None]
        self.mock_graphql.return_value = _threads_page(
            [None, _thread(101, resolved=True), null_comments, null_first]
        )
        md = format_initial_audit_markdown(
            "google", "xmc", 42, self._initial_data([_comment(101), _comment(102)])
        )
        open_part, resolved_part = md.split("**Already Resolved Threads on GitHub UI")
        self.assertIn("Comment ID `101`", resolved_part)
        self.assertIn("Comment ID `102`", open_part)

    def test_resolved_threads_api_error_treated_as_open(self):
        """A GraphQL failure leaves all threads open instead of failing the audit."""
        self.mock_graphql.side_effect = GithubAPIError("GraphQL down")
        md = format_initial_audit_markdown("google", "xmc", 42, self._initial_data([_comment(101)]))
        self.assertIn("Comment ID `101`", md)
        self.assertNotIn("Already Resolved Threads", md)


if __name__ == "__main__":
    unittest.main()
