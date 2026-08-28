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

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.review_pr_verify_posted import (
    parse_checklist_approved_items,
    verify_review_comments_posted,
)


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

    @patch("tools.pr_review.review_pr_verify_posted.call_github_api")
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

    @patch("tools.pr_review.review_pr_verify_posted.call_github_api")
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

    @patch("tools.pr_review.review_pr_verify_posted.call_github_api")
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


if __name__ == "__main__":
    unittest.main()
