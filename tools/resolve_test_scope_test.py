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

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from resolve_test_scope import (
    _discover_static_rdeps_targets,
    _parse_build_file_test_targets,
    classify_modified_files,
    discover_bazel_rdeps_targets,
    find_enclosing_bazel_package,
    get_git_modified_files,
    is_non_code_file,
    resolve_test_scope,
    write_github_output,
)


class TestResolveTestScope(unittest.TestCase):
    def test_is_non_code_file(self):
        self.assertTrue(is_non_code_file("README.md"))
        self.assertTrue(is_non_code_file("docs/architecture.md"))
        self.assertTrue(is_non_code_file("deploy/helm/templates/ui.yaml"))
        self.assertTrue(is_non_code_file(".githooks/pre-commit"))
        self.assertFalse(is_non_code_file("pkg/auth/oauth2/handlers.go"))
        self.assertFalse(is_non_code_file("cmd/ui/src/app/app.component.ts"))
        self.assertFalse(is_non_code_file("services/billing/v1/billing.go"))

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
        self.assertFalse(res_doc["has_workflow"])
        self.assertFalse(res_doc["has_ui"])
        self.assertFalse(res_doc["has_backend"])

    def test_classify_modified_files_cmd_ui_server_python(self):
        # Python server in cmd/ui/ triggers both UI and backend tests
        res = classify_modified_files(["cmd/ui/server.py"])
        self.assertFalse(res["has_core_infra"])
        self.assertTrue(res["has_ui"])
        self.assertTrue(res["has_backend"])

        # Workflow file
        res_wf = classify_modified_files([".github/workflows/unit-tests.yml"])
        self.assertFalse(res_wf["has_core_infra"])
        self.assertTrue(res_wf["has_workflow"])
        self.assertTrue(res_wf["has_ui"])
        self.assertTrue(res_wf["has_backend"])

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

    def test_resolve_test_scope_workflow_file_modified(self):
        scope = resolve_test_scope([".github/workflows/unit-tests.yml"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")
        self.assertEqual(scope["bazel_targets"], "//... -//cmd/ui/...")
        self.assertEqual(scope["reason"], "workflow file modified")

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

    @patch("resolve_test_scope.discover_bazel_rdeps_targets")
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

    @patch("subprocess.run")
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

    @patch("subprocess.run")
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

    @patch("subprocess.run")
    def test_discover_bazel_rdeps_targets_exception_safety(self, mock_run):
        mock_run.side_effect = Exception("Bazel timeout")
        targets = discover_bazel_rdeps_targets(["nonexistent/path/foo.go"])
        self.assertEqual(targets, [])

    def test_discover_static_rdeps_targets_root_dir_matching(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmppath = Path(tmpdir)
            (tmppath / "BUILD.bazel").write_text('py_test(name = "root_test")\n')
            (tmppath / "pkg1").mkdir()
            (tmppath / "pkg1" / "BUILD.bazel").write_text('go_test(name = "pkg1_test")\n')
            (tmppath / "cmd" / "ui").mkdir(parents=True)
            (tmppath / "cmd" / "ui" / "BUILD.bazel").write_text(
                'py_test(name = "server_test")\npy_test(name = "ui_unit_tests")\n'
            )

            # Test root file does not match all subpackages
            targets_root = discover_bazel_rdeps_targets(["root_script.py"], cwd=tmppath)
            self.assertIn("//:root_test", targets_root)
            self.assertNotIn("//pkg1:pkg1_test", targets_root)

            # Test cmd/ui/server.py matches cmd/ui:server_test but excludes ui_unit_tests
            targets_ui = discover_bazel_rdeps_targets(["cmd/ui/server.py"], cwd=tmppath)
            self.assertIn("//cmd/ui:server_test", targets_ui)
            self.assertNotIn("//cmd/ui:ui_unit_tests", targets_ui)

    def test_parse_build_file_test_targets_ast(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            build_path = Path(tmpdir) / "BUILD.bazel"
            build_path.write_text(
                "compat_go_test(\n"
                '    name = "manual_test",\n'
                '    tags = ["manual", "local"],\n'
                ")\n"
                "go_test(\n"
                '    name = "unit_test",\n'
                '    tags = ["unit"],\n'
                ")\n"
                "py_test(\n"
                '    name = "py_unit_test",\n'
                ")\n"
            )
            targets = _parse_build_file_test_targets(build_path)
            self.assertEqual(targets, ["unit_test", "py_unit_test"])
            self.assertNotIn("manual_test", targets)

    @patch("subprocess.run")
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

    def test_discover_static_rdeps_targets_with_migration_sql(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            mig_dir = base / "internal" / "storage" / "spanner" / "migrations"
            mig_dir.mkdir(parents=True)
            (mig_dir / "BUILD.bazel").write_text(
                'compat_go_test(\n    name = "migrations_test",\n)\n',
                encoding="utf-8",
            )
            nested_sql_dir = mig_dir / "ess"
            nested_sql_dir.mkdir()
            (nested_sql_dir / "001_baseline.up.sql").touch()

            targets = _discover_static_rdeps_targets(
                ["internal/storage/spanner/migrations/ess/001_baseline.up.sql"],
                cwd=base,
            )
            self.assertIn("//internal/storage/spanner/migrations:migrations_test", targets)

    def test_discover_static_rdeps_targets_path_prefix_boundary(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            (base / "BUILD.bazel").touch()
            pkg_spanner = base / "internal" / "storage" / "spanner"
            pkg_spanner.mkdir(parents=True)
            (pkg_spanner / "BUILD.bazel").write_text(
                'go_test(\n    name = "spanner_test",\n)\n',
                encoding="utf-8",
            )
            pkg_spanner_alt = base / "internal" / "storage" / "spanner_alt"
            pkg_spanner_alt.mkdir(parents=True)
            (pkg_spanner_alt / "BUILD.bazel").write_text(
                'go_test(\n    name = "spanner_alt_test",\n)\n',
                encoding="utf-8",
            )

            # Modifying spanner should match spanner_test but NOT spanner_alt_test
            targets = _discover_static_rdeps_targets(
                ["internal/storage/spanner/client.go"],
                cwd=base,
            )
            self.assertIn("//internal/storage/spanner:spanner_test", targets)
            self.assertNotIn("//internal/storage/spanner_alt:spanner_alt_test", targets)

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

    @patch("resolve_test_scope.subprocess.run")
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

    @patch("resolve_test_scope.subprocess.run")
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
                scope["bazel_targets"],
            )

    def test_resolve_test_scope_core_infra_compat_bzl(self):
        scope = resolve_test_scope(["compat.bzl"], use_rdeps=False)
        self.assertTrue(scope["run_backend"])
        self.assertTrue(scope["run_ui"])
        self.assertEqual(scope["backend_mode"], "all")


if __name__ == "__main__":
    unittest.main()
