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
            "cmd/ui/app/testing/test.ts",
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
            "cmd/ui/app/quota_banner/test_quota_banner.ts",
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

    def test_ignores_unstaged_worktree_deletion_of_non_allowlisted_violation(self) -> None:
        bad_file = self._write_and_stage("cmd/ui/app/banner/banner.spec.ts")
        bad_file.unlink()

        result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

    def test_fails_when_allowlisted_file_is_removed(self) -> None:
        (self.root / "cmd/ui/index.html").unlink()

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
        fake_proc = subprocess.CompletedProcess(
            args=["git", "ls-files", "-z"],
            returncode=0,
            stdout="cmd/ui/index.html\0docs/notes_\ufffd.md\0",
            stderr="",
        )
        with mock.patch.object(cli, "run_command", return_value=fake_proc):
            result = cli.check_file_naming(argparse.Namespace(repo_root=str(self.root)))
        self.assertEqual(result["violations"], 0)

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
        self.assertEqual(payload["status"], "error")
        violations = payload.get("violations") or payload.get("details", {}).get("violations")
        self.assertEqual(len(violations), 1)


if __name__ == "__main__":
    unittest.main()
