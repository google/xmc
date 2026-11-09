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

"""Unit tests for review_pr_verify_posted tool."""

import argparse
import json
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAuthError
from tools.pr_review.review_pr_verify_posted import (
    _is_re_review_thread_posted,
    _LiveState,
    _normalize_body,
    approved_sidecar_items,
    load_sidecar,
    parse_checklist_approved_items,
    verify_review_comments_posted,
)

# Live comments and review threads are fetched via the shared github_utils helpers.
_GH_API = "tools.pr_review.github_utils.call_github_api"
_LOGIN = "tools.pr_review.review_pr_verify_posted.get_authenticated_login"
_TELEMETRY = '\n\n<!-- xmc-telemetry: {"git_username": "me"} -->'


def _live_comment(
    line: int = 15, body: str = "Add nil check", login: str = "me", path: str = "pkg/a.go"
) -> dict[str, Any]:
    """Builds a live inline review comment as posted by review_pr_post_inline_comment.py."""
    return {"path": path, "line": line, "body": body + _TELEMETRY, "user": {"login": login}}


def _reply(root_id: int, login: str = "me") -> dict[str, Any]:
    """Builds a live reply to the review thread whose root comment has `root_id`."""
    return {"in_reply_to_id": root_id, "user": {"login": login}}


def _thread(root_id: int, is_resolved: bool) -> dict[str, Any]:
    """Builds a GraphQL review thread node whose root comment has `root_id`."""
    return {
        "id": f"PRRT_{root_id}",
        "isResolved": is_resolved,
        "comments": {"nodes": [{"databaseId": root_id}, {"databaseId": root_id + 1000}]},
    }


def _route_github(comments: list[Any], threads: list[dict[str, Any]]):
    """Returns a fake call_github_api serving REST comments and GraphQL review threads."""

    def fake(endpoint: str, method: str = "GET", payload: Any = None, user_agent: str = ""):
        if endpoint != "/graphql":
            return comments
        review_threads = {"pageInfo": {"hasNextPage": False}, "nodes": threads}
        return {"data": {"repository": {"pullRequest": {"reviewThreads": review_threads}}}}

    return fake


class TestReviewPRVerifyPosted(unittest.TestCase):
    """Test suite for review_pr_verify_posted tool."""

    def setUp(self):
        login_patch = patch(_LOGIN, return_value="me")
        login_patch.start()
        self.addCleanup(login_patch.stop)

    def test_parse_checklist_approved_items_initial(self):
        content = """
| Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- |
| #1 | Approved | `pkg/auth/handler.go` | 15 | `core_engineering.md` | Add check |
| #2 | Draft | `pkg/auth/handler.go` | 20 | `core_engineering.md` | Not confirmed |
"""
        items = parse_checklist_approved_items(content)
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["item_id"], "#1")
        self.assertEqual(items[0]["file_path"], "pkg/auth/handler.go")
        self.assertEqual(items[0]["line"], 15)

    def test_parse_checklist_approved_items_rereview(self):
        content = """
### Thread ID `101` (`pkg/a.go`:L10)
- **Audit Verification Status**: `- [ ] Pending Analysis`
- **Recommended Action**: `[RESPOND: Need edge case check]`
---
### Thread ID `102` (`pkg/b.go`:L20)
- **Audit Verification Status**: `- [ ] Pending Analysis`
- **Recommended Action**: `[RESOLVE]`
---
"""
        items = parse_checklist_approved_items(content)
        self.assertEqual(len(items), 2)
        self.assertEqual(items[0]["thread_id"], 101)
        self.assertEqual(items[0]["action"], "RESPOND")
        self.assertEqual(items[1]["thread_id"], 102)
        self.assertEqual(items[1]["action"], "RESOLVE")

    @patch(_GH_API)
    def test_verify_review_comments_posted_success(self, mock_api):
        content = """
| Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- |
| #1 | Approved | `pkg/auth/handler.go` | 15 | `core_engineering.md` | Add check |
"""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write(content)
            f_path = f.name

        mock_api.return_value = [_live_comment(path="pkg/auth/handler.go", body="Add check")]

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 10
        args.checklist_file = f_path

        res = verify_review_comments_posted(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["posted_items"], 1)

        # Check updated markdown file marked Status as Posted
        updated = Path(f_path).read_text(encoding="utf-8")
        self.assertIn("Posted", updated)

        # Re-running verification on the updated file with 'Posted' status should succeed idempotently
        res2 = verify_review_comments_posted(args)
        self.assertEqual(res2["status"], "success")
        self.assertEqual(res2["posted_items"], 1)

    @patch(_GH_API)
    def test_verify_review_comments_posted_missing_raises_handled_error(self, mock_api):
        content = """
| Item | Status | File Path | Line | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- |
| #1 | Approved | `pkg/auth/handler.go` | 15 | `core_engineering.md` | Add check |
"""
        with tempfile.NamedTemporaryFile("w+", encoding="utf-8", delete=False) as f:
            f.write(content)
            f_path = f.name

        mock_api.return_value = []  # No comments posted live!

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 10
        args.checklist_file = f_path

        with self.assertRaises(HandledError):
            verify_review_comments_posted(args)

    @patch(_GH_API)
    def test_verify_review_comments_posted_unverified_resolve(self, mock_api):
        """Regression: RESOLVE fails unless GitHub reports the thread resolved, even if marked [x]."""
        content = """
### Thread ID `201` (`pkg/test.go`:L10)
- **Audit Verification Status**: `- [ ] Pending Analysis`
- **Recommended Action**: `[RESOLVE]`

### Thread ID `202` (`pkg/test2.go`:L20)
- **Audit Verification Status**: `- [x] Verified Fixed`
- **Recommended Action**: `[RESOLVE]`
"""
        with tempfile.NamedTemporaryFile("w+", delete=False) as f:
            f.write(content)
            f_path = f.name

        # Thread 202 has a reply but is still unresolved on GitHub.
        mock_api.side_effect = _route_github([{"in_reply_to_id": 202}], [_thread(202, False)])

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 10
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(args)

        instruction = str(ctx.exception.instruction)
        self.assertIn("RESOLVE action on Thread ID `201` has not been verified", instruction)
        self.assertIn("RESOLVE action on Thread ID `202` has not been verified", instruction)

    def test_parse_checklist_approved_items_dynamic_columns(self):
        content = """
| Item | Scope | Status | Target File | Line | Priority | Rule Reference | Proposed Inline Comment |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| #1 | [Group A] | Approved | `cmd/ui/app/page.ts` | 42 | Blocker | `core_engineering.md#1` | Remove mock data |
| #2 | [Group A] | Confirmed | `cmd/ui/app/page.ts` | 55 | Actionable | `core_engineering.md#2` | Add guard return |
| #3 | [Group A] | Draft | `cmd/ui/app/page.ts` | 80 | Nit | `core_engineering.md#3` | Optional styling |
"""
        items = parse_checklist_approved_items(content)
        self.assertEqual(len(items), 2)
        self.assertEqual(items[0]["item_id"], "#1")
        self.assertEqual(items[0]["file_path"], "cmd/ui/app/page.ts")
        self.assertEqual(items[0]["line"], 42)
        self.assertEqual(items[1]["item_id"], "#2")
        self.assertEqual(items[1]["line"], 55)

    def test_is_re_review_thread_posted_unknown_action(self):
        item = {"action": "CUSTOM_ACTION", "thread_id": 301}
        ok, err = _is_re_review_thread_posted(item, _LiveState("me", [], frozenset()))
        self.assertFalse(ok)
        self.assertEqual(err, "Unknown action 'CUSTOM_ACTION' on Thread ID `301`")


_HEADING_CHECKLIST = """
#### #1 Approved: add nil check
- File: `pkg/a.go` line 15
"""


def _sidecar(items: list[Any]) -> dict[str, Any]:
    return {"owner": "google", "repo": "xmc", "pr_number": 10, "items": items}


def _inline_item(item_id: str, status: str, line: int = 15) -> dict[str, Any]:
    return {
        "item_id": item_id,
        "type": "initial_inline",
        "status": status,
        "file_path": "pkg/a.go",
        "line": line,
        "rule": "core_engineering.md",
        "proposed": "Add nil check",
    }


def _resolve_item(thread_id: int, status: str = "Pending") -> dict[str, Any]:
    return {
        "item_id": str(thread_id),
        "type": "re_review_thread",
        "thread_id": thread_id,
        "status": status,
        "action": "RESOLVE",
    }


class TestVerifyPostedSidecar(unittest.TestCase):
    """Tests for the JSON sidecar, zero-item failure, and --mark-posted."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.md_path = Path(tmp.name) / "pr_review_audit_checklist.md"
        self.json_path = self.md_path.with_suffix(".json")
        api_patch = patch(_GH_API)
        self.mock_api = api_patch.start()
        self.addCleanup(api_patch.stop)
        login_patch = patch(_LOGIN, return_value="me")
        login_patch.start()
        self.addCleanup(login_patch.stop)

    def _args(self, checklist: Path, mark_posted: list[str] | None = None) -> argparse.Namespace:
        return argparse.Namespace(
            owner="google",
            repo="xmc",
            pr_number=10,
            checklist_file=str(checklist),
            mark_posted=mark_posted,
        )

    def _write(self, markdown: str, items: list[dict[str, Any]] | None) -> None:
        self.md_path.write_text(markdown, encoding="utf-8")
        if items is not None:
            self.json_path.write_text(json.dumps(_sidecar(items)), encoding="utf-8")

    def _sidecar_statuses(self) -> dict[str, str]:
        sidecar = json.loads(self.json_path.read_text(encoding="utf-8"))
        return {item["item_id"]: item["status"] for item in sidecar["items"]}

    def test_zero_items_fails_loudly(self):
        """Regression: an unparseable heading-style checklist fails instead of passing."""
        self._write(_HEADING_CHECKLIST, None)
        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))
        self.assertIn("No approved items", ctx.exception.message)
        self.mock_api.assert_not_called()

    def test_zero_sidecar_items_error_names_sidecar_path(self):
        """The zero-items error names the sidecar file, not the markdown passed via -c."""
        self._write(_HEADING_CHECKLIST, [_inline_item("#1", "Draft")])
        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))
        self.assertIn(f"sidecar '{self.json_path}'", ctx.exception.message)

    def test_sidecar_path_still_updates_sibling_markdown(self):
        """Passing the .json sidecar via -c also updates its sibling markdown table."""
        markdown = "| Item | Status | File Path | Line |\n| :--- | :--- | :--- | :--- |\n| #1 | Approved | `pkg/a.go` | 15 |\n"
        self._write(markdown, [_inline_item("#1", "Approved")])

        verify_review_comments_posted(self._args(self.json_path, mark_posted=["#1"]))

        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted"})
        self.assertIn("| #1 | Posted |", self.md_path.read_text(encoding="utf-8"))

    def test_sidecar_items_preferred_over_markdown(self):
        """Approved sidecar items are verified even when the markdown has none."""
        self._write(
            _HEADING_CHECKLIST, [_inline_item("#1", "Approved"), _inline_item("#2", "Draft")]
        )
        self.mock_api.return_value = [_live_comment()]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["items_source"], "sidecar")
        self.assertEqual(res["total_checklist_items"], 1)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted", "#2": "Draft"})

    def test_live_comments_paginated_beyond_first_page(self):
        """Regression: a comment beyond the first 30 (or 100) live comments is found."""
        self._write("", [_inline_item("#1", "Approved", line=500)])
        first_page = [{"path": "pkg/a.go", "line": n} for n in range(1, 101)]
        self.mock_api.side_effect = [first_page, [_live_comment(line=500)]]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)
        self.assertEqual(self.mock_api.call_count, 2)
        endpoints = [c.args[0] for c in self.mock_api.call_args_list]
        self.assertTrue(endpoints[0].endswith("/pulls/10/comments?page=1&per_page=100"))
        self.assertTrue(endpoints[1].endswith("/pulls/10/comments?page=2&per_page=100"))

    def test_unposted_items_are_not_marked_posted(self):
        """Only verified items get the Posted status."""
        self._write("", [_inline_item("#1", "Approved"), _inline_item("#2", "Approved", line=99)])
        self.mock_api.return_value = [_live_comment()]

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.json_path))

        self.assertIn("'#2'", ctx.exception.instruction)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted", "#2": "Approved"})

    def test_other_users_comment_on_same_line_is_not_posted(self):
        """Regression: someone else's comment on the item's line does not satisfy it."""
        self._write("", [_inline_item("#1", "Approved")])
        self.mock_api.return_value = [_live_comment(login="other-reviewer")]

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))

        self.assertIn("no comment by @me", ctx.exception.instruction)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Approved"})

    def test_own_comment_with_different_body_is_not_posted(self):
        """Regression: an own comment on the line without the proposed text does not count."""
        self._write("", [_inline_item("#1", "Approved")])
        self.mock_api.return_value = [_live_comment(body="Rename this variable.")]

        with self.assertRaises(HandledError):
            verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(self._sidecar_statuses(), {"#1": "Approved"})

    def test_proposed_text_matches_after_normalization(self):
        """The proposed text matches inside a longer, reformatted posted body."""
        self._write("", [_inline_item("#1", "Approved")])
        body = "**ADD   nil\ncheck**: guard `cfg` first.\n\n```go\nif cfg == nil {}\n```"
        self.mock_api.return_value = [_live_comment(body=body)]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)

    def test_non_dict_live_comments_are_skipped(self):
        """Stray non-dict elements in the live comments do not break matching."""
        self._write("", [_inline_item("#1", "Approved"), _resolve_item(101)])
        threads = [_thread(101, True)]
        threads[0]["comments"]["nodes"].insert(0, "not a node")
        self.mock_api.side_effect = _route_github(["not a comment", _live_comment()], threads)

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 2)

    def test_non_dict_live_comments_skipped_for_respond(self):
        """A RESPOND item ignores non-dict live comments and still finds the reply."""
        self._write("", [{**_resolve_item(101), "action": "RESPOND"}])
        self.mock_api.return_value = [None, _reply(101)]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)

    def test_respond_reply_by_other_user_is_not_posted(self):
        """Regression: a RESPOND item fails when only another user replied."""
        self._write("", [{**_resolve_item(101), "action": "RESPOND"}])
        self.mock_api.return_value = [_reply(101, login="other-reviewer")]

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))

        self.assertIn("no reply by @me", ctx.exception.instruction)
        self.assertEqual(self._sidecar_statuses(), {"101": "Pending"})

    def test_resolve_marked_posted_but_unresolved_thread_fails(self):
        """Regression: a RESOLVE item recorded as Posted fails while the thread is unresolved."""
        self._write("", [_resolve_item(101, status="Posted")])
        self.mock_api.side_effect = _route_github([{"in_reply_to_id": 101}], [_thread(101, False)])

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))

        self.assertIn("not resolved on GitHub", ctx.exception.instruction)

    def test_resolve_passes_when_thread_resolved_on_github(self):
        """A RESOLVE item passes once GitHub reports its review thread resolved."""
        self._write("", [_resolve_item(101)])
        self.mock_api.side_effect = _route_github([], [_thread(7, False), _thread(101, True)])

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)
        self.assertEqual(self._sidecar_statuses()["101"], "Posted")
        graphql_calls = [c for c in self.mock_api.call_args_list if c.args[0] == "/graphql"]
        self.assertEqual(len(graphql_calls), 1)

    def test_sidecar_rereview_thread_actions(self):
        """Re-review sidecar threads with an action are verified; null actions are skipped."""
        thread = {
            "item_id": "101",
            "type": "re_review_thread",
            "thread_id": 101,
            "status": "Pending",
        }
        self._write(
            "",
            [
                {**thread, "action": "RESPOND"},
                {**thread, "item_id": "102", "thread_id": 102, "action": None},
            ],
        )
        self.mock_api.return_value = [_reply(101)]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)
        self.assertEqual(self._sidecar_statuses()["101"], "Posted")

    def test_mark_posted_updates_sidecar_and_markdown(self):
        """--mark-posted records items as Posted in both files without calling GitHub."""
        markdown = "| Item | Status | File Path | Line |\n| :--- | :--- | :--- | :--- |\n| #1 | Approved | `pkg/a.go` | 15 |\n"
        self._write(markdown, [_inline_item("#1", "Approved")])

        res = verify_review_comments_posted(self._args(self.md_path, mark_posted=["1"]))

        self.assertEqual(res["marked_posted"], ["1"])
        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted"})
        self.assertIn("| #1 | Posted |", self.md_path.read_text(encoding="utf-8"))
        self.mock_api.assert_not_called()

    def test_mark_posted_rejects_unapproved_or_unknown_ids(self):
        self._write("", [_inline_item("#1", "Approved"), _inline_item("#2", "Draft")])
        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path, mark_posted=["#2", "#7"]))
        self.assertIn("2, 7", ctx.exception.message)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Approved", "#2": "Draft"})

    def test_invalid_sidecar_json_raises(self):
        self.md_path.write_text("", encoding="utf-8")
        self.json_path.write_text("{not json", encoding="utf-8")
        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))
        self.assertIn("not valid JSON", ctx.exception.message)

    def test_sidecar_missing_items_key_raises(self):
        self.md_path.write_text("", encoding="utf-8")
        self.json_path.write_text(json.dumps({"owner": "google"}), encoding="utf-8")
        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.md_path))
        self.assertIn("has no 'items' list", ctx.exception.message)
        self.mock_api.assert_not_called()


class TestApprovedSidecarItems(unittest.TestCase):
    def test_filters_invalid_items(self):
        sidecar = _sidecar(
            [
                "not a dict",
                _inline_item("#1", "Confirmed"),
                _inline_item("#2", "Approved", line=0),
                {**_inline_item("#3", "Approved"), "file_path": ""},
                {"type": "re_review_thread", "thread_id": "x", "action": "RESOLVE"},
                {"type": "re_review_thread", "thread_id": 5, "action": "resolve"},
            ]
        )
        items = approved_sidecar_items(sidecar)
        self.assertEqual([i.get("item_id") or i["thread_id"] for i in items], ["#1", 5])
        self.assertEqual(items[1]["action"], "RESOLVE")

    def test_integer_strings_accepted_bools_and_floats_rejected(self):
        sidecar = _sidecar(
            [
                {**_inline_item("#1", "Approved"), "line": " 15 "},
                {**_inline_item("#2", "Approved"), "line": True},
                {**_inline_item("#3", "Approved"), "line": 15.0},
                {**_inline_item("#4", "Approved"), "line": "-3"},
                {"type": "re_review_thread", "thread_id": "101", "action": "RESPOND"},
                {"type": "re_review_thread", "thread_id": False, "action": "RESOLVE"},
            ]
        )
        items = approved_sidecar_items(sidecar)
        self.assertEqual(len(items), 2)
        self.assertEqual((items[0]["item_id"], items[0]["line"]), ("#1", 15))
        self.assertEqual(items[1]["thread_id"], 101)

    def test_missing_sidecar_returns_none(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            self.assertIsNone(load_sidecar(Path(tmpdir) / "checklist.json"))


class TestNormalizeBody(unittest.TestCase):
    def test_none_returns_empty_string(self):
        self.assertEqual(_normalize_body(None), "")

    def test_non_str_is_stringified(self):
        self.assertEqual(_normalize_body(42), "42")

    def test_falsy_non_none_is_not_treated_as_empty(self):
        self.assertEqual(_normalize_body(0), "0")
        self.assertEqual(_normalize_body(False), "false")

    def test_strips_marker_markdown_and_whitespace(self):
        self.assertEqual(_normalize_body(f"  Use **`Foo`**\n now{_TELEMETRY}"), "use foo now")

    def test_table_cell_escapes_match_rendered_body(self):
        cell = _normalize_body(r"Use `a \| b`<br>then <BR/>guard")
        self.assertEqual(cell, _normalize_body("Use `a | b`\nthen\nguard"))


class TestVerifyPostedLiveState(unittest.TestCase):
    """Tests for live-state fetching and author/line-only matching."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.json_path = Path(tmp.name) / "pr_review_audit_checklist.json"
        api_patch = patch(_GH_API)
        self.mock_api = api_patch.start()
        self.addCleanup(api_patch.stop)
        self.args = argparse.Namespace(
            owner="google", repo="xmc", pr_number=10, checklist_file=str(self.json_path)
        )

    def _write(self, items: list[dict[str, Any]]) -> None:
        self.json_path.write_text(json.dumps(_sidecar(items)), encoding="utf-8")

    @patch(_LOGIN, return_value="me")
    def test_empty_proposed_matches_on_author_path_and_line(self, _):
        self._write([{**_inline_item("#1", "Approved"), "proposed": ""}])
        self.mock_api.return_value = [_live_comment(body="Anything at all")]

        res = verify_review_comments_posted(self.args)

        self.assertEqual(res["posted_items"], 1)

    @patch(_LOGIN, return_value="me")
    def test_review_threads_not_fetched_without_resolve_items(self, _):
        self._write([_inline_item("#1", "Approved")])
        self.mock_api.return_value = [_live_comment()]

        verify_review_comments_posted(self.args)

        endpoints = [c.args[0] for c in self.mock_api.call_args_list]
        self.assertNotIn("/graphql", endpoints)

    @patch(_LOGIN, side_effect=GithubAuthError("no credentials"))
    def test_login_failure_raises_handled_error(self, _):
        self._write([_inline_item("#1", "Approved")])

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self.args)

        self.assertIn("no credentials", ctx.exception.message)


if __name__ == "__main__":
    unittest.main()
