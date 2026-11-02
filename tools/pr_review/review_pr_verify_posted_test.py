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
from tools.pr_review.review_pr_verify_posted import (
    _is_re_review_thread_posted,
    approved_sidecar_items,
    load_sidecar,
    parse_checklist_approved_items,
    verify_review_comments_posted,
)

# Live comments are fetched via the shared github_utils.list_all_pages helper.
_GH_API = "tools.pr_review.github_utils.call_github_api"


class TestReviewPRVerifyPosted(unittest.TestCase):
    """Test suite for review_pr_verify_posted tool."""

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

        mock_api.return_value = [
            {"path": "pkg/auth/handler.go", "line": 15, "body": "Posted comment"}
        ]

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

        mock_api.return_value = []  # No replies posted

        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.pr_number = 10
        args.checklist_file = f_path

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(args)

        self.assertIn(
            "RESOLVE action on Thread ID `201` has not been verified",
            str(ctx.exception.instruction),
        )

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
        ok, err = _is_re_review_thread_posted(item, "", [])
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

    def test_sidecar_items_preferred_over_markdown(self):
        """Approved sidecar items are verified even when the markdown has none."""
        self._write(
            _HEADING_CHECKLIST, [_inline_item("#1", "Approved"), _inline_item("#2", "Draft")]
        )
        self.mock_api.return_value = [{"path": "pkg/a.go", "line": 15}]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["items_source"], "sidecar")
        self.assertEqual(res["total_checklist_items"], 1)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted", "#2": "Draft"})

    def test_live_comments_paginated_beyond_first_page(self):
        """Regression: a comment beyond the first 30 (or 100) live comments is found."""
        self._write("", [_inline_item("#1", "Approved", line=500)])
        first_page = [{"path": "pkg/a.go", "line": n} for n in range(1, 101)]
        self.mock_api.side_effect = [first_page, [{"path": "pkg/a.go", "line": 500}]]

        res = verify_review_comments_posted(self._args(self.md_path))

        self.assertEqual(res["posted_items"], 1)
        self.assertEqual(self.mock_api.call_count, 2)
        endpoints = [c.args[0] for c in self.mock_api.call_args_list]
        self.assertTrue(endpoints[0].endswith("/pulls/10/comments?page=1&per_page=100"))
        self.assertTrue(endpoints[1].endswith("/pulls/10/comments?page=2&per_page=100"))

    def test_unposted_items_are_not_marked_posted(self):
        """Only verified items get the Posted status."""
        self._write("", [_inline_item("#1", "Approved"), _inline_item("#2", "Approved", line=99)])
        self.mock_api.return_value = [{"path": "pkg/a.go", "line": 15}]

        with self.assertRaises(HandledError) as ctx:
            verify_review_comments_posted(self._args(self.json_path))

        self.assertIn("'#2'", ctx.exception.instruction)
        self.assertEqual(self._sidecar_statuses(), {"#1": "Posted", "#2": "Approved"})

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
        self.mock_api.return_value = [{"in_reply_to_id": 101}]

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

    def test_missing_sidecar_returns_none(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            self.assertIsNone(load_sidecar(Path(tmpdir) / "checklist.json"))


if __name__ == "__main__":
    unittest.main()
