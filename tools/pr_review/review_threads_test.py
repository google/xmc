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

"""Unit tests for review_threads tool."""

import unittest
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError
from tools.pr_review.review_threads import (
    build_parser,
    list_threads,
    main,
    resolve_replied,
    summarize_thread,
)

_MODULE = "tools.pr_review.review_threads"
_ME = "me"


def _comment(author: str, body: str = "body", url: str = "") -> dict:
    return {"databaseId": 1, "body": body, "url": url, "author": {"login": author}}


def _thread(thread_id: str, resolved: bool, authors: list[str]) -> dict:
    return {
        "id": thread_id,
        "isResolved": resolved,
        "path": "pkg/a.go",
        "line": 10,
        "comments": {"nodes": [_comment(author) for author in authors]},
    }


# Baseline fixture covering every resolve-replied bucket.
_THREADS = [
    _thread("PRRT_replied", False, ["bot", _ME]),
    _thread("PRRT_unreplied", False, ["bot"]),
    _thread("PRRT_resolved", True, ["bot", _ME]),
    _thread("PRRT_mine_only", False, [_ME]),
    _thread("PRRT_other_reply", False, ["bot", "someone"]),
]


class SummarizeThreadTest(unittest.TestCase):
    """Tests for summarize_thread."""

    def test_summarizes_replied_thread(self):
        thread = _thread("PRRT_1", False, ["bot", _ME])
        thread["comments"]["nodes"][0] = _comment("bot", "First\n  line", "https://x/1")
        self.assertEqual(
            summarize_thread(thread, _ME),
            {
                "thread_id": "PRRT_1",
                "location": "pkg/a.go:10",
                "is_resolved": False,
                "replied_by_me": True,
                "first_comment_author": "bot",
                "first_comment_url": "https://x/1",
                "first_comment_snippet": "First line",
            },
        )

    def test_first_comment_by_me_is_not_a_reply(self):
        self.assertFalse(summarize_thread(_thread("PRRT_1", False, [_ME]), _ME)["replied_by_me"])

    def test_truncates_long_snippet(self):
        thread = _thread("PRRT_1", False, ["bot"])
        thread["comments"]["nodes"][0]["body"] = "x" * 500
        snippet = summarize_thread(thread, _ME)["first_comment_snippet"]
        self.assertEqual(len(snippet), 120)
        self.assertTrue(snippet.endswith("..."))

    def test_handles_null_comment_nodes(self):
        thread = _thread("PRRT_1", False, ["bot"])
        thread["comments"]["nodes"] = [None, _comment(_ME)]
        summary = summarize_thread(thread, _ME)
        self.assertEqual(summary["first_comment_author"], "unknown")
        self.assertTrue(summary["replied_by_me"])

        thread["comments"]["nodes"] = [_comment("bot"), None, _comment(_ME)]
        summary = summarize_thread(thread, _ME)
        self.assertEqual(summary["first_comment_author"], "bot")
        self.assertTrue(summary["replied_by_me"])

    def test_handles_missing_fields(self):
        summary = summarize_thread({"id": "PRRT_1", "comments": None, "path": None}, _ME)
        self.assertEqual(summary["location"], "Unknown:?")
        self.assertEqual(summary["first_comment_author"], "unknown")
        self.assertEqual(summary["first_comment_snippet"], "")
        self.assertFalse(summary["replied_by_me"])


class GetLoginTest(unittest.TestCase):
    """Tests for the CLI-boundary error conversion of the login lookup."""

    @patch(f"{_MODULE}.get_authenticated_login", side_effect=GithubAuthError("no auth"))
    def test_auth_error_raises_handled(self, mock_login):
        args = build_parser().parse_args(["list", "google", "xmc", "7"])
        with self.assertRaises(HandledError) as ctx:
            list_threads(args)
        mock_login.assert_called_once_with(user_agent="XMC-Review-Threads-Agent")
        self.assertIn("no auth", ctx.exception.message)
        self.assertIn("gh auth login", ctx.exception.instruction)

    @patch(f"{_MODULE}.get_authenticated_login", side_effect=GithubAPIError("no login"))
    def test_api_error_raises_handled(self, _):
        args = build_parser().parse_args(["resolve-replied", "google", "xmc", "7"])
        with self.assertRaises(HandledError) as ctx:
            resolve_replied(args)
        self.assertIn("no login", ctx.exception.message)


@patch(f"{_MODULE}.get_authenticated_login", return_value=_ME)
@patch(f"{_MODULE}.list_review_threads", return_value=_THREADS)
class ListThreadsTest(unittest.TestCase):
    """Tests for the list subcommand."""

    def test_lists_all_threads(self, mock_list, _):
        args = build_parser().parse_args(["list", "google", "xmc", "7"])
        res = list_threads(args)
        mock_list.assert_called_once_with("google", "xmc", 7, user_agent="XMC-Review-Threads-Agent")
        self.assertEqual(res["authenticated_user"], _ME)
        self.assertEqual(res["unresolved_count"], 4)
        self.assertEqual(len(res["threads"]), 5)

    def test_unresolved_only_filters(self, *_):
        args = build_parser().parse_args(["list", "--unresolved-only", "google", "xmc", "7"])
        res = list_threads(args)
        self.assertEqual(res["unresolved_count"], 4)
        self.assertNotIn("PRRT_resolved", [t["thread_id"] for t in res["threads"]])
        self.assertEqual(len(res["threads"]), 4)

    def test_api_error_raises_handled(self, mock_list, _):
        mock_list.side_effect = GithubAPIError("GraphQL query failed")
        args = build_parser().parse_args(["list", "google", "xmc", "7"])
        with self.assertRaises(HandledError) as ctx:
            list_threads(args)
        self.assertIn("Failed to list review threads", ctx.exception.message)


@patch(f"{_MODULE}.get_authenticated_login", return_value=_ME)
@patch(f"{_MODULE}.list_review_threads", return_value=_THREADS)
@patch(f"{_MODULE}.resolve_review_thread")
class ResolveRepliedTest(unittest.TestCase):
    """Tests for the resolve-replied subcommand."""

    def test_dry_run_resolves_nothing(self, mock_resolve, *_):
        args = build_parser().parse_args(["resolve-replied", "google", "xmc", "7", "--dry-run"])
        res = resolve_replied(args)
        mock_resolve.assert_not_called()
        self.assertTrue(res["dry_run"])
        self.assertEqual([c["thread_id"] for c in res["candidates"]], ["PRRT_replied"])
        self.assertEqual(
            res["skipped_unreplied"], ["PRRT_unreplied", "PRRT_mine_only", "PRRT_other_reply"]
        )
        self.assertNotIn("resolved", res)

    def test_resolves_only_replied_unresolved_threads(self, mock_resolve, *_):
        args = build_parser().parse_args(["resolve-replied", "google", "xmc", "7"])
        res = resolve_replied(args)
        mock_resolve.assert_called_once_with(
            "google", "xmc", 7, "PRRT_replied", user_agent="XMC-Review-Threads-Agent"
        )
        self.assertEqual(res["resolved"], ["PRRT_replied"])
        self.assertEqual(res["failed"], [])

    def test_partial_failure_raises_with_details(self, mock_resolve, mock_list, _):
        mock_list.return_value = _THREADS + [_thread("PRRT_replied_2", False, ["bot", _ME])]
        mock_resolve.side_effect = [GithubAPIError("forbidden"), {"status": "success"}]
        args = build_parser().parse_args(["resolve-replied", "google", "xmc", "7"])
        with self.assertRaises(HandledError) as ctx:
            resolve_replied(args)
        self.assertIn("Failed to resolve 1 of 2", ctx.exception.message)
        details = ctx.exception.details or {}
        self.assertEqual(details["resolved"], ["PRRT_replied_2"])
        self.assertEqual(details["failed"][0]["thread_id"], "PRRT_replied")


class BuildParserTest(unittest.TestCase):
    """Tests for build_parser."""

    def test_requires_subcommand(self):
        with self.assertRaises(SystemExit):
            build_parser().parse_args([])

    def test_dispatches_handlers(self):
        parser = build_parser()
        self.assertIs(parser.parse_args(["list", "o", "r", "1"]).handler, list_threads)
        self.assertIs(
            parser.parse_args(["resolve-replied", "o", "r", "1"]).handler, resolve_replied
        )

    @patch(f"{_MODULE}.run_tool")
    def test_main_dispatches_to_selected_handler(self, mock_run_tool):
        main()
        _, main_fn = mock_run_tool.call_args.args
        args = build_parser().parse_args(["list", "o", "r", "1"])
        args.handler = lambda parsed: {"handled": parsed.pr_number}
        self.assertEqual(main_fn(args), {"handled": 1})


if __name__ == "__main__":
    unittest.main()
