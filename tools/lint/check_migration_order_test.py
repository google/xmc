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

"""Unit tests for tools.lint.check_migration_order."""

from __future__ import annotations

import argparse
import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.lint import check_migration_order as cli

SPANNER_DIR = "internal/storage/spanner/migrations/ess"
POSTGRES_DIR = "internal/storage/postgres/migrations/ess"


class ParseMigrationVersionTest(unittest.TestCase):
    """Tests for migration filename version parsing."""

    def test_parses_valid_sql_migration_filenames(self) -> None:
        cases = (
            (f"{SPANNER_DIR}/001_baseline.up.sql", 1),
            (f"{SPANNER_DIR}/001_baseline.down.sql", 1),
            (f"{POSTGRES_DIR}/020_work_unit_trial_fields.up.sql", 20),
            ("100_big_change.up.sql", 100),
            ("000_zero.up.sql", 0),
            ("021_café_日本語.up.sql", 21),
            ("021_bad_\ufffd.up.sql", 21),
        )
        for path, expected in cases:
            with self.subTest(path=path):
                self.assertEqual(cli.parse_migration_version(path), expected)

    def test_ignores_non_migration_filenames(self) -> None:
        ignored = (
            "",
            f"{SPANNER_DIR}/README.md",
            f"{SPANNER_DIR}/BUILD.bazel",
            f"{SPANNER_DIR}/notes.sql",
            f"{SPANNER_DIR}/v021_bad_prefix.up.sql",
            f"{SPANNER_DIR}/021_wrong_ext.txt",
            f"{SPANNER_DIR}/021no_underscore.sql",
        )
        for path in ignored:
            with self.subTest(path=path):
                self.assertIsNone(cli.parse_migration_version(path))


class FindOutOfOrderMigrationsTest(unittest.TestCase):
    """Pure logic tests covering gaps, duplicates, edits, and deletions."""

    def test_allows_strictly_higher_migration_and_tolerates_existing_gaps(self) -> None:
        base = [
            f"{POSTGRES_DIR}/013_baseline.up.sql",
            f"{POSTGRES_DIR}/015_skip_fourteen.up.sql",
            f"{POSTGRES_DIR}/020_latest.up.sql",
        ]
        current = [
            *base,
            f"{POSTGRES_DIR}/021_new_feature.up.sql",
            f"{POSTGRES_DIR}/021_new_feature.down.sql",
            f"{POSTGRES_DIR}/025_with_gap.up.sql",
        ]
        base_max, violations = cli.find_out_of_order_migrations(POSTGRES_DIR, base, current)
        self.assertEqual(base_max, 20)
        self.assertEqual(violations, [])

    def test_rejects_added_migration_equal_to_or_below_base_max(self) -> None:
        base = [
            f"{SPANNER_DIR}/013_pause.up.sql",
            f"{SPANNER_DIR}/015_fence.up.sql",
            f"{SPANNER_DIR}/020_trial_fields.up.sql",
        ]
        current = [
            f"{SPANNER_DIR}/013_pause.up.sql",
            f"{SPANNER_DIR}/014_gap_fill.up.sql",
            f"{SPANNER_DIR}/015_fence.up.sql",
            f"{SPANNER_DIR}/020_conflicting_feature.up.sql",
            f"{SPANNER_DIR}/020_conflicting_feature.down.sql",
            f"{SPANNER_DIR}/021_valid.up.sql",
        ]
        base_max, violations = cli.find_out_of_order_migrations(SPANNER_DIR, base, current)
        self.assertEqual(base_max, 20)
        self.assertEqual(
            [(v["path"], v["version"], v["base_max"]) for v in violations],
            [
                (f"{SPANNER_DIR}/014_gap_fill.up.sql", 14, 20),
                (f"{SPANNER_DIR}/020_conflicting_feature.down.sql", 20, 20),
                (f"{SPANNER_DIR}/020_conflicting_feature.up.sql", 20, 20),
            ],
        )

    def test_ignores_unchanged_modified_deleted_and_non_sql_files(self) -> None:
        base = [
            f"{SPANNER_DIR}/019_study.up.sql",
            f"{SPANNER_DIR}/020_trial.up.sql",
        ]
        # 019 kept (modified in place), 020 deleted, non-SQL README added.
        current = [
            f"{SPANNER_DIR}/019_study.up.sql",
            f"{SPANNER_DIR}/README.md",
        ]
        base_max, violations = cli.find_out_of_order_migrations(SPANNER_DIR, base, current)
        self.assertEqual(base_max, 20)
        self.assertEqual(violations, [])

    def test_handles_empty_base_directory(self) -> None:
        base_max, violations = cli.find_out_of_order_migrations(
            SPANNER_DIR,
            [],
            [f"{SPANNER_DIR}/001_baseline.up.sql"],
        )
        self.assertEqual(base_max, 0)
        self.assertEqual(violations, [])

    def test_handles_empty_base_directory_with_zero_version(self) -> None:
        base_max, violations = cli.find_out_of_order_migrations(
            SPANNER_DIR,
            [],
            [f"{SPANNER_DIR}/000_zero.up.sql"],
        )
        self.assertEqual(base_max, 0)
        self.assertEqual(violations, [])

    def test_rejects_duplicate_versions_with_different_names(self) -> None:
        base = [f"{SPANNER_DIR}/013_baseline.up.sql"]
        current = [
            *base,
            f"{SPANNER_DIR}/014_foo.up.sql",
            f"{SPANNER_DIR}/014_bar.up.sql",
        ]
        base_max, violations = cli.find_out_of_order_migrations(SPANNER_DIR, base, current)
        self.assertEqual(base_max, 13)
        self.assertEqual(
            [(v["path"], v["version"]) for v in violations],
            [
                (f"{SPANNER_DIR}/014_bar.up.sql", 14),
                (f"{SPANNER_DIR}/014_foo.up.sql", 14),
            ],
        )
        self.assertIn("Duplicate migration version 14", violations[0]["reason"])


class CheckMigrationOrderIntegrationTest(unittest.TestCase):
    """Tests for git integration, error paths, and CLI entrypoint."""

    def _make_fake_repo(self, tmpdir: str) -> Path:
        root = Path(tmpdir)
        (root / ".git").mkdir()
        (root / SPANNER_DIR).mkdir(parents=True)
        (root / POSTGRES_DIR).mkdir(parents=True)
        return root

    def test_passes_when_both_backends_add_higher_versions(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=f"{SPANNER_DIR}/020_a.up.sql\0{SPANNER_DIR}/021_b.up.sql\0",
                    stderr="",
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=f"{POSTGRES_DIR}/020_a.up.sql\0{POSTGRES_DIR}/021_b.up.sql\0",
                    stderr="",
                ),
            ]
            args = argparse.Namespace(repo_root=str(root), base_ref="origin/main", dirs=None)
            with mock.patch.object(cli, "run_command", side_effect=responses) as run_mock:
                result = cli.check_migration_order(args)

        self.assertEqual(result["violations"], 0)
        self.assertEqual(result["base_ref"], "origin/main")
        self.assertEqual(result["checked_dirs"][SPANNER_DIR], {"base_max": 20, "added_files": 1})
        self.assertEqual(result["checked_dirs"][POSTGRES_DIR], {"base_max": 20, "added_files": 1})
        expected_cmds = [
            ["git", "rev-parse", "--verify", "origin/main^{commit}"],
            [
                "git",
                "-c",
                "core.quotePath=false",
                "ls-tree",
                "-r",
                "--name-only",
                "-z",
                "origin/main",
                "--",
                SPANNER_DIR,
            ],
            ["git", "-c", "core.quotePath=false", "ls-files", "-z", "--", SPANNER_DIR],
            [
                "git",
                "-c",
                "core.quotePath=false",
                "ls-tree",
                "-r",
                "--name-only",
                "-z",
                "origin/main",
                "--",
                POSTGRES_DIR,
            ],
            ["git", "-c", "core.quotePath=false", "ls-files", "-z", "--", POSTGRES_DIR],
        ]
        self.assertEqual([call.args[0] for call in run_mock.call_args_list], expected_cmds)
        for call in run_mock.call_args_list:
            self.assertFalse(call.kwargs["log_on_error"])

    def test_fails_when_one_backend_adds_out_of_order_migration(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=f"{POSTGRES_DIR}/020_a.up.sql\0{POSTGRES_DIR}/019_late.up.sql\0",
                    stderr="",
                ),
            ]
            args = argparse.Namespace(repo_root=str(root), base_ref="origin/main", dirs=None)
            with (
                mock.patch.object(cli, "run_command", side_effect=responses),
                self.assertRaises(HandledError) as cm,
            ):
                cli.check_migration_order(args)

        self.assertIn("1 out-of-order migration", cm.exception.message)
        assert cm.exception.details is not None
        self.assertEqual(len(cm.exception.details["violations"]), 1)
        self.assertEqual(cm.exception.details["violations"][0]["dir"], POSTGRES_DIR)
        self.assertEqual(cm.exception.details["violations"][0]["version"], 19)

    def test_fails_on_invalid_base_ref(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            for bad_ref in ("", "-f", "--help"):
                with self.subTest(base_ref=bad_ref):
                    args = argparse.Namespace(repo_root=str(root), base_ref=bad_ref, dirs=None)
                    with self.assertRaises(HandledError) as cm:
                        cli.check_migration_order(args)
                    self.assertIn("Invalid base ref", cm.exception.message)

    def test_fails_on_empty_or_root_dir(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            for bad_dir in ("", "/"):
                with self.subTest(bad_dir=bad_dir):
                    args = argparse.Namespace(
                        repo_root=str(root), base_ref="origin/main", dirs=[bad_dir]
                    )
                    with self.assertRaises(HandledError) as cm:
                        cli.check_migration_order(args)
                    self.assertIn("cannot be empty", cm.exception.message)

    def test_propagates_handled_error_when_base_ref_unresolvable(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            args = argparse.Namespace(repo_root=str(root), base_ref="origin/missing", dirs=None)
            err = HandledError("rev-parse failed", "Run 'git fetch origin main'.")
            with (
                mock.patch.object(cli, "run_command", side_effect=err),
                self.assertRaises(HandledError) as cm,
            ):
                cli.check_migration_order(args)
        self.assertEqual(cm.exception, err)

    def test_fails_on_invalid_repo_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            args = argparse.Namespace(repo_root=tmpdir, base_ref="origin/main", dirs=None)
            with self.assertRaises(HandledError) as cm:
                cli.check_migration_order(args)
        self.assertIn("Invalid git repository root", cm.exception.message)

    def test_fails_fast_when_migration_dir_missing(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / ".git").mkdir()
            args = argparse.Namespace(
                repo_root=str(root),
                base_ref="origin/main",
                dirs=["internal/storage/missing/migrations/ess"],
            )
            with self.assertRaises(HandledError) as cm:
                cli.check_migration_order(args)
        self.assertIn("Migration directory not found", cm.exception.message)
        assert cm.exception.details is not None
        self.assertEqual(
            cm.exception.details["missing_dirs"],
            ["internal/storage/missing/migrations/ess"],
        )

    def test_main_emits_json_and_exits_zero_on_success(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
            ]
            stdout_buf = io.StringIO()
            with (
                mock.patch.object(cli, "run_command", side_effect=responses),
                mock.patch("sys.argv", ["check_migration_order.py", "--repo-root", str(root)]),
                mock.patch("sys.stdout", stdout_buf),
                self.assertRaises(SystemExit) as cm,
            ):
                cli.main()

        self.assertEqual(cm.exception.code, 0)
        payload = json.loads(stdout_buf.getvalue())
        self.assertEqual(payload["status"], "success")
        self.assertEqual(payload["result"]["violations"], 0)

    def test_main_emits_error_json_to_stderr_and_exits_one_on_violation(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=f"{SPANNER_DIR}/020_a.up.sql\0{SPANNER_DIR}/020_dup.up.sql\0",
                    stderr="",
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
            ]
            stderr_buf = io.StringIO()
            with (
                mock.patch.object(cli, "run_command", side_effect=responses),
                mock.patch("sys.argv", ["check_migration_order.py", "--repo-root", str(root)]),
                mock.patch("sys.stderr", stderr_buf),
                self.assertRaises(SystemExit) as cm,
            ):
                cli.main()

        self.assertEqual(cm.exception.code, 1)
        payload = json.loads(stderr_buf.getvalue())
        self.assertEqual(payload["status"], "error")
        self.assertEqual(payload["error_type"], "handled")
        self.assertEqual(len(payload["details"]["violations"]), 1)

    def test_duplicate_versions_above_base_max_reports_accurate_error(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=(
                        f"{SPANNER_DIR}/020_a.up.sql\0"
                        f"{SPANNER_DIR}/021_foo.up.sql\0"
                        f"{SPANNER_DIR}/021_bar.up.sql\0"
                    ),
                    stderr="",
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
            ]
            args = argparse.Namespace(repo_root=str(root), base_ref="origin/main", dirs=None)
            with (
                mock.patch.object(cli, "run_command", side_effect=responses),
                self.assertRaises(HandledError) as cm,
            ):
                cli.check_migration_order(args)

        self.assertIn("duplicate-version migration", cm.exception.message)
        self.assertNotIn("version <= max", cm.exception.message)
        self.assertIn("unique version number", cm.exception.instruction)

    def test_mixed_out_of_order_and_duplicate_reports_combined_error(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._make_fake_repo(tmpdir)
            responses = [
                subprocess.CompletedProcess([], 0, stdout="deadbeef\n", stderr=""),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{SPANNER_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [],
                    0,
                    stdout=(
                        f"{SPANNER_DIR}/020_a.up.sql\0"
                        f"{SPANNER_DIR}/019_old.up.sql\0"
                        f"{SPANNER_DIR}/021_foo.up.sql\0"
                        f"{SPANNER_DIR}/021_bar.up.sql\0"
                    ),
                    stderr="",
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
                subprocess.CompletedProcess(
                    [], 0, stdout=f"{POSTGRES_DIR}/020_a.up.sql\0", stderr=""
                ),
            ]
            args = argparse.Namespace(repo_root=str(root), base_ref="origin/main", dirs=None)
            with (
                mock.patch.object(cli, "run_command", side_effect=responses),
                self.assertRaises(HandledError) as cm,
            ):
                cli.check_migration_order(args)

        self.assertIn("ordering/duplicate-version", cm.exception.message)
        self.assertIn("unique version strictly above", cm.exception.instruction)


class RealGitIntegrationTest(unittest.TestCase):
    """Hermetic integration tests exercising real git subprocess invocations."""

    def _init_git_repo(self, tmpdir: str) -> Path:
        root = Path(tmpdir)
        subprocess.run(["git", "init", "-b", "main"], cwd=root, check=True, capture_output=True)
        subprocess.run(
            ["git", "config", "user.email", "test@example.com"],
            cwd=root,
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "Test User"],
            cwd=root,
            check=True,
            capture_output=True,
        )
        for mig_dir in (SPANNER_DIR, POSTGRES_DIR):
            full_dir = root / mig_dir
            full_dir.mkdir(parents=True)
            (full_dir / "020_base.up.sql").write_text("-- base\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=root, check=True, capture_output=True)
        subprocess.run(
            ["git", "commit", "-m", "initial migrations"],
            cwd=root,
            check=True,
            capture_output=True,
        )
        return root

    def test_valid_staged_addition_and_untracked_sql_exclusion(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._init_git_repo(tmpdir)
            (root / SPANNER_DIR / "021_new_feature.up.sql").write_text("-- ok\n", encoding="utf-8")
            (root / SPANNER_DIR / "010_untracked_scratch.up.sql").write_text(
                "-- untracked\n", encoding="utf-8"
            )
            subprocess.run(
                ["git", "add", f"{SPANNER_DIR}/021_new_feature.up.sql"],
                cwd=root,
                check=True,
                capture_output=True,
            )
            args = argparse.Namespace(repo_root=str(root), base_ref="main", dirs=None)
            result = cli.check_migration_order(args)

        self.assertEqual(result["violations"], 0)
        self.assertEqual(result["checked_dirs"][SPANNER_DIR], {"base_max": 20, "added_files": 1})

    def test_rejects_staged_out_of_order_migration(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._init_git_repo(tmpdir)
            (root / POSTGRES_DIR / "020_conflict.up.sql").write_text("-- bad\n", encoding="utf-8")
            subprocess.run(
                ["git", "add", f"{POSTGRES_DIR}/020_conflict.up.sql"],
                cwd=root,
                check=True,
                capture_output=True,
            )
            args = argparse.Namespace(repo_root=str(root), base_ref="main", dirs=None)
            with self.assertRaises(HandledError) as cm:
                cli.check_migration_order(args)

        self.assertIn("1 out-of-order migration", cm.exception.message)
        assert cm.exception.details is not None
        self.assertEqual(cm.exception.details["violations"][0]["kind"], "out_of_order")

    def test_unresolvable_base_ref_raises_handled_error_with_pure_json_stderr(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = self._init_git_repo(tmpdir)
            stderr_buf = io.StringIO()
            with (
                mock.patch(
                    "sys.argv",
                    [
                        "check_migration_order.py",
                        "--repo-root",
                        str(root),
                        "--base-ref",
                        "origin/does-not-exist",
                    ],
                ),
                mock.patch("sys.stderr", stderr_buf),
                self.assertRaises(SystemExit) as cm,
            ):
                cli.main()

        self.assertEqual(cm.exception.code, 1)
        payload = json.loads(stderr_buf.getvalue())
        self.assertEqual(payload["status"], "error")
        self.assertEqual(payload["error_type"], "handled")
        self.assertIn("origin/does-not-exist", payload["instruction"])


if __name__ == "__main__":
    unittest.main()
