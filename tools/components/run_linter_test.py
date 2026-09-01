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

"""Unit tests for the XMC component manifests linter runner script."""

import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.components import run_linter


class RunLinterTest(unittest.TestCase):
    """Hermetic unit test suite for run_linter module."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir_obj = tempfile.TemporaryDirectory()
        self.temp_dir = Path(self.temp_dir_obj.name)

    def tearDown(self) -> None:
        self.temp_dir_obj.cleanup()
        super().tearDown()

    def create_file(self, rel_path: str, content: str = "") -> Path:
        """Helper to create a file with parent directories in temp_dir."""
        target = self.temp_dir / rel_path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        return target

    def test_normalize_scan_roots(self) -> None:
        """Tests parsing and splitting scan root arguments."""
        roots = ["dir1", "dir2,dir3", "  dir4  "]
        result = run_linter.normalize_scan_roots(roots, base_dir=self.temp_dir)
        expected = [
            self.temp_dir / "dir1",
            self.temp_dir / "dir2",
            self.temp_dir / "dir3",
            self.temp_dir / "dir4",
        ]
        self.assertEqual(result, expected)

    def test_find_manifest_files(self) -> None:
        """Tests finding YAML manifests while filtering out rules files."""
        sub = self.temp_dir / "profile"
        sub.mkdir()
        self.create_file("profile/storage.yaml", "apiVersion: v1")
        self.create_file("profile/logging.yml", "apiVersion: v1")
        self.create_file("profile/rules.yaml", "staticChecks: {}")
        self.create_file("profile/rules.yml", "staticChecks: {}")
        self.create_file("profile/readme.txt", "notes")

        manifests = run_linter.find_manifest_files(sub)
        self.assertEqual(len(manifests), 2)
        self.assertEqual([m.name for m in manifests], ["logging.yml", "storage.yaml"])

    @patch("pathlib.Path.iterdir")
    def test_find_manifest_files_oserror(self, mock_iterdir: MagicMock) -> None:
        """Tests that find_manifest_files handles OSError gracefully."""
        mock_iterdir.side_effect = PermissionError("Access denied")
        manifests = run_linter.find_manifest_files(self.temp_dir)
        self.assertEqual(manifests, [])

    def test_find_rules_config(self) -> None:
        """Tests finding co-located rules.yaml and rules.yml."""
        sub1 = self.temp_dir / "p1"
        sub1.mkdir()
        rules1 = self.create_file("p1/rules.yaml", "staticChecks: {}")
        self.assertEqual(run_linter.find_rules_config(sub1), rules1)

        sub2 = self.temp_dir / "p2"
        sub2.mkdir()
        rules2 = self.create_file("p2/rules.yml", "staticChecks: {}")
        self.assertEqual(run_linter.find_rules_config(sub2), rules2)

        sub3 = self.temp_dir / "p3"
        sub3.mkdir()
        self.assertIsNone(run_linter.find_rules_config(sub3))

    def test_discover_lint_targets(self) -> None:
        """Tests recursive discovery of lint targets across roots."""
        self.create_file("root1/prof1/comp.yaml", "kind: Component")
        self.create_file("root1/prof1/rules.yaml", "staticChecks: {}")
        self.create_file("root1/empty_dir/rules.yaml", "staticChecks: {}")
        self.create_file("root2/comp2.yml", "kind: Component")

        scan_roots = [self.temp_dir / "root1", self.temp_dir / "root2"]
        targets = run_linter.discover_lint_targets(scan_roots)

        self.assertEqual(len(targets), 2)
        self.assertEqual(targets[0].manifests_dir, self.temp_dir / "root1" / "prof1")
        self.assertIsNotNone(targets[0].rules_config)
        self.assertEqual(targets[1].manifests_dir, self.temp_dir / "root2")
        self.assertIsNone(targets[1].rules_config)

    def test_resolve_linter_binary(self) -> None:
        """Tests resolving explicit binary or pre-built bazel-bin candidate."""
        self.assertEqual(
            run_linter.resolve_linter_binary("/custom/bin"),
            "/custom/bin",
        )
        self.assertIsNone(run_linter.resolve_linter_binary(None, base_dir=self.temp_dir))

        dummy_bin = self.create_file(
            "bazel-bin/cmd/xmc-linter/xmc-linter_/xmc-linter", "#!/bin/sh\n"
        )
        dummy_bin.chmod(0o755)
        self.assertEqual(
            run_linter.resolve_linter_binary(None, base_dir=self.temp_dir),
            str(dummy_bin),
        )

    def test_build_linter_command_with_binary(self) -> None:
        """Tests command assembly with pre-built binary."""
        target = run_linter.LintTarget(
            manifests_dir=Path("/path/to/profile"),
            rules_config=Path("/path/to/profile/rules.yaml"),
        )
        cmd = run_linter.build_linter_command(target, "/usr/bin/xmc-linter", "text")
        self.assertEqual(
            cmd,
            [
                "/usr/bin/xmc-linter",
                "--manifests-dir=/path/to/profile",
                "--output=text",
                "--rules-config=/path/to/profile/rules.yaml",
            ],
        )

    def test_build_linter_command_bazel_fallback(self) -> None:
        """Tests command assembly with Bazel fallback when binary is omitted."""
        target = run_linter.LintTarget(
            manifests_dir=Path("/path/to/profile"),
            rules_config=None,
        )
        cmd = run_linter.build_linter_command(target, None, "json")
        self.assertEqual(
            cmd,
            [
                "bazel",
                "run",
                "//cmd/xmc-linter:xmc-linter",
                "--",
                "--manifests-dir=/path/to/profile",
                "--output=json",
            ],
        )

    @patch("subprocess.run")
    def test_run_linter_on_target_success(self, mock_run: MagicMock) -> None:
        """Tests successful linter target execution."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["xmc-linter"],
            returncode=0,
            stdout="All component manifests passed static validation successfully!\n",
            stderr="",
        )
        target = run_linter.LintTarget(manifests_dir=Path("/path/to/dir"))
        res = run_linter.run_linter_on_target(target, "/bin/xmc-linter", "text")

        self.assertTrue(res.success)
        self.assertEqual(res.returncode, 0)
        self.assertIn("passed static validation", res.stdout)

    @patch("subprocess.run")
    def test_run_linter_on_target_failure(self, mock_run: MagicMock) -> None:
        """Tests failed linter target execution with error diagnostics."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["xmc-linter"],
            returncode=1,
            stdout="",
            stderr="[ERROR] missing required field\n",
        )
        target = run_linter.LintTarget(manifests_dir=Path("/path/to/dir"))
        res = run_linter.run_linter_on_target(target, "/bin/xmc-linter", "text")

        self.assertFalse(res.success)
        self.assertEqual(res.returncode, 1)
        self.assertIn("[ERROR]", res.stderr)

    @patch("subprocess.run")
    def test_run_linter_on_target_json(self, mock_run: MagicMock) -> None:
        """Tests json output parsing from linter."""
        json_payload = json.dumps({"valid": True, "diagnostics": [], "errors": [], "warnings": []})
        mock_run.return_value = subprocess.CompletedProcess(
            args=["xmc-linter"],
            returncode=0,
            stdout=json_payload,
            stderr="",
        )
        target = run_linter.LintTarget(manifests_dir=Path("/path/to/dir"))
        res = run_linter.run_linter_on_target(target, "/bin/xmc-linter", "json")

        self.assertTrue(res.success)
        self.assertIsNotNone(res.json_data)
        assert res.json_data is not None
        self.assertTrue(res.json_data["valid"])

    @patch("subprocess.run")
    def test_run_linter_on_target_invalid_json(self, mock_run: MagicMock) -> None:
        """Tests handling of invalid JSON stdout from linter process."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["xmc-linter"],
            returncode=1,
            stdout="non-json output",
            stderr="",
        )
        target = run_linter.LintTarget(manifests_dir=Path("/path/to/dir"))
        res = run_linter.run_linter_on_target(target, "/bin/xmc-linter", "json")

        self.assertFalse(res.success)
        self.assertIsNone(res.json_data)
        self.assertEqual(res.stdout, "non-json output")

    @patch("subprocess.run")
    def test_run_linter_on_target_oserror(self, mock_run: MagicMock) -> None:
        """Tests handling of OSError (e.g. missing executable) when running linter."""
        mock_run.side_effect = FileNotFoundError("Executable not found")
        target = run_linter.LintTarget(manifests_dir=Path("/path/to/dir"))
        res = run_linter.run_linter_on_target(target, "/nonexistent/xmc-linter", "text")

        self.assertFalse(res.success)
        self.assertEqual(res.returncode, -1)
        self.assertIn("Failed to execute linter command", res.stderr)

    def test_format_text_report(self) -> None:
        """Tests formatting aggregated text output."""
        results = [
            run_linter.LintResult(
                manifests_dir="dir1",
                rules_config="dir1/rules.yaml",
                success=True,
                returncode=0,
                stdout="OK",
                stderr="",
            ),
            run_linter.LintResult(
                manifests_dir="dir2",
                rules_config=None,
                success=False,
                returncode=1,
                stdout="",
                stderr="FAIL",
            ),
        ]
        text, valid = run_linter.format_text_report(results)
        self.assertFalse(valid)
        self.assertIn("=== Validating Profile: dir1 (rules: dir1/rules.yaml) ===", text)
        self.assertIn("=== Validating Profile: dir2 ===", text)
        self.assertIn("2 total, 1 passed, 1 failed", text)

    def test_format_json_report(self) -> None:
        """Tests formatting structured JSON report."""
        results = [
            run_linter.LintResult(
                manifests_dir="dir1",
                rules_config="dir1/rules.yaml",
                success=True,
                returncode=0,
                stdout="",
                stderr="",
                json_data={"valid": True},
            )
        ]
        data = run_linter.format_json_report(results)
        self.assertTrue(data["valid"])
        self.assertEqual(data["total_targets"], 1)
        self.assertEqual(data["passed_targets"], 1)
        self.assertEqual(data["failed_targets"], 0)
        self.assertEqual(len(data["results"]), 1)

    def test_main_empty_strict_fails(self) -> None:
        """Tests that empty scan roots in strict mode exit with status 1."""
        empty_root = self.temp_dir / "empty"
        empty_root.mkdir()
        with patch("sys.stderr", new=io.StringIO()) as mock_stderr:
            code = run_linter.main(["--scan-roots", str(empty_root), "--strict"])
            self.assertEqual(code, 1)
            self.assertIn(
                "Error: No profile or component manifest directories", mock_stderr.getvalue()
            )

    def test_main_empty_strict_json_fails(self) -> None:
        """Tests that empty scan roots in strict mode with JSON output exit with status 1 and JSON error."""
        empty_root = self.temp_dir / "empty"
        empty_root.mkdir()
        with patch("sys.stdout", new=io.StringIO()) as mock_stdout:
            code = run_linter.main(["--scan-roots", str(empty_root), "--strict", "--output=json"])
            self.assertEqual(code, 1)
            data = json.loads(mock_stdout.getvalue())
            self.assertFalse(data["valid"])
            self.assertIn("No profile or component manifest directories", data["error"])

    def test_main_empty_non_strict_succeeds(self) -> None:
        """Tests that empty scan roots in non-strict mode exit with status 0."""
        empty_root = self.temp_dir / "empty"
        empty_root.mkdir()
        with patch("sys.stdout", new=io.StringIO()) as mock_stdout:
            code = run_linter.main(["--scan-roots", str(empty_root)])
            self.assertEqual(code, 0)
            self.assertIn(
                "Notice: No profile or component manifest directories", mock_stdout.getvalue()
            )

    @patch("subprocess.run")
    def test_main_end_to_end_success(self, mock_run: MagicMock) -> None:
        """Tests successful end-to-end execution of main."""
        self.create_file("profile/comp.yaml", "kind: Component")
        mock_run.return_value = subprocess.CompletedProcess(
            args=["xmc-linter"],
            returncode=0,
            stdout="OK",
            stderr="",
        )
        with patch("sys.stdout", new=io.StringIO()) as mock_stdout:
            code = run_linter.main(["--scan-roots", str(self.temp_dir / "profile")])
            self.assertEqual(code, 0)
            self.assertIn("1 total, 1 passed, 0 failed", mock_stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
