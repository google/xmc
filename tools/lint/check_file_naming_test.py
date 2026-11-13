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

"""Unit tests for tools.lint.check_file_naming."""

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
from tools.lint import check_file_naming as cli


class ClassifyPathViolationTest(unittest.TestCase):
    """Pure predicate coverage across positive and negative boundary sets."""

    def test_allows_compliant_repository_paths(self) -> None:
        valid_paths = (
            "cmd/ui/index.html",
            "cmd/ui/app/quota_banner/quota_banner.ng.html",
            "cmd/ui/app/quota_banner/quota_banner_test.ts",
            "cmd/ui/app/admin_panel.guard_test.ts",
            "cmd/ui/app/service/testing/mock_auth_service_test.ts",
            "cmd/ui/app/service/testing/test_fixtures.ts",
            "cmd/ui/app/testing/test.ts",
            "cmd/ui/app/testing/test_fixtures.ts",
            "cmd/ui/app/testing/testing.d.ts",
            "cmd/ui/main.ts",
            "cmd/ui/playwright.config.ts",
            "cmd/ui/eslint.config.js",
            "cmd/ui/karma.conf.js",
            "cmd/ui/server_test.py",
            "cmd/ui/screenshot_tests/test_utils.ts",
            "cmd/ui/screenshot_tests/rod/rod.screenshot.spec.ts",
            "cmd/ui/screenshot_tests/rod/rod.screenshot.spec.ts-snapshots/view.png",
            ".github/scripts/test-file-classifier.js",
            ".github/scripts/test-file-classifier.test.js",
            ".github/scripts/README.md",
            "tools/nightly_tests/gallery_template/index.html",
            "tools/nightly_tests/format_visual_audit_summary_test.js",
            "tools/widget.ts",
        )
        for path in valid_paths:
            with self.subTest(path=path):
                self.assertIsNone(cli.classify_path_violation(path))

    def test_rejects_misnamed_templates_and_tests(self) -> None:
        invalid_paths = (
            "cmd/ui/app/quota_banner/quota_banner.html",
            "cmd/ui/app/index.html",
            "cmd/ui/extra.html",
            "cmd/ui/outside_app.ng.html",
            "cmd/ui/app/quota_banner/quota_banner.spec.ts",
            "cmd/ui/app/quota_banner/quota_banner_spec.ts",
            "cmd/ui/app/quota_banner/quota_banner.test.ts",
            "cmd/ui/app/quota_banner/quota_banner_test.tsx",
            "cmd/ui/app/quota_banner/quota_banner_test.mts",
            "cmd/ui/main_test.ts",
            "tools/stray.spec.ts",
            "cmd/ui/screenshot_tests/rod/rod.spec.ts",
            "cmd/ui/screenshot_tests/rod/rod_spec.ts",
            "cmd/ui/screenshot_tests/rod/rod_test.ts",
            "cmd/ui/screenshot_tests/rod/rod.test.ts",
            ".github/scripts/foo_test.js",
            ".github/scripts/foo.spec.js",
            ".github/scripts/foo_spec.js",
            ".github/scripts/test_foo.js",
            ".github/scripts/foo.test.mjs",
            ".github/scripts/foo.test.ts",
            ".github/scripts/nested/foo.test.js",
        )
        for path in invalid_paths:
            with self.subTest(path=path):
                self.assertIsNotNone(cli.classify_path_violation(path))

    def test_reports_github_scripts_message_for_misnamed_ts_workflow_test(self) -> None:
        reason = cli.classify_path_violation(".github/scripts/foo.test.ts")
        self.assertIsNotNone(reason)
        assert reason is not None
        self.assertIn(".github/scripts/", reason)
        self.assertIn("*.test.js", reason)


class CheckFileNamingEndToEndTest(unittest.TestCase):
    """Hermetic git-repository tests for CLI, allowlist, and repo-root gates."""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)
        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        self._write_and_stage("cmd/ui/index.html", "<!doctype html>\n")

    def _write_and_stage(self, rel_path: str, content: str = "// ok\n") -> Path:
        full_path = self.root / rel_path
        full_path.parent.mkdir(parents=True, exist_ok=True)
        full_path.write_text(content, encoding="utf-8")
        subprocess.run(["git", "add", "--", rel_path], cwd=self.root, check=True)
        return full_path

    def test_passes_clean_repository(self) -> None:
        self._write_and_stage("cmd/ui/app/banner/banner.ng.html", "<p>ok</p>\n")
        self._write_and_stage("cmd/ui/app/banner/banner_test.ts")
        self._write_and_stage("cmd/ui/screenshot_tests/banner.screenshot.spec.ts")
        self._write_and_stage(".github/scripts/check.js")
        self._write_and_stage(".github/scripts/check.test.js")

        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)
        self.assertEqual(result["files_scanned"], 6)

    def test_fails_and_reports_structured_violations(self) -> None:
        self._write_and_stage("cmd/ui/app/banner/banner.spec.ts")
        self._write_and_stage(".github/scripts/check_test.js")

        with self.assertRaises(HandledError) as ctx:
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        assert ctx.exception.details is not None
        paths = [item["path"] for item in ctx.exception.details["violations"]]
        self.assertEqual(
            paths,
            [".github/scripts/check_test.js", "cmd/ui/app/banner/banner.spec.ts"],
        )

    def test_catches_staged_violation_deleted_from_worktree_until_git_rm(self) -> None:
        bad_file = self._write_and_stage("cmd/ui/app/banner/banner.spec.ts")
        bad_file.unlink()

        with self.assertRaises(HandledError):
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        subprocess.run(
            ["git", "rm", "-f", "-q", "--", "cmd/ui/app/banner/banner.spec.ts"],
            cwd=self.root,
            check=True,
        )
        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

    def test_fails_when_allowlisted_file_is_removed(self) -> None:
        subprocess.run(
            ["git", "rm", "-f", "-q", "--", "cmd/ui/index.html"],
            cwd=self.root,
            check=True,
        )

        with self.assertRaises(HandledError) as ctx:
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        assert ctx.exception.details is not None
        self.assertEqual(ctx.exception.details["stale_allowlist"], ["cmd/ui/index.html"])

    def test_rejects_missing_or_subdirectory_repo_root(self) -> None:
        missing = self.root / "does_not_exist"
        subdir = self.root / "cmd/ui"
        for candidate in (missing, subdir):
            with self.subTest(repo_root=str(candidate)):
                with self.assertRaises(HandledError):
                    cli.check_file_naming(argparse.Namespace(repo_root=str(candidate)))

    def test_handles_replacement_char_paths_from_git_ls_files(self) -> None:
        clean_proc = subprocess.CompletedProcess(
            args=["git", "ls-files", "-z"],
            returncode=0,
            stdout="cmd/ui/index.html\0docs/notes_\ufffd.md\0cmd/ui/app/banner_\ufffd_test.ts\0",
            stderr="",
        )
        with mock.patch.object(cli, "run_command", return_value=clean_proc):
            result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result, {"files_scanned": 3, "violations": 0})

        bad_proc = subprocess.CompletedProcess(
            args=["git", "ls-files", "-z"],
            returncode=0,
            stdout="cmd/ui/index.html\0cmd/ui/app/banner_\ufffd.spec.ts\0",
            stderr="",
        )
        with (
            mock.patch.object(cli, "run_command", return_value=bad_proc),
            self.assertRaises(HandledError) as ctx,
        ):
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        assert ctx.exception.details is not None
        self.assertEqual(
            [v["path"] for v in ctx.exception.details["violations"]],
            ["cmd/ui/app/banner_\ufffd.spec.ts"],
        )

    def test_main_emits_error_json_to_stderr_and_exits_one(self) -> None:
        self._write_and_stage("cmd/ui/app/banner/banner.html", "<p>bad</p>\n")
        stderr_buf = io.StringIO()
        with (
            mock.patch.dict("os.environ", {"XMC_BYPASS_VENV": "1"}),
            mock.patch("sys.argv", ["check_file_naming.py", "--repo-root", str(self.root)]),
            mock.patch("sys.stderr", stderr_buf),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            cli.main()

        self.assertEqual(exit_ctx.exception.code, 1)
        payload = json.loads(stderr_buf.getvalue())
        self.assertEqual(
            set(payload.keys()),
            {"status", "error_type", "message", "instruction", "details"},
        )
        self.assertEqual(payload["status"], "error")
        self.assertEqual(payload["error_type"], "handled")
        self.assertEqual(payload["details"]["stale_allowlist"], [])
        self.assertEqual(
            [v["path"] for v in payload["details"]["violations"]],
            ["cmd/ui/app/banner/banner.html"],
        )

    def test_enforces_one_to_one_unit_test_pairing_across_languages(self) -> None:
        self._write_and_stage("internal/pkg/service.go", "package pkg\nfunc Run() {}\n")
        self._write_and_stage("tools/helper.py", "def compute() -> int:\n    return 1\n")
        self._write_and_stage(
            "cmd/ui/app/banner/banner.ts",
            "@Component({selector: 'xmc-banner'})\nexport class Banner {}\n",
        )
        self._write_and_stage(".github/scripts/deploy.js", "module.exports = {};\n")

        with self.assertRaises(HandledError) as ctx:
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        assert ctx.exception.details is not None
        violation_paths = [v["path"] for v in ctx.exception.details["violations"]]
        self.assertEqual(
            violation_paths,
            [
                ".github/scripts/deploy.js",
                "cmd/ui/app/banner/banner.ts",
                "internal/pkg/service.go",
                "tools/helper.py",
            ],
        )

        # Adding the 1-to-1 unit tests clears all violations.
        self._write_and_stage("internal/pkg/service_test.go", "package pkg\n")
        self._write_and_stage("tools/helper_test.py", "import unittest\n")
        self._write_and_stage("cmd/ui/app/banner/banner_test.ts", "// test\n")
        self._write_and_stage(".github/scripts/deploy.test.js", "// test\n")

        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

    def test_exempts_entrypoints_pure_declarations_harnesses_and_examples(self) -> None:
        self._write_and_stage("cmd/server/main.go", "package main\nfunc main() {}\n")
        self._write_and_stage("internal/pkg/types.go", "package pkg\ntype Config struct{}\n")
        self._write_and_stage("sdks/python/xmc/constants.py", "TIMEOUT = 30\n")
        self._write_and_stage(
            "cmd/ui/app/banner/banner.model.ts",
            "export interface BannerModel { id: string; }\n",
        )
        self._write_and_stage("cmd/ui/app/banner/banner_harness.ts", "export class Harness {}\n")
        self._write_and_stage("examples/demo/train.py", "def train() -> None:\n    pass\n")

        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

    def test_detects_unwired_bazel_test_files_when_build_bazel_tracked(self) -> None:
        self._write_and_stage("BUILD.bazel", "# root build\n")
        self._write_and_stage("internal/pkg/service.go", "package pkg\nfunc Run() {}\n")
        self._write_and_stage("internal/pkg/service_test.go", "package pkg\n")
        self._write_and_stage("tools/helper.py", "def compute() -> int:\n    return 1\n")
        self._write_and_stage("tools/helper_test.py", "import unittest\n")

        with self.assertRaises(HandledError) as ctx:
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        assert ctx.exception.details is not None
        violation_paths = [v["path"] for v in ctx.exception.details["violations"]]
        self.assertEqual(violation_paths, ["internal/pkg/service_test.go", "tools/helper_test.py"])

        # Registering the tests in BUILD.bazel resolves the violations.
        self._write_and_stage(
            "internal/pkg/BUILD.bazel",
            'go_test(name = "pkg_test", srcs = ["service_test.go"])\n',
        )
        self._write_and_stage(
            "tools/BUILD.bazel",
            'py_test(name = "helper_test", srcs = ["helper_test.py"])\n',
        )
        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

    def test_ratchets_stale_legacy_unpaired_allowlist_entry(self) -> None:
        legacy_go = "internal/broadcast/events.go"
        self._write_and_stage(legacy_go, "package broadcast\nfunc Emit() {}\n")
        # Without a paired test, the legacy allowlist permits the file.
        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

        # Once a paired test is added, keeping the entry in LEGACY_UNPAIRED_ALLOWLIST fails.
        self._write_and_stage("internal/broadcast/events_test.go", "package broadcast\n")
        with self.assertRaises(HandledError) as ctx:
            cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))

        assert ctx.exception.details is not None
        self.assertEqual(ctx.exception.details["stale_allowlist"], [legacy_go])


if __name__ == "__main__":
    unittest.main()
