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

"""Unit tests for tools.common.changed_files."""

import subprocess
import unittest
from collections.abc import Callable, Mapping
from pathlib import Path
from unittest import mock

from tools.common.changed_files import (
    ChangedFilesError,
    list_changed_files,
    list_untracked_files,
)
from tools.common.tool_utils import HandledError

_REPO_ROOT = Path("/fake/repo")
_MERGE_BASE_CMD = ["git", "merge-base", "origin/main", "HEAD"]
_UNTRACKED_CMD = ["git", "ls-files", "--others", "--exclude-standard", "-z"]


def _diff_cmd(since: str) -> list[str]:
    """Returns the git command listing tracked files changed since the given commit."""
    return ["git", "diff", "--name-only", "--no-renames", "-z", since, "--"]


def _fake_git(
    stdout_by_subcommand: Mapping[str, str],
    returncode: int = 0,
    stderr: str = "",
) -> Callable[..., subprocess.CompletedProcess[str]]:
    """Returns a run_command double answering each git subcommand with canned output."""

    def run(cmd: list[str], **kwargs: object) -> subprocess.CompletedProcess[str]:
        del kwargs
        return subprocess.CompletedProcess(
            cmd, returncode, stdout=stdout_by_subcommand.get(cmd[1], ""), stderr=stderr
        )

    return run


def _git_call(cmd: list[str]) -> object:
    """Returns the run_command call expected for cmd: captured, unlogged, in the repo root."""
    return mock.call(cmd, check=False, capture_output=True, log_cmd=False, cwd=_REPO_ROOT)


@mock.patch("tools.common.changed_files.run_command")
class ListChangedFilesTest(unittest.TestCase):
    """Tests list_changed_files against canned git output."""

    def test_lists_tracked_changes_since_merge_base_and_untracked_files(
        self, mock_run: mock.MagicMock
    ) -> None:
        mock_run.side_effect = _fake_git(
            {
                "merge-base": "abc123\n",
                "diff": "internal/old name.go\0cmd/actuator/main.go\0",
                "ls-files": "docs/ñew.md\0cmd/ui/src/new.ts\0",
            }
        )

        files = list_changed_files("origin/main", _REPO_ROOT)

        self.assertEqual(
            files,
            [
                "cmd/actuator/main.go",
                "cmd/ui/src/new.ts",
                "docs/ñew.md",
                "internal/old name.go",
            ],
        )
        self.assertEqual(
            mock_run.call_args_list,
            [_git_call(_MERGE_BASE_CMD), _git_call(_diff_cmd("abc123")), _git_call(_UNTRACKED_CMD)],
        )

    def test_without_base_ref_lists_uncommitted_changes_against_head(
        self, mock_run: mock.MagicMock
    ) -> None:
        mock_run.side_effect = _fake_git({"diff": "go.mod\0", "ls-files": "new.go\0"})

        files = list_changed_files(None, _REPO_ROOT)

        self.assertEqual(files, ["go.mod", "new.go"])
        self.assertEqual(
            mock_run.call_args_list, [_git_call(_diff_cmd("HEAD")), _git_call(_UNTRACKED_CMD)]
        )

    def test_clean_checkout_returns_empty_list(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = _fake_git({"merge-base": "abc123\n"})

        self.assertEqual(list_changed_files("origin/main", _REPO_ROOT), [])

    def test_unknown_base_ref_raises_handled_error_with_fetch_instruction(
        self, mock_run: mock.MagicMock
    ) -> None:
        mock_run.side_effect = _fake_git(
            {}, returncode=128, stderr="fatal: Not a valid object name origin/main\n"
        )

        with self.assertRaises(ChangedFilesError) as ctx:
            list_changed_files("origin/main", _REPO_ROOT)

        self.assertIsInstance(ctx.exception, HandledError)
        self.assertEqual(
            ctx.exception.message,
            "`git merge-base origin/main HEAD` failed: fatal: Not a valid object name origin/main",
        )
        self.assertIn("git fetch origin", ctx.exception.instruction)
        self.assertIn("git fetch --unshallow", ctx.exception.instruction)
        mock_run.assert_called_once()

    def test_git_failure_without_stderr_reports_exit_code(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = _fake_git({}, returncode=1)

        with self.assertRaises(ChangedFilesError) as ctx:
            list_changed_files(None, _REPO_ROOT)

        self.assertEqual(
            ctx.exception.message,
            f"`{' '.join(_diff_cmd('HEAD'))}` failed: exit code 1",
        )

    def test_missing_git_raises_changed_files_error(self, mock_run: mock.MagicMock) -> None:
        missing_git = FileNotFoundError(2, "No such file or directory", "git")
        mock_run.side_effect = missing_git

        with self.assertRaises(ChangedFilesError) as ctx:
            list_changed_files(None, _REPO_ROOT)

        self.assertTrue(ctx.exception.message.startswith("Cannot run git: "))
        self.assertIs(ctx.exception.__cause__, missing_git)


@mock.patch("tools.common.changed_files.run_command")
class ListUntrackedFilesTest(unittest.TestCase):
    """Tests list_untracked_files against canned git output."""

    def test_lists_sorted_nul_separated_paths(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = _fake_git({"ls-files": "pkg/b c.go\0docs/ñew.md\0pkg/a.go\0"})

        files = list_untracked_files(_REPO_ROOT)

        self.assertEqual(files, ["docs/ñew.md", "pkg/a.go", "pkg/b c.go"])
        self.assertEqual(mock_run.call_args_list, [_git_call(_UNTRACKED_CMD)])

    def test_git_failure_raises_changed_files_error(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = _fake_git({}, returncode=128, stderr="fatal: not a git repository\n")

        with self.assertRaises(ChangedFilesError) as ctx:
            list_untracked_files(_REPO_ROOT)

        self.assertEqual(
            ctx.exception.message,
            f"`{' '.join(_UNTRACKED_CMD)}` failed: fatal: not a git repository",
        )


if __name__ == "__main__":
    unittest.main()
