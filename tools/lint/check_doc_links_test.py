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

"""Tests for check_doc_links."""

from __future__ import annotations

import argparse
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import check_doc_links as cli  # noqa: E402
from tools.lint.check_agent_doc_paths import tracked_paths  # noqa: E402

_EXISTING = tracked_paths(
    [
        "README.md",
        "docs/developer.md",
        "docs/my file.md",
        "docs/foo(1).md",
        "deploy/ctk_v2/README.md",
    ]
)


class ResolveRepoTargetTest(unittest.TestCase):
    def test_ignores_external_and_anchor_links(self) -> None:
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "https://example.com"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "HTTPS://example.com"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "http://example.com"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "mailto:x@example.com"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "MAILTO:x@example.com"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "gs://bucket/path"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "//example.com/path"), ("", None))
        self.assertEqual(cli.resolve_repo_target("docs/a.md", "#section"), ("", None))

    def test_resolves_relative_links_with_fragments(self) -> None:
        self.assertEqual(
            cli.resolve_repo_target("docs/design/topologies.md", "../../deploy/ctk_v2/README.md#s"),
            ("deploy/ctk_v2/README.md", None),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "../README.md?plain=1"),
            ("README.md", None),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "/docs/developer.md"),
            ("docs/developer.md", None),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "my%20file.md"),
            ("docs/my file.md", None),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "my%20file.md?plain=1#section"),
            ("docs/my file.md", None),
        )

    def test_flags_repo_escape_and_file_uri(self) -> None:
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "../../outside.md"),
            ("../outside.md", "escapes_repo"),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "/../../outside.md"),
            ("../../outside.md", "escapes_repo"),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "%2e%2e/%2e%2e/outside.md"),
            ("../outside.md", "escapes_repo"),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "file:///tmp/x.md"),
            ("file:///tmp/x.md", "forbidden_url"),
        )
        self.assertEqual(
            cli.resolve_repo_target("docs/a.md", "FILE:///tmp/x.md"),
            ("FILE:///tmp/x.md", "forbidden_url"),
        )


class FindLineViolationsTest(unittest.TestCase):
    def test_allows_existing_targets_and_code_examples(self) -> None:
        text = (
            "See [Dev Guide](developer.md#setup), [Ümlaut](/docs/developer.md), and `file:///tmp/example`.\n"
            "````md\n"
            "```md\n"
            "[missing](nonexistent.md) http://go/example\n"
            "```\n"
            "````\n"
            "~~~~bash\n"
            "[tilde-nested](nonexistent.md)\n"
            "~~~~\n"
        )
        found = []
        for lineno, prose in cli.iter_prose_lines(text):
            found.extend(cli.find_line_violations("docs/a.md", lineno, prose, _EXISTING))
        self.assertEqual(found, [])

    def test_handles_empty_malformed_unclosed_fence_and_multibyte(self) -> None:
        self.assertEqual(list(cli.iter_prose_lines("")), [])
        malformed = "[unclosed(target.md) and 日本語 [broken](missing-日本語.md)\n"
        malformed_found = []
        for lineno, prose in cli.iter_prose_lines(malformed):
            malformed_found.extend(cli.find_line_violations("docs/a.md", lineno, prose, _EXISTING))
        self.assertEqual(
            [(v.kind, v.target) for v in malformed_found],
            [("missing_target", "docs/missing-日本語.md")],
        )
        unclosed_fence = "```\n[ignored](nope.md)\n"
        self.assertEqual(list(cli.iter_prose_lines(unclosed_fence)), [])

    def test_handles_nested_links_parens_spaces_refs_and_fence_edges(self) -> None:
        valid = (
            "[![alt](developer.md)](/docs/foo(1).md) "
            "[space](<my file.md> 'title') "
            "[ext](//example.com/a) "
            "[gcs](gs://my-bucket/obj)\n"
            "[ref]: /docs/developer.md\n"
        )
        valid_found = []
        for lineno, prose in cli.iter_prose_lines(valid):
            valid_found.extend(cli.find_line_violations("docs/a.md", lineno, prose, _EXISTING))
        self.assertEqual(valid_found, [])

        tricky = (
            "Use ```file:///tmp/x``` and [broken1](m1.md)\n"
            "Use ``unclosed [broken2](m2.md) and `ok`\n"
            "Use ``foo``` and [broken3](m3.md) and `bar`\n"
            "[![alt](missing_img.png)](missing_outer.md)\n"
            "[trailing](file:///tmp/x.md.)\n"
            "[badref]: ../missing_ref.md\n"
        )
        tricky_found = []
        for lineno, prose in cli.iter_prose_lines(tricky):
            tricky_found.extend(cli.find_line_violations("docs/a.md", lineno, prose, _EXISTING))
        self.assertEqual(
            [(v.line, v.kind, v.target) for v in tricky_found],
            [
                (1, "missing_target", "docs/m1.md"),
                (2, "missing_target", "docs/m2.md"),
                (3, "missing_target", "docs/m3.md"),
                (4, "missing_target", "docs/missing_img.png"),
                (4, "missing_target", "docs/missing_outer.md"),
                (5, "forbidden_url", "file:///tmp/x.md"),
                (6, "missing_target", "missing_ref.md"),
            ],
        )

    def test_flags_missing_relative_links_and_internal_urls(self) -> None:
        text = (
            "Old [link](missing.md) and [corp](https://moma.corp.google.com/foo) "
            "plus http://go/internal-doc, [proto](//go/proto-doc), "
            "https://example.com/a//go/safe, and file:///etc/passwd.\n"
        )
        found = []
        for lineno, prose in cli.iter_prose_lines(text):
            found.extend(cli.find_line_violations("docs/a.md", lineno, prose, _EXISTING))
        self.assertEqual(
            [(v.kind, v.target) for v in found],
            [
                ("missing_target", "docs/missing.md"),
                ("forbidden_url", "moma.corp.google.com"),
                ("forbidden_url", "http://go/internal-doc"),
                ("forbidden_url", "//go/proto-doc"),
                ("forbidden_url", "file:///etc/passwd"),
            ],
        )

    def test_ignores_agents_docs(self) -> None:
        self.assertFalse(cli.is_checked_doc(".agents/rules/markdown.md"))
        self.assertTrue(cli.is_checked_doc("docs/developer.md"))
        self.assertTrue(cli.is_checked_doc("README.md"))


class CheckDocLinksCliTest(unittest.TestCase):
    def test_scan_passes_and_fails_cleanly(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "docs").mkdir()
            (root / "README.md").write_text("# Root\n", encoding="utf-8")
            (root / "docs/ok.md").write_text("See [Root](../README.md).\n", encoding="utf-8")
            (root / "docs/bad.md").write_text("See [Missing](nope.md).\n", encoding="utf-8")

            with mock.patch.object(
                cli, "list_tracked_files", return_value=["README.md", "docs/ok.md"]
            ):
                res = cli.check_doc_links(argparse.Namespace(repo_root=tmp))
            self.assertEqual(res, {"docs_scanned": 2, "violations": 0})

            with mock.patch.object(
                cli, "list_tracked_files", return_value=["README.md", "docs/bad.md"]
            ):
                with self.assertRaises(HandledError) as ctx:
                    cli.check_doc_links(argparse.Namespace(repo_root=tmp))
            assert ctx.exception.details is not None
            self.assertEqual(
                ctx.exception.details["violations"],
                [
                    {
                        "doc": "docs/bad.md",
                        "line": 1,
                        "kind": "missing_target",
                        "target": "docs/nope.md",
                    }
                ],
            )

            # Working-tree deletion not yet staged is skipped gracefully; repo escape fails.
            (root / "docs/esc.md").write_text("See [Out](../../out.md).\n", encoding="utf-8")
            with mock.patch.object(
                cli,
                "list_tracked_files",
                return_value=["README.md", "docs/deleted.md", "docs/esc.md"],
            ):
                with self.assertRaises(HandledError) as esc_ctx:
                    cli.check_doc_links(argparse.Namespace(repo_root=tmp))
            assert esc_ctx.exception.details is not None
            self.assertEqual(esc_ctx.exception.details["violations"][0]["kind"], "escapes_repo")

    def test_main_outputs_json_and_exits_one_on_violation(self) -> None:
        buf = io.StringIO()
        err = HandledError("bad link", "fix it", details={"violations": []})
        with (
            mock.patch.object(sys, "argv", ["check_doc_links.py", "--repo-root", "/tmp"]),
            mock.patch.object(cli, "check_doc_links", side_effect=err),
            redirect_stderr(buf),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            cli.main()
        self.assertEqual(exit_ctx.exception.code, 1)
        self.assertEqual(json.loads(buf.getvalue())["status"], "error")


if __name__ == "__main__":
    unittest.main()
