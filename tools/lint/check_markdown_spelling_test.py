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

"""Unit tests for check_markdown_spelling."""

import argparse
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import check_markdown_spelling as spelling  # noqa: E402


class FindBritishWordsTest(unittest.TestCase):
    def test_flags_british_forms_with_suggestions(self):
        cases = {
            "The behaviour changed.": ("behaviour", "behavior"),
            "Colours are labelled.": ("Colours", "colors"),
            "We serialise it.": ("serialise", "serialize"),
            "Initialisation runs once.": ("Initialisation", "initialization"),
            "It was summarised.": ("summarised", "summarized"),
            "Results were analysed.": ("analysed", "analyzed"),
            "Labelling the pods.": ("Labelling", "labeling"),
            "A favourite flag.": ("favourite", "favorite"),
        }
        for prose, (word, fix) in cases.items():
            with self.subTest(prose=prose):
                self.assertEqual(spelling.find_british_words(prose), [word])
                self.assertEqual(spelling.suggest(word), fix)

    def test_us_words_ending_in_ise_are_not_flagged(self):
        prose = (
            "Otherwise be concise and precise; exercise the enterprise expertise, raise noise, "
            "advertise, compromise, revise, supervise, surprise, premise, promise. Emphasis on "
            "analyses, realistic specialists, organisms, customers, hours, and detours."
        )
        self.assertEqual(spelling.find_british_words(prose), [])

    def test_allowed_words_mirror_golangci_ignore_list(self):
        self.assertEqual(spelling.find_british_words("Runs cancelled, cancelling, labelled."), [])

    def test_urls_and_link_targets_are_ignored(self):
        prose = (
            "See [docs](colour/behaviour.md) and https://example.com/colour <https://x/organise>"
        )
        self.assertEqual(spelling.find_british_words(prose), [])

    def test_empty_and_multibyte_lines(self):
        self.assertEqual(spelling.find_british_words(""), [])
        self.assertEqual(spelling.find_british_words("名前 ✨ behaviour"), ["behaviour"])


class ParseAddedLinesTest(unittest.TestCase):
    def test_hunks_deletions_and_dev_null(self):
        diff = "\n".join(
            [
                "diff --git a/a.md b/a.md",
                "--- a/a.md",
                "+++ b/a.md",
                "@@ -1,0 +2,2 @@",
                "+x",
                "+y",
                "@@ -9 +10 @@",
                "-old",
                "+new",
                "@@ -20,3 +21,0 @@",
                "-gone",
                "diff --git a/b.md b/b.md",
                "--- a/b.md",
                "+++ /dev/null",
                "@@ -1 +0,0 @@",
                "-bye",
            ]
        )
        self.assertEqual(spelling.parse_added_lines(diff), {"a.md": {2, 3, 10}})

    def test_empty_diff(self):
        self.assertEqual(spelling.parse_added_lines(""), {})


class RepoIntegrationTest(unittest.TestCase):
    """Exercises the lint against a real repository with a base branch and staged edits."""

    def _git(self, *args: str) -> None:
        subprocess.run(["git", *args], cwd=self.repo, check=True, capture_output=True)

    def _write(self, rel: str, text: str) -> None:
        path = self.repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = Path(tmp.name)
        self._git("init", "-q", "-b", "main")
        self._git("config", "user.email", "t@example.com")
        self._git("config", "user.name", "t")
        self._git("config", "commit.gpgsign", "false")  # isolate from global signing config
        self._write("docs/a.md", "Existing behaviour stays.\n")
        self._git("add", ".")
        self._git("commit", "-qm", "base")
        self._git("checkout", "-qb", "feature")

    def _run(self) -> dict:
        args = argparse.Namespace(base_ref="main", repo_root=str(self.repo))
        return spelling.check_markdown_spelling(args)

    def test_only_added_prose_lines_fail(self):
        self._write(
            "docs/a.md",
            "Existing behaviour stays.\nNew colour line.\n```\ncode behaviour\n```\n"
            "Inline `behaviour` is fine.\n",
        )
        self._write("notes.txt", "behaviour in a non-markdown file\n")
        self._git("add", ".")
        with self.assertRaises(HandledError) as ctx:
            self._run()
        details = ctx.exception.details
        self.assertIsNotNone(details)
        assert details is not None  # narrows the Optional for mypy
        self.assertEqual(
            details["violations"],
            [{"path": "docs/a.md", "line": 2, "word": "colour", "suggestion": "color"}],
        )

    def test_clean_change_passes_and_unstaged_edits_are_ignored(self):
        self._write("docs/b.md", "New behavior line.\n")
        self._git("add", ".")
        self._write("docs/b.md", "New behaviour line.\n")  # unstaged; the index is clean
        self.assertEqual(self._run()["files_checked"], 1)

    def test_committed_branch_lines_are_checked(self):
        self._write(".agents/rules/x.md", "Normalise inputs.\n")
        self._git("add", ".")
        self._git("commit", "-qm", "feature")
        with self.assertRaises(HandledError):
            self._run()

    def test_missing_base_ref_falls_back_to_staged_changes(self):
        self._write("docs/c.md", "Organise it.\n")
        self._git("add", ".")
        args = argparse.Namespace(base_ref="no-such-ref", repo_root=str(self.repo))
        with self.assertRaises(HandledError):
            spelling.check_markdown_spelling(args)


if __name__ == "__main__":
    unittest.main()
