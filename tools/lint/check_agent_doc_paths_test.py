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

"""Tests for check_agent_doc_paths."""

import argparse
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import check_agent_doc_paths as cli  # noqa: E402

_EXISTING = cli.tracked_paths(["tools/lint/check.py", "tools/common/setup_venv.py", "cmd/ui/x.ts"])


class IsAgentDocTest(unittest.TestCase):
    def test_classifies_paths(self):
        self.assertTrue(cli.is_agent_doc(".agents/skills/x/SKILL.md"))
        self.assertTrue(cli.is_agent_doc("AGENTS.md"))
        self.assertTrue(cli.is_agent_doc("internal/storage/AGENTS.md"))
        self.assertFalse(cli.is_agent_doc(".agents/skills/x/script.py"))
        self.assertFalse(cli.is_agent_doc("docs/developer.md"))


class IterCodeSegmentsTest(unittest.TestCase):
    def test_yields_inline_spans_and_fenced_lines_with_line_numbers(self):
        text = (
            "Prose tools/ignored.py and `tools/a.py` plus ``tools/b.py``.\n"
            "```bash\n"
            "python3 tools/c.py\n"
            "```\n"
            "After fence tools/ignored.py\n"
        )
        self.assertEqual(
            list(cli.iter_code_segments(text)),
            [(1, "tools/a.py"), (1, "tools/b.py"), (3, "python3 tools/c.py")],
        )

    def test_mismatched_fence_marker_does_not_close_block(self):
        text = "~~~\n```\ntools/x.py\n~~~\n"
        self.assertEqual(list(cli.iter_code_segments(text)), [(2, "```"), (3, "tools/x.py")])

    def test_nested_shorter_fence_with_info_string_does_not_close_block(self):
        text = "````md\n```bash\npython3 tools/nested.py\n```\n````\nprose tools/prose_word\n"
        self.assertEqual(
            list(cli.iter_code_segments(text)),
            [(2, "```bash"), (3, "python3 tools/nested.py"), (4, "```")],
        )

    def test_indented_fence_in_list_item(self):
        text = "1. Step:\n   ```bash\n   tools/a.py\n   ```\nafter tools/b.py\n"
        self.assertEqual(list(cli.iter_code_segments(text)), [(3, "   tools/a.py")])


class ClosesFenceTest(unittest.TestCase):
    def test_closing_rules(self):
        cases = {
            ("```", "```"): True,
            ("`````", "```"): True,
            ("```  ", "```"): True,
            ("```bash", "```"): False,
            ("```", "````"): False,
            ("~~~", "```"): False,
            ("plain", "```"): False,
        }
        for (line, opening), want in cases.items():
            with self.subTest(line=line, opening=opening):
                self.assertEqual(cli.closes_fence(line, opening), want)


class ExtractPathTokensTest(unittest.TestCase):
    def test_extracts_relative_and_dot_slash_paths(self):
        self.assertEqual(
            cli.extract_path_tokens("./tools/a.py --x=deploy/b.yaml && cd cmd/ui"),
            ["tools/a.py", "deploy/b.yaml", "cmd/ui"],
        )

    def test_stops_at_shell_redirects_and_punctuation(self):
        self.assertEqual(
            cli.extract_path_tokens("python3 tools/x.py>/dev/null (see tools/y.py), tools/z.py;"),
            ["tools/x.py", "tools/y.py", "tools/z.py"],
        )

    def test_ignores_embedded_paths_urls_and_labels(self):
        code = (
            "github.com/google/xmc/pkg/foo //tools/lint:target "
            "~/tools/x ${HOME}/tools/y internal_pkg/z mytools/q"
        )
        self.assertEqual(cli.extract_path_tokens(code), [])


class ResolveCheckedPathTest(unittest.TestCase):
    def test_strips_trailing_punctuation_and_slash(self):
        self.assertEqual(cli.resolve_checked_path("tools/lint/"), "tools/lint")
        self.assertEqual(cli.resolve_checked_path("tools/a.py."), "tools/a.py")

    def test_reduces_placeholders_to_directory_prefix(self):
        cases = {
            "tools/<subpackage>/<script>.py": "tools",
            "tools/pr_review/review_pr_*.py": "tools/pr_review",
            "cmd/...": "cmd",
            "deploy/{a,b}/main.tf": "deploy",
            "tools/lint/${NAME}.py": "tools/lint",
            "pkg/[abc]/x.go": "pkg",
        }
        for token, want in cases.items():
            with self.subTest(token=token):
                self.assertEqual(cli.resolve_checked_path(token), want)


class TrackedPathsTest(unittest.TestCase):
    def test_includes_files_and_parent_directories(self):
        self.assertEqual(
            cli.tracked_paths(["a/b/c.txt", "d.txt"]),
            frozenset({"a", "a/b", "a/b/c.txt", "d.txt"}),
        )


class FindViolationsTest(unittest.TestCase):
    def test_reports_missing_paths_with_line_numbers(self):
        text = "Run `tools/lint/check.py`.\n\n```\n./tools/venv/bin/python3 tools/missing.py\n```\n"
        violations, used = cli.find_violations("SKILL.md", text, _EXISTING, {})
        self.assertEqual(
            violations,
            [
                cli.Violation(doc="SKILL.md", line=4, path="tools/venv/bin/python3"),
                cli.Violation(doc="SKILL.md", line=4, path="tools/missing.py"),
            ],
        )
        self.assertEqual(used, set())

    def test_existing_directories_and_placeholders_pass(self):
        text = "`cmd/ui/` `tools/<x>/y.py` `tools/lint/*.py` `cmd/...`\n"
        self.assertEqual(cli.find_violations("d.md", text, _EXISTING, {}), ([], set()))

    def test_allowlist_covers_entry_and_descendants(self):
        text = "`tools/common/venv/bin/python3` `tools/common/venv`\n"
        allowlist = {"tools/common/venv": "runtime"}
        self.assertEqual(
            cli.find_violations("d.md", text, _EXISTING, allowlist),
            ([], {"tools/common/venv"}),
        )

    def test_allowlist_prefix_requires_path_boundary(self):
        violations, _ = cli.find_violations(
            "d.md", "`tools/common/venv2`\n", _EXISTING, {"tools/common/venv": "runtime"}
        )
        self.assertEqual([v.path for v in violations], ["tools/common/venv2"])


class CheckAgentDocPathsEndToEndTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        self._write_and_track("tools/real.py", "")
        self._write_and_track("docs/guide.md", "`tools/not_scanned.py`\n")

    def tearDown(self):
        self._tmp.cleanup()

    def _write_and_track(self, rel, content):
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        subprocess.run(["git", "add", rel], cwd=self.root, check=True)

    def _run(self, allowlist=None):
        with mock.patch.object(cli, "ALLOWLIST", allowlist or {}):
            return cli.check_agent_doc_paths(argparse.Namespace(repo_root=str(self.root)))

    def test_passes_with_allowlisted_runtime_path(self):
        self._write_and_track(".agents/skills/s/SKILL.md", "`tools/real.py` `tools/runtime/x`\n")
        self._write_and_track("pkg/AGENTS.md", "`./tools/real.py`\n")
        self.assertEqual(self._run({"tools/runtime": "created at runtime"}), {"docs_scanned": 2})

    def test_unused_allowlist_entry_fails(self):
        self._write_and_track(".agents/skills/s/SKILL.md", "`tools/real.py`\n")
        with self.assertRaises(HandledError) as ctx:
            self._run({"tools/runtime": "created at runtime"})
        self.assertEqual(ctx.exception.details, {"unused_allowlist": ["tools/runtime"]})

    def test_fails_with_violation_details(self):
        self._write_and_track(".agents/rules/r.md", "ok\n`tools/venv/bin/python3`\n")
        with self.assertRaises(HandledError) as ctx:
            self._run()
        self.assertEqual(
            ctx.exception.details,
            {
                "violations": [
                    {"doc": ".agents/rules/r.md", "line": 2, "path": "tools/venv/bin/python3"}
                ]
            },
        )

    def test_main_exits_with_handled_error_code(self):
        self._write_and_track(".agents/rules/r.md", "`tools/missing.py`\n")
        argv = ["check_agent_doc_paths.py", "--repo-root", str(self.root)]
        with (
            mock.patch.object(sys, "argv", argv),
            mock.patch("sys.stderr"),
            self.assertRaises(SystemExit) as ctx,
        ):
            cli.main()
        self.assertEqual(ctx.exception.code, 1)

    def test_skips_tracked_doc_deleted_from_worktree(self):
        self._write_and_track(".agents/rules/r.md", "`tools/missing.py`\n")
        (self.root / ".agents/rules/r.md").unlink()
        self.assertEqual(self._run()["docs_scanned"], 1)

    def test_non_utf8_file_name_and_doc_content_do_not_crash(self):
        (self.root / b"bad\xff.txt".decode("utf-8", "surrogateescape")).write_bytes(b"")
        subprocess.run(["git", "add", "-A"], cwd=self.root, check=True)
        self._write_and_track(".agents/rules/r.md", "")
        (self.root / ".agents/rules/r.md").write_bytes(b"\xff `tools/real.py`\n")
        self.assertEqual(self._run(), {"docs_scanned": 1})


class ListTrackedFilesTest(unittest.TestCase):
    def test_outside_git_repository_raises_handled_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(HandledError, "git ls-files failed"):
                cli.list_tracked_files(Path(tmp))

    def test_missing_git_binary_raises_handled_error(self):
        with mock.patch.object(cli.subprocess, "run", side_effect=FileNotFoundError("git")):
            with self.assertRaisesRegex(HandledError, "git ls-files failed"):
                cli.list_tracked_files(Path("."))


class RepositoryDocsTest(unittest.TestCase):
    def test_allowlist_entries_have_reasons(self):
        for path, reason in cli.ALLOWLIST.items():
            with self.subTest(path=path):
                self.assertTrue(reason.strip())


if __name__ == "__main__":
    unittest.main()
