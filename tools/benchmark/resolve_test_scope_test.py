#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for tools/resolve_test_scope.py."""

import argparse
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tools.benchmark.resolve_test_scope import (
    _correlate_rebased_parent_tip,
    _get_merge_base,
    _normalize_subject,
    apply_github_env_defaults,
    classify_modified_files,
    discover_bazel_rdeps_targets,
    find_enclosing_bazel_package,
    get_git_modified_files,
    is_non_code_file,
    main,
    resolve_effective_base_ref,
    resolve_test_scope,
    write_github_output,
)

# Isolates real-git tests from host/user git config (signing, hooks, identity).
HERMETIC_GIT_ENV: dict[str, str] = {
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_AUTHOR_NAME": "Test User",
    "GIT_AUTHOR_EMAIL": "test@example.com",
    "GIT_COMMITTER_NAME": "Test User",
    "GIT_COMMITTER_EMAIL": "test@example.com",
}


class TestResolveTestScope(unittest.TestCase):
    def test_is_non_code_file(self):
        self.assertTrue(is_non_code_file("README.md"))
        self.assertTrue(is_non_code_file("docs/architecture.md"))
        self.assertTrue(is_non_code_file("deploy/helm/templates/ui.yaml"))
        self.assertTrue(is_non_code_file(".githooks/pre-commit"))
        self.assertFalse(is_non_code_file("pkg/auth/oauth2/handlers.go"))
        self.assertFalse(is_non_code_file("cmd/ui/src/app/app.component.ts"))
        self.assertFalse(is_non_code_file("services/billing/v1/billing.go"))
        self.assertFalse(is_non_code_file("deploy/ctk_v2/verify_xmc.py"))
        self.assertFalse(is_non_code_file("deploy/ctk_v2/BUILD.bazel"))
        self.assertFalse(is_non_code_file("deploy/clickhouse/schema.sql"))
        self.assertFalse(is_non_code_file("deploy/deploy_dev_env.sh"))

    def test_classify_modified_files_inverted_default_safety(self):
        # Brand new unknown backend folder MUST trigger backend code flag!
        res = classify_modified_files(["services/new_analytics/worker.go"])
        self.assertFalse(res["has_core_infra"])
        self.assertFalse(res["has_ui"])
        self.assertTrue(res["has_backend"])

        # UI-only file
        res_ui = classify_modified_files(["cmd/ui/src/app/dashboard.component.ts"])
        self.assertFalse(res_ui["has_core_infra"])
        self.assertTrue(res_ui["has_ui"])
        self.assertFalse(res_ui["has_backend"])

        # Pure doc file
        res_doc = classify_modified_files(["README.md", "docs/guide.md"])
        self.assertFalse(res_doc["has_core_infra"])
        self.assertFalse(res_doc["has_ui"])
        self.assertFalse(res_doc["has_backend"])

    def test_classify_modified_files_cmd_ui_server_python(self):
        # Python server in cmd/ui/ triggers both UI and backend tests
        res = classify_modified_files(["cmd/ui/server.py"])
        self.assertFalse(res["has_core_infra"])
        self.assertTrue(res["has_ui"])
        self.assertTrue(res["has_backend"])

    def test_resolve_test_scope_ui_only(self):
        scope = resolve_test_scope(
            ["cmd/ui/src/app/dashboard.component.ts", "cmd/ui/package.json"],
            use_rdeps=False,
        )
        self.assertFalse(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "none")
        self.assertEqual(scope["bazel_targets"], "")

    def test_resolve_test_scope_doc_only(self):
        scope = resolve_test_scope(
            ["README.md", "docs/api.md", "deploy/terraform/main.tf"],
            use_rdeps=False,
        )
        self.assertFalse(scope["run_backend"])
        self.assertFalse(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "none")

    def test_resolve_test_scope_markdown_only_anywhere(self):
        # Any markdown file anywhere in the repo (e.g. pkg/auth/README.md, cmd/ui/DOCS.md) skips all tests
        scope = resolve_test_scope(
            ["pkg/auth/oauth2/README.md", "cmd/ui/DESIGN.md", "docs/architecture.md"],
            use_rdeps=False,
        )
        self.assertFalse(scope["run_backend"])
        self.assertFalse(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "none")
        self.assertEqual(scope["reason"], "only non-code or documentation files modified")

    def test_resolve_test_scope_new_backend_folder_inverted_safety(self):
        # Testing that adding a brand-new top-level folder defaults to running backend tests!
        scope = resolve_test_scope(["services/billing/v1/billing.go"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])
        self.assertFalse(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")
        self.assertEqual(scope["bazel_targets"], "//... -//cmd/ui/...")

    def test_resolve_test_scope_core_infra_modified(self):
        scope = resolve_test_scope(["go.mod"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")

    def test_resolve_test_scope_main_branch_push(self):
        scope = resolve_test_scope(
            ["cmd/ui/src/app/app.component.ts"],
            event_name="push",
            ref="refs/heads/main",
            use_rdeps=False,
        )
        self.assertTrue(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")

    @patch("tools.benchmark.resolve_test_scope.discover_bazel_rdeps_targets")
    def test_resolve_test_scope_rdeps_success(self, mock_rdeps):
        mock_rdeps.return_value = [
            "//pkg/auth/oauth2:oauth2_test",
            "//cmd/xid_service/server:server_test",
        ]
        scope = resolve_test_scope(["pkg/auth/oauth2/handlers.go"], use_rdeps=True)
        self.assertTrue(scope["run_backend"])
        self.assertFalse(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "rdeps")
        self.assertEqual(
            scope["bazel_targets"],
            "//pkg/auth/oauth2:oauth2_test //cmd/xid_service/server:server_test",
        )

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_get_git_modified_files_porcelain_renamed(self, mock_run):
        mock_diff = unittest.mock.Mock(returncode=1)
        mock_status = unittest.mock.Mock(
            returncode=0,
            stdout="M  pkg/auth/oauth2/handlers.go\nR  old/path.go -> new/path.go\n",
        )
        mock_run.side_effect = [mock_diff, mock_status]

        files = get_git_modified_files(base_ref="origin/main")
        self.assertIn("pkg/auth/oauth2/handlers.go", files)
        self.assertIn("new/path.go", files)
        self.assertNotIn("old/path.go -> new/path.go", files)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_discover_bazel_rdeps_targets_parsing_and_filtering(self, mock_run):
        mock_run.return_value = unittest.mock.Mock(
            returncode=0,
            stdout=(
                "//pkg/auth/oauth2:oauth2_test\n"
                "//cmd/xid_service/server:server_test\n"
                "//cmd/ui:server_test\n"
                "//cmd/ui:ui_unit_tests\n"
                "//test/local/ui:server_local_test\n"
            ),
        )
        targets = discover_bazel_rdeps_targets(["cmd/ui/server.py", "pkg/auth/oauth2/handlers.go"])
        self.assertIn("//pkg/auth/oauth2:oauth2_test", targets)
        self.assertIn("//cmd/xid_service/server:server_test", targets)
        self.assertIn("//cmd/ui:server_test", targets)
        self.assertIn("//test/local/ui:server_local_test", targets)
        self.assertNotIn("//cmd/ui:ui_unit_tests", targets)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_discover_bazel_rdeps_targets_exception_safety(self, mock_run):
        mock_run.side_effect = Exception("Bazel timeout")
        targets = discover_bazel_rdeps_targets(["pkg/auth/oauth2/handlers.go"])
        self.assertEqual(targets, [])

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_discover_bazel_rdeps_targets_nonzero_exit_code(self, mock_run):
        mock_run.return_value = unittest.mock.Mock(
            returncode=1,
            stdout="",
            stderr="ERROR: Failed to run bazel query",
        )
        targets = discover_bazel_rdeps_targets(["pkg/auth/oauth2/handlers.go"])
        self.assertEqual(targets, [])

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_test_scope_rdeps_failure_falls_back_to_all(self, mock_run):
        mock_run.return_value = unittest.mock.Mock(
            returncode=1,
            stdout="",
            stderr="ERROR: query failed",
        )
        scope = resolve_test_scope(["pkg/auth/oauth2/handlers.go"], use_rdeps=True)
        self.assertTrue(scope["run_backend"])
        self.assertEqual(scope["backend_mode"], "all")
        self.assertEqual(scope["bazel_targets"], "//... -//cmd/ui/...")
        self.assertEqual(scope["reason"], "fallback to full backend suite for modified files")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_test_scope_rdeps_exception_falls_back_to_all(self, mock_run):
        mock_run.side_effect = FileNotFoundError("No such file or directory: 'bazel'")
        scope = resolve_test_scope(["pkg/auth/oauth2/handlers.go"], use_rdeps=True)
        self.assertTrue(scope["run_backend"])
        self.assertEqual(scope["backend_mode"], "all")
        self.assertEqual(scope["bazel_targets"], "//... -//cmd/ui/...")
        self.assertEqual(scope["reason"], "fallback to full backend suite for modified files")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_get_git_modified_files_strips_quotes(self, mock_run):
        mock_diff = unittest.mock.Mock(
            returncode=0,
            stdout='"pkg/auth/with space.go"\n"cmd/ui/test file.ts"\n',
        )
        mock_run.return_value = mock_diff
        files = get_git_modified_files(base_ref="origin/main")
        self.assertEqual(files, ["pkg/auth/with space.go", "cmd/ui/test file.ts"])

    def test_txt_fixture_not_non_code(self):
        self.assertFalse(is_non_code_file("pkg/auth/testdata/expected_output.txt"))
        scope = resolve_test_scope(["pkg/auth/testdata/expected_output.txt"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])

    def test_write_github_output(self):
        with tempfile.NamedTemporaryFile("w+", delete=False) as tmp:
            tmp_path = tmp.name

        try:
            sample_result = {
                "run_backend": True,
                "run_ui": False,
                "backend_mode": "rdeps",
                "bazel_targets": "//pkg/auth/oauth2:oauth2_test",
                "reason": "discovered 1 affected target",
            }
            write_github_output(tmp_path, sample_result)

            content = Path(tmp_path).read_text(encoding="utf-8")
            self.assertIn("run_backend=true\n", content)
            self.assertIn("run_ui=false\n", content)
            self.assertIn("backend_mode=rdeps\n", content)
            self.assertIn("bazel_targets=//pkg/auth/oauth2:oauth2_test\n", content)
        finally:
            if os.path.exists(tmp_path):
                os.remove(tmp_path)

    def test_find_enclosing_bazel_package(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            pkg_a = base / "internal" / "storage" / "spanner" / "migrations"
            pkg_a.mkdir(parents=True)
            (pkg_a / "BUILD.bazel").touch()
            nested_sql_dir = pkg_a / "ess"
            nested_sql_dir.mkdir()
            sql_file = nested_sql_dir / "001_baseline.up.sql"
            sql_file.touch()

            # Nested SQL file in subdirectory without BUILD should resolve to parent package
            pkg = find_enclosing_bazel_package(
                "internal/storage/spanner/migrations/ess/001_baseline.up.sql", cwd=base
            )
            self.assertEqual(pkg, "//internal/storage/spanner/migrations")

            # File directly in package
            pkg_direct = find_enclosing_bazel_package(
                "internal/storage/spanner/migrations/runner.go", cwd=base
            )
            self.assertEqual(pkg_direct, "//internal/storage/spanner/migrations")

            # File at root
            pkg_root = find_enclosing_bazel_package("compat.bzl", cwd=base)
            self.assertEqual(pkg_root, "//")

    def test_find_enclosing_bazel_package_robustness(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            pkg = base / "pkg" / "auth"
            pkg.mkdir(parents=True)
            (pkg / "BUILD.bazel").touch()

            # Relative path with '..'
            res = find_enclosing_bazel_package("pkg/foo/../auth/handler.go", cwd=base)
            self.assertEqual(res, "//pkg/auth")

            # Absolute path inside base
            abs_file = (pkg / "handler.go").resolve()
            res_abs = find_enclosing_bazel_package(str(abs_file), cwd=base)
            self.assertEqual(res_abs, "//pkg/auth")

            # Absolute path outside base
            with tempfile.TemporaryDirectory() as outside_dir:
                outside_file = Path(outside_dir) / "other.go"
                res_outside = find_enclosing_bazel_package(str(outside_file), cwd=base)
                self.assertIsNone(res_outside)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_discover_bazel_rdeps_targets_with_migration_sql(self, mock_run):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            mig_dir = base / "internal" / "storage" / "spanner" / "migrations"
            mig_dir.mkdir(parents=True)
            (mig_dir / "BUILD.bazel").touch()
            nested_sql_dir = mig_dir / "ess"
            nested_sql_dir.mkdir()
            (nested_sql_dir / "001_baseline.up.sql").touch()

            mock_run.return_value = unittest.mock.Mock(
                returncode=0,
                stdout="//internal/storage/spanner/migrations:migrations_test\n//cmd/xid_service/server:server_test\n",
            )
            targets = discover_bazel_rdeps_targets(
                ["internal/storage/spanner/migrations/ess/001_baseline.up.sql"],
                cwd=base,
            )
            self.assertIn("//internal/storage/spanner/migrations:migrations_test", targets)
            self.assertIn("//cmd/xid_service/server:server_test", targets)
            # Verify the query argument used the enclosing package
            call_args = mock_run.call_args[0][0]
            query_str = call_args[-1]
            self.assertIn("//internal/storage/spanner/migrations:all", query_str)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_test_scope_migration_sql(self, mock_run):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            mig_dir = base / "internal" / "storage" / "spanner" / "migrations"
            mig_dir.mkdir(parents=True)
            (mig_dir / "BUILD.bazel").touch()
            nested_sql_dir = mig_dir / "ess"
            nested_sql_dir.mkdir()
            (nested_sql_dir / "001_baseline.up.sql").touch()

            mock_run.return_value = unittest.mock.Mock(
                returncode=0,
                stdout="//internal/storage/spanner/migrations:migrations_test\n",
            )
            scope = resolve_test_scope(
                ["internal/storage/spanner/migrations/ess/001_baseline.up.sql"],
                use_rdeps=True,
                cwd=base,
            )
            self.assertTrue(scope["run_backend"])
            self.assertFalse(scope["run_ui"])
            self.assertEqual(scope["backend_mode"], "rdeps")
            self.assertIn(
                "//internal/storage/spanner/migrations:migrations_test",
                str(scope["bazel_targets"]),
            )

    def test_resolve_test_scope_core_infra_compat_bzl(self):
        scope = resolve_test_scope(["compat.bzl"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")

    @patch("tools.benchmark.resolve_test_scope.discover_bazel_rdeps_targets")
    def test_resolve_test_scope_deploy_code_file(self, mock_rdeps):
        mock_rdeps.return_value = ["//deploy/ctk_v2:verify_xmc_test"]
        scope = resolve_test_scope(["deploy/ctk_v2/verify_xmc.py"], use_rdeps=True)
        self.assertTrue(scope["run_backend"])
        self.assertFalse(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "rdeps")
        self.assertEqual(scope["bazel_targets"], "//deploy/ctk_v2:verify_xmc_test")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_get_merge_base(self, mock_run):
        mock_run.return_value = unittest.mock.Mock(returncode=0, stdout="abc1234\n")
        self.assertEqual(_get_merge_base("origin/main", "HEAD"), "abc1234")

        mock_run.return_value = unittest.mock.Mock(returncode=1, stdout="")
        self.assertIsNone(_get_merge_base("bad-ref", "HEAD"))

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_fast_path_and_single_candidate(self, mock_run):
        # Fast-path: origin/main without base_branch does not invoke git
        self.assertEqual(resolve_effective_base_ref("origin/main", "HEAD"), "origin/main")
        mock_run.assert_not_called()

        # Single valid merge-base candidate returns directly without --independent
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=0, stdout="mb111\n"),
            unittest.mock.Mock(returncode=0, stdout="mb111\n"),
        ]
        self.assertEqual(resolve_effective_base_ref("sha-base", "sha-head"), "mb111")
        self.assertEqual(mock_run.call_count, 2)

        # No valid merge-bases falls back to original base_ref
        mock_run.reset_mock()
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=1, stdout=""),
            unittest.mock.Mock(returncode=1, stdout=""),
        ]
        self.assertEqual(resolve_effective_base_ref("sha-base", "sha-head"), "sha-base")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_stacked_pr_picks_closest_ancestor(self, mock_run):
        # Simulate stacked PR rebased onto newer origin/main while origin/parent-branch is not fetched
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=1, stdout=""),  # merge-base(origin/parent-branch, head)
            unittest.mock.Mock(returncode=0, stdout="old_main_sha\n"),  # merge-base(base_ref, head)
            unittest.mock.Mock(
                returncode=0, stdout="new_main_sha\n"
            ),  # merge-base(origin/main, head)
            unittest.mock.Mock(returncode=0, stdout="new_main_sha\n"),  # merge-base --independent
            unittest.mock.Mock(returncode=0, stdout=""),  # _index_parent_commits parent_log
        ]
        effective = resolve_effective_base_ref(
            base_ref="stale_parent_sha",
            head_ref="rebased_head_sha",
            base_branch="feat/parent-branch",
        )
        self.assertEqual(effective, "new_main_sha")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_commit_moved_down_stack_prefers_origin_branch(
        self, mock_run
    ):
        # When feat/parent is rewound from A2 to A1 and A2 moves into feat/child,
        # origin/feat/parent (A1) is an ancestor of stale webhook PR_BASE_SHA (A2),
        # so mb_base (A2) is suppressed in favor of mb_branch (A1).
        mock_run.side_effect = [
            unittest.mock.Mock(
                returncode=0, stdout="sha_a1\n"
            ),  # merge-base(origin/feat/parent, head)
            unittest.mock.Mock(returncode=0, stdout="sha_a2\n"),  # merge-base(base_ref, head)
            unittest.mock.Mock(
                returncode=0, stdout=""
            ),  # merge-base --is-ancestor origin/feat/parent stale_sha_a2 (True)
            unittest.mock.Mock(returncode=0, stdout="sha_m1\n"),  # merge-base(origin/main, head)
            unittest.mock.Mock(returncode=0, stdout="sha_a1\n"),  # merge-base --independent
        ]
        effective = resolve_effective_base_ref(
            base_ref="stale_sha_a2",
            head_ref="child_head_sha",
            base_branch="feat/parent",
        )
        self.assertEqual(effective, "sha_a1")
        self.assertEqual(mock_run.call_count, 5)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_parent_rebased_before_child_keeps_mb_base(self, mock_run):
        # When origin/feat/parent is amended/rebased before feat/child is rebased,
        # mb_branch drops to sha_m1 while mb_base is still sha_a1 (the direct parent).
        # Because origin/feat/parent is NOT an ancestor of base_ref, mb_base is kept
        # and selected by --independent.
        mock_run.side_effect = [
            unittest.mock.Mock(
                returncode=0, stdout="sha_m1\n"
            ),  # merge-base(origin/feat/parent, head)
            unittest.mock.Mock(returncode=0, stdout="sha_a1\n"),  # merge-base(base_ref, head)
            unittest.mock.Mock(
                returncode=1, stdout=""
            ),  # merge-base --is-ancestor origin/feat/parent sha_a1 (False)
            unittest.mock.Mock(returncode=0, stdout="sha_m1\n"),  # merge-base(origin/main, head)
            unittest.mock.Mock(returncode=0, stdout="sha_a1\n"),  # merge-base --independent
        ]
        effective = resolve_effective_base_ref(
            base_ref="sha_a1",
            head_ref="child_head_sha",
            base_branch="feat/parent",
        )
        self.assertEqual(effective, "sha_a1")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_excludes_head_ref(self, mock_run):
        # If head_ref is already merged into origin/main, mb(origin/main, head) == head_ref
        # and must be excluded so we do not diff head_ref...head_ref.
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=0, stdout="base_mb_sha\n"),  # merge-base(base_ref, head)
            unittest.mock.Mock(
                returncode=0, stdout="pr_head_sha\n"
            ),  # merge-base(origin/main, head)
        ]
        effective = resolve_effective_base_ref(
            base_ref="pr_base_sha",
            head_ref="pr_head_sha",
        )
        self.assertEqual(effective, "base_mb_sha")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_resolve_effective_base_ref_independent_fallback(self, mock_run):
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=0, stdout="mb_first\n"),
            unittest.mock.Mock(returncode=0, stdout="mb_second\n"),
            unittest.mock.Mock(returncode=1, stdout=""),  # --independent fails
        ]
        effective = resolve_effective_base_ref("sha-base", "sha-head")
        self.assertEqual(effective, "mb_first")

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_get_git_modified_files_with_base_branch_and_head(self, mock_run):
        mock_run.side_effect = [
            unittest.mock.Mock(
                returncode=0, stdout="old_mb\n"
            ),  # merge-base(origin/feat/stack-parent, head)
            unittest.mock.Mock(returncode=0, stdout="old_mb\n"),  # merge-base(base_ref, head)
            unittest.mock.Mock(
                returncode=0, stdout="closest_mb\n"
            ),  # merge-base(origin/main, head)
            unittest.mock.Mock(returncode=0, stdout="closest_mb\n"),  # merge-base --independent
            unittest.mock.Mock(returncode=0, stdout=""),  # _index_parent_commits parent_log
            unittest.mock.Mock(returncode=0, stdout="pkg/components/service.go\n"),  # git diff
        ]
        files = get_git_modified_files(
            base_ref="stale_base",
            head_ref="pr_head_sha",
            base_branch="feat/stack-parent",
        )
        self.assertEqual(files, ["pkg/components/service.go"])
        diff_cmd = mock_run.call_args[0][0]
        self.assertEqual(diff_cmd, ["git", "diff", "--name-only", "closest_mb...pr_head_sha"])

    def test_normalize_subject_strips_squash_pr_suffix(self):
        self.assertEqual(
            _normalize_subject("feat(ui): add layer-1 button (#990)"),
            "feat(ui): add layer-1 button",
        )
        self.assertEqual(
            _normalize_subject("fix(api): handle (edge case) (#42)\n"),
            "fix(api): handle (edge case)",
        )
        self.assertEqual(
            _normalize_subject("feat(core): keep non-pr parens (v2)"),
            "feat(core): keep non-pr parens (v2)",
        )

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_correlate_rebased_parent_tip_matches_patch_id_and_bounded_subject(self, mock_run):
        # 1. Stacked branch with allow_subject_fallback=True:
        # Parent has 1 commit ("wip"); child has 2 "wip" commits + 1 final commit.
        # 1-to-1 Counter consumption ensures only the first "wip" commit is consumed!
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=0, stdout="sha_p1 wip\n"),  # parent log
            unittest.mock.Mock(returncode=0, stdout="diff_p1\n"),  # parent log -p
            unittest.mock.Mock(returncode=0, stdout="pid_parent sha_p1\n"),  # parent patch-id
            unittest.mock.Mock(
                returncode=0,
                stdout=(
                    "sha_p1_amended wip\n"
                    "sha_child_wip wip\n"
                    "sha_child_final feat(api): child commit\n"
                ),
            ),  # head log
            unittest.mock.Mock(returncode=0, stdout="diff_head\n"),  # head log -p
            unittest.mock.Mock(
                returncode=0,
                stdout="pid_amended sha_p1_amended\npid_child1 sha_child_wip\n",
            ),  # head patch-id
            unittest.mock.Mock(returncode=0, stdout="diff_cum\n"),  # cumulative git diff
            unittest.mock.Mock(returncode=0, stdout="pid_cum -\n"),  # cumulative patch-id
        ]
        correlated = _correlate_rebased_parent_tip(
            parent_base="sha_m1",
            target_ref="origin/feat/layer-1",
            candidate_base="sha_m2",
            head_ref="sha_child_final",
            allow_subject_fallback=True,
        )
        self.assertEqual(correlated, "sha_p1_amended")

        # 2. Non-stacked branch (allow_subject_fallback=False):
        # Subject collision with different patch-id MUST NOT advance candidate_base!
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=0, stdout="sha_main fix: lint\n"),  # parent log
            unittest.mock.Mock(returncode=0, stdout="diff_main\n"),  # parent log -p
            unittest.mock.Mock(returncode=0, stdout="pid_main sha_main\n"),  # parent patch-id
            unittest.mock.Mock(
                returncode=0,
                stdout="sha_pr1 fix: lint\nsha_pr2 feat: second commit\n",
            ),  # head log
            unittest.mock.Mock(returncode=0, stdout="diff_pr1\n"),  # head log -p
            unittest.mock.Mock(returncode=0, stdout="pid_pr1 sha_pr1\n"),  # head patch-id
            unittest.mock.Mock(returncode=0, stdout="diff_cum\n"),  # cumulative git diff
            unittest.mock.Mock(returncode=0, stdout="pid_pr1 -\n"),  # cumulative patch-id
        ]
        self.assertEqual(
            _correlate_rebased_parent_tip(
                "sha_m1",
                "origin/main",
                "sha_m1",
                "sha_pr2",
                allow_subject_fallback=False,
            ),
            "sha_m1",
        )

    @patch.dict(os.environ, HERMETIC_GIT_ENV)
    def test_gh_stack_shenanigans_real_git_repo(self):
        """Verifies all gh-stack topologies and review edge cases in a real Git repository."""
        with tempfile.TemporaryDirectory() as tmpdir:
            repo = Path(tmpdir)

            def git(*args: str) -> str:
                res = subprocess.run(
                    ["git", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args],
                    cwd=repo,
                    capture_output=True,
                    text=True,
                    check=True,
                )
                return res.stdout.strip()

            def commit_file(rel_path: str, content: str, msg: str) -> str:
                full = repo / rel_path
                full.parent.mkdir(parents=True, exist_ok=True)
                full.write_text(content, encoding="utf-8")
                git("add", rel_path)
                git("commit", "-m", msg)
                return git("rev-parse", "HEAD")

            git("init", "-b", "main")
            git("config", "user.email", "test@example.com")
            git("config", "user.name", "Test User")

            # Base commit M1 on main
            m1 = commit_file("README.md", "# XMC\n", "chore: initial commit")
            git("update-ref", "refs/remotes/origin/main", m1)

            # Layer 1 (UI change): feat/layer-1 -> A1
            git("checkout", "-b", "feat/layer-1", m1)
            a1 = commit_file("cmd/ui/src/btn.ts", "export const v = 1;\n", "feat(ui): add button")
            git("update-ref", "refs/remotes/origin/feat/layer-1", a1)

            # Layer 2 (Backend change): feat/layer-2 -> B1 (stacked on A1)
            git("checkout", "-b", "feat/layer-2", a1)
            b1 = commit_file("pkg/api/svc.go", "package api\n", "feat(api): add service")

            # Advance origin/main with an unrelated backend change M2
            git("checkout", "main")
            m2 = commit_file("pkg/core/other.go", "package core\n", "feat(core): unrelated main")
            git("update-ref", "refs/remotes/origin/main", m2)

            old_cwd = os.getcwd()
            os.chdir(repo)
            try:
                # Shenanigan 1: Synthetic merge commit (refs/pull/<num>/merge) vs PR_HEAD_SHA
                git("checkout", "-B", "pr-merge-sim", b1)
                git("merge", "--no-ff", "-m", "Merge b1 into main", m2)
                files_pr_head = get_git_modified_files(
                    base_ref=a1,
                    head_ref=b1,
                    base_branch="feat/layer-1",
                )
                self.assertEqual(files_pr_head, ["pkg/api/svc.go"])
                scope_s1 = resolve_test_scope(files_pr_head, use_rdeps=False)
                self.assertTrue(scope_s1["run_backend"])
                self.assertFalse(scope_s1["run_ui"])

                # Shenanigan 2a: gh stack sync / rebase onto M2 where webhook PR_BASE_SHA is stale (A1)
                git("checkout", "-B", "feat/layer-1", m2)
                a1_rebased = commit_file(
                    "cmd/ui/src/btn.ts", "export const v = 1;\n", "feat(ui): add button"
                )
                git("checkout", "-B", "feat/layer-2", a1_rebased)
                b1_rebased = commit_file(
                    "pkg/api/svc.go", "package api\n", "feat(api): add service"
                )
                git("update-ref", "refs/remotes/origin/feat/layer-1", a1_rebased)
                files_s2a = get_git_modified_files(
                    base_ref=a1,
                    head_ref=b1_rebased,
                    base_branch="feat/layer-1",
                )
                self.assertEqual(files_s2a, ["pkg/api/svc.go"])

                # Shenanigan 2b (Comment #4132468885): Parent branch amended with a renamed title
                # BEFORE child branch (still at B1 on A1) is rebased.
                # mb_base (A1) is preserved and chosen over mb_branch (M1)!
                git("checkout", "-B", "feat/layer-1-amended", m1)
                a1_renamed = commit_file(
                    "cmd/ui/src/btn.ts",
                    "export const v = 2;\n",
                    "feat(ui): completely different parent title",
                )
                git("update-ref", "refs/remotes/origin/feat/layer-1", a1_renamed)
                files_s2b = get_git_modified_files(
                    base_ref=a1,
                    head_ref=b1,
                    base_branch="feat/layer-1",
                )
                self.assertEqual(files_s2b, ["pkg/api/svc.go"])

                # Shenanigan 3: Non-atomic gh stack submit / parent amended before origin/feat/layer-1 updates
                git("update-ref", "refs/remotes/origin/feat/layer-1", a1)
                files_s3 = get_git_modified_files(
                    base_ref=a1,
                    head_ref=b1_rebased,
                    base_branch="feat/layer-1",
                )
                self.assertEqual(files_s3, ["pkg/api/svc.go"])
                scope_s3 = resolve_test_scope(files_s3, use_rdeps=False)
                self.assertTrue(scope_s3["run_backend"])
                self.assertFalse(scope_s3["run_ui"])

                # Shenanigan 4a: 2-commit parent PR (A1 -> A2_ui) squash-merged into main as a single
                # commit M3_squash, and child PR (M1 -> A1 -> A2_ui -> B_multi) retargeted to main
                # before child rebase! Cumulative prefix patch-id correlates M1..A2_ui with M3_squash.
                git("checkout", "-B", "feat/layer-1-multi", a1)
                a2_ui = commit_file(
                    "cmd/ui/src/icon.ts", "export const icon = 'x';\n", "feat(ui): add icon"
                )
                git("checkout", "-B", "feat/layer-2-multi", a2_ui)
                b_multi = commit_file("pkg/api/svc.go", "package api\n", "feat(api): add service")

                git("checkout", "main")
                (repo / "cmd/ui/src").mkdir(parents=True, exist_ok=True)
                (repo / "cmd/ui/src/btn.ts").write_text("export const v = 1;\n", encoding="utf-8")
                (repo / "cmd/ui/src/icon.ts").write_text(
                    "export const icon = 'x';\n", encoding="utf-8"
                )
                git("add", "cmd/ui/src/btn.ts", "cmd/ui/src/icon.ts")
                git("commit", "-m", "feat(ui): add button and icon (#990)")
                m3_squash = git("rev-parse", "HEAD")
                git("update-ref", "refs/remotes/origin/main", m3_squash)

                files_s4a = get_git_modified_files(
                    base_ref=m3_squash,
                    head_ref=b_multi,
                    base_branch="main",
                )
                self.assertEqual(files_s4a, ["pkg/api/svc.go"])

                # Shenanigan 4b (Comment #4132468872): Standard multi-commit PR targeting main where
                # commit 1 happens to have the exact same subject as M2 on main ("feat(core): unrelated main")
                # MUST NOT drop commit 1's files because patch-id differs and allow_subject_fallback=False!
                git("checkout", "-B", "feat/standard-pr", m1)
                c1_colliding = commit_file(
                    "cmd/ui/src/colliding.ts",
                    "export const c = 1;\n",
                    "feat(core): unrelated main",
                )
                c2_standard = commit_file(
                    "pkg/api/standard.go", "package api\n", "feat(api): standard endpoint"
                )
                files_s4b = get_git_modified_files(
                    base_ref=m3_squash,
                    head_ref=c2_standard,
                    base_branch="main",
                )
                self.assertEqual(
                    sorted(files_s4b),
                    ["cmd/ui/src/colliding.ts", "pkg/api/standard.go"],
                )
                self.assertNotEqual(c1_colliding, c2_standard)

                # Shenanigan 5: Commit moved/rewound from parent layer down into child layer
                git("checkout", "-B", "feat/layer-1-ext", a1)
                a2 = commit_file(
                    "pkg/moved/moved.go", "package moved\n", "feat(moved): move to layer 2"
                )
                git("checkout", "-B", "feat/layer-2-ext", a2)
                b_ext = commit_file("pkg/api/svc.go", "package api\n", "feat(api): add service")
                git("update-ref", "refs/remotes/origin/feat/layer-1", a1)
                files_s5 = get_git_modified_files(
                    base_ref=a2,
                    head_ref=b_ext,
                    base_branch="feat/layer-1",
                )
                self.assertEqual(sorted(files_s5), ["pkg/api/svc.go", "pkg/moved/moved.go"])
            finally:
                os.chdir(old_cwd)

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_get_git_modified_files_retries_head_on_missing_pr_head_sha(self, mock_run):
        # When missing_sha fails merge-base and git diff, fallback re-resolves effective_base
        # against "HEAD" before running git diff resolved_head_base...HEAD (Comment #4132468903).
        mock_run.side_effect = [
            unittest.mock.Mock(returncode=1, stdout=""),  # merge-base(base_ref, missing_sha)
            unittest.mock.Mock(returncode=1, stdout=""),  # merge-base(origin/main, missing_sha)
            unittest.mock.Mock(returncode=128, stdout=""),  # git diff base_sha...missing_sha fails
            unittest.mock.Mock(
                returncode=0, stdout="resolved_head_mb\n"
            ),  # merge-base(base_ref, HEAD)
            unittest.mock.Mock(
                returncode=0, stdout="resolved_head_mb\n"
            ),  # merge-base(origin/main, HEAD)
            unittest.mock.Mock(
                returncode=0, stdout="pkg/auth/handler.go\n"
            ),  # git diff resolved_head_mb...HEAD
        ]
        files = get_git_modified_files(base_ref="base_sha", head_ref="missing_sha")
        self.assertEqual(files, ["pkg/auth/handler.go"])
        diff_cmd = mock_run.call_args[0][0]
        self.assertEqual(diff_cmd, ["git", "diff", "--name-only", "resolved_head_mb...HEAD"])

    def test_resolve_test_scope_empty_files_and_workflow_dispatch_fail_open(self):
        empty_scope = resolve_test_scope([], use_rdeps=False)
        self.assertTrue(empty_scope["run_backend"])
        self.assertTrue(empty_scope["run_ui"])
        self.assertEqual(empty_scope["backend_mode"], "all")

        dispatch_scope = resolve_test_scope(
            ["README.md"],
            event_name="workflow_dispatch",
            ref="refs/heads/main",
            use_rdeps=False,
        )
        self.assertTrue(dispatch_scope["run_backend"])
        self.assertTrue(dispatch_scope["run_ui"])
        self.assertEqual(dispatch_scope["backend_mode"], "all")

    def test_classify_ci_workflow_and_ui_action_files(self):
        ui_action = classify_modified_files([".github/actions/run-ui-tests/action.yml"])
        self.assertFalse(ui_action["has_core_infra"])
        self.assertTrue(ui_action["has_ui"])
        self.assertFalse(ui_action["has_backend"])

        unit_wf = classify_modified_files([".github/workflows/unit-tests.yml"])
        self.assertTrue(unit_wf["has_core_infra"])
        self.assertTrue(unit_wf["has_ui"])
        self.assertTrue(unit_wf["has_backend"])

    @patch("tools.benchmark.resolve_test_scope.subprocess.run")
    def test_discover_bazel_rdeps_targets_includes_non_code_testdata_package(self, mock_run):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            pkg_auth = base / "pkg" / "auth"
            (pkg_auth / "testdata").mkdir(parents=True)
            (pkg_auth / "BUILD.bazel").touch()
            pkg_other = base / "pkg" / "other"
            pkg_other.mkdir(parents=True)
            (pkg_other / "BUILD.bazel").touch()

            mock_run.return_value = unittest.mock.Mock(
                returncode=0,
                stdout="//pkg/auth:auth_test\n//pkg/other:other_test\n",
            )
            targets = discover_bazel_rdeps_targets(
                ["pkg/auth/testdata/expected.txt", "pkg/other/foo.go"],
                cwd=base,
            )
            self.assertEqual(targets, ["//pkg/auth:auth_test", "//pkg/other:other_test"])
            query_str = mock_run.call_args[0][0][-1]
            self.assertIn("//pkg/auth:all", query_str)
            self.assertIn("//pkg/other:all", query_str)

            # Unpackaged root non-code file triggers empty rdeps fallback -> backend_mode="all"
            targets_root = discover_bazel_rdeps_targets(
                ["unknown_root_data.yaml", "pkg/other/foo.go"],
                cwd=base,
            )
            self.assertEqual(targets_root, [])

    @patch.dict(os.environ, {"PATH": os.environ.get("PATH", "")}, clear=True)
    @patch("tools.benchmark.resolve_test_scope.get_git_modified_files")
    def test_main_passes_base_branch_and_head(self, mock_get_files):
        mock_get_files.return_value = ["README.md"]
        with patch(
            "sys.argv",
            [
                "resolve_test_scope.py",
                "--base",
                "base_sha",
                "--base-branch",
                "feat/parent",
                "--head",
                "head_sha",
                "--no-rdeps",
            ],
        ):
            main()
        mock_get_files.assert_called_once_with(
            base_ref="base_sha",
            head_ref="head_sha",
            base_branch="feat/parent",
        )


def _empty_args() -> argparse.Namespace:
    return argparse.Namespace(
        base=None, head=None, base_branch=None, event=None, ref=None, github_output=None
    )


class TestApplyGithubEnvDefaults(unittest.TestCase):
    def _write_event(self, tmp: str, content: str) -> str:
        path = Path(tmp) / "event.json"
        path.write_text(content, encoding="utf-8")
        return str(path)

    def test_pull_request_prefers_payload_head_over_github_sha(self):
        with tempfile.TemporaryDirectory() as tmp:
            event = json.dumps({"pull_request": {"base": {"sha": "b1"}, "head": {"sha": "h1"}}})
            env = {
                "GITHUB_EVENT_NAME": "pull_request",
                "GITHUB_EVENT_PATH": self._write_event(tmp, event),
                "GITHUB_SHA": "synthetic_merge",
                "GITHUB_BASE_REF": "feat/parent",
                "GITHUB_REF": "refs/pull/1/merge",
                "GITHUB_OUTPUT": "/tmp/out",
            }
            args = apply_github_env_defaults(_empty_args(), env)
        self.assertEqual(
            (args.base, args.head, args.base_branch, args.event, args.ref, args.github_output),
            ("b1", "h1", "feat/parent", "pull_request", "refs/pull/1/merge", "/tmp/out"),
        )

    def test_merge_group_uses_group_shas(self):
        with tempfile.TemporaryDirectory() as tmp:
            event = json.dumps({"merge_group": {"base_sha": "mb", "head_sha": "mh"}})
            env = {
                "GITHUB_EVENT_NAME": "merge_group",
                "GITHUB_EVENT_PATH": self._write_event(tmp, event),
            }
            args = apply_github_env_defaults(_empty_args(), env)
        self.assertEqual((args.base, args.head, args.event), ("mb", "mh", "merge_group"))

    def test_push_falls_back_to_origin_main_and_github_sha(self):
        with tempfile.TemporaryDirectory() as tmp:
            env = {
                "GITHUB_EVENT_NAME": "push",
                "GITHUB_EVENT_PATH": self._write_event(tmp, json.dumps({"after": "x"})),
                "GITHUB_SHA": "push_sha",
                "GITHUB_REF": "refs/heads/main",
            }
            args = apply_github_env_defaults(_empty_args(), env)
        self.assertEqual((args.base, args.head, args.base_branch), ("origin/main", "push_sha", ""))

    def test_missing_and_malformed_event_file_fall_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            for path in (
                str(Path(tmp) / "absent.json"),
                self._write_event(tmp, "{not json"),
                self._write_event(tmp, "[1, 2]"),
            ):
                env = {
                    "GITHUB_EVENT_NAME": "pull_request",
                    "GITHUB_EVENT_PATH": path,
                    "GITHUB_SHA": "s",
                }
                args = apply_github_env_defaults(_empty_args(), env)
                self.assertEqual((args.base, args.head), ("origin/main", "s"), path)

    def test_explicit_flags_win(self):
        args = argparse.Namespace(
            base="B", head="H", base_branch="BB", event="E", ref="R", github_output="O"
        )
        env = {
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_SHA": "s",
            "GITHUB_REF": "r",
            "GITHUB_OUTPUT": "o",
        }
        args = apply_github_env_defaults(args, env)
        self.assertEqual(
            (args.base, args.head, args.base_branch, args.event, args.ref, args.github_output),
            ("B", "H", "BB", "E", "R", "O"),
        )

    def test_outside_actions_keeps_local_defaults(self):
        args = apply_github_env_defaults(_empty_args(), {})
        self.assertEqual(
            (args.base, args.head, args.base_branch, args.event, args.ref, args.github_output),
            (None, "HEAD", "", "pull_request", "", None),
        )


if __name__ == "__main__":
    unittest.main()
