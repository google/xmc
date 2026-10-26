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

"""Unit tests for tools/lint/check_docs_topography_map.py."""

from __future__ import annotations

import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tools.lint import check_docs_topography_map as linter

SAMPLE_VALID_MAP_JSON = json.dumps(
    {
        "quadrants": [
            {
                "name": "Services",
                "source_patterns": ["cmd/**", "pkg/auth/**"],
                "doc_patterns": [
                    "README.md",
                    "cmd/**/README.md",
                    "docs/**/*.md",
                ],
            },
            {
                "name": "SDKs",
                "source_patterns": ["sdks/python/xmc/**"],
                "doc_patterns": [
                    "sdks/python/**/README.md",
                    ".agents/**/*.md",
                ],
            },
        ],
        "identifier_sources": [
            {
                "identifier_type": "CLI Flags",
                "source_patterns": ["cmd/*/main.go", "pkg/**"],
                "verification_command": "git grep StringVar cmd/",
            }
        ],
    }
)

SAMPLE_TRACKED_FILES: tuple[str, ...] = (
    "README.md",
    "cmd/actuator/main.go",
    "cmd/actuator/README.md",
    "pkg/auth/jwks.go",
    "docs/overview.md",
    "docs/architecture/components.md",
    "sdks/python/xmc/__init__.py",
    "sdks/python/xmc/README.md",
    ".agents/AGENTS.md",
    ".agents/skills/xmc_review_docs_drift/SKILL.md",
)


class CheckDocsTopographyMapTest(unittest.TestCase):
    """Tests for bi-directional documentation topography map verification."""

    def test_matches_pattern_uses_pure_posix_path_full_match(self) -> None:
        """Verifies `matches_pattern` evaluates standard globs via PurePosixPath."""
        self.assertTrue(linter.matches_pattern("cmd/actuator/main.go", "cmd/**"))
        self.assertFalse(linter.matches_pattern("pkg/actuator/main.go", "cmd/**"))
        self.assertTrue(linter.matches_pattern("docs/developer.md", "docs/**/*.md"))
        self.assertTrue(linter.matches_pattern("docs/architecture/overview.md", "docs/**/*.md"))
        self.assertTrue(linter.matches_pattern("cmd/actuator/main.go", "cmd/*/main.go"))
        self.assertFalse(linter.matches_pattern("cmd/actuator/sub/main.go", "cmd/*/main.go"))

    def test_load_topography_map_extracts_all_pattern_groups(self) -> None:
        """Verifies JSON `quadrants` and `identifier_sources` load cleanly."""
        patterns = linter.load_topography_map(SAMPLE_VALID_MAP_JSON)
        self.assertEqual(
            patterns.source_patterns,
            ("cmd/**", "pkg/auth/**", "sdks/python/xmc/**"),
        )
        self.assertEqual(
            patterns.doc_patterns,
            (
                "README.md",
                "cmd/**/README.md",
                "docs/**/*.md",
                "sdks/python/**/README.md",
                ".agents/**/*.md",
            ),
        )
        self.assertEqual(
            patterns.registry_patterns,
            ("cmd/*/main.go", "pkg/**"),
        )

    def test_load_topography_map_raises_on_non_dict_root(self) -> None:
        """Verifies `load_topography_map` rejects non-object JSON roots."""
        with self.assertRaises(ValueError):
            linter.load_topography_map("[]")

    def test_discover_component_directories_identifies_all_roots(self) -> None:
        """Verifies single-level and two-level component directories are found."""
        files = (
            "cmd/actuator/main.go",
            "sdks/python/xmc/__init__.py",
            "sdks/python/xdash/client.py",
            "tools/analyze_diff_size.py",
            "tools/lint/check_docs_topography_map.py",
        )
        self.assertEqual(
            linter.discover_component_directories(files),
            [
                "cmd/actuator/",
                "sdks/python/xdash/",
                "sdks/python/xmc/",
                "tools/lint/",
            ],
        )

    def test_validate_topography_map_passes_when_complete(self) -> None:
        """Verifies all three checks pass on a synchronized map and file list."""
        patterns = linter.load_topography_map(SAMPLE_VALID_MAP_JSON)
        report = linter.validate_topography_map(patterns, SAMPLE_TRACKED_FILES)
        self.assertTrue(report.is_valid())
        self.assertEqual(report.dead_map_entries, ())
        self.assertEqual(report.unmapped_doc_files, ())
        self.assertEqual(report.unmapped_source_dirs, ())

    def test_check_1_detects_dead_map_entry(self) -> None:
        """Verifies Check 1 flags map entries matching zero tracked files."""
        patterns = linter.TopographyPatterns(
            source_patterns=(
                "cmd/**",
                "pkg/nonexistent/**",
                "pkg/nonexistent/**",
            ),
            doc_patterns=("README.md", "docs/deleted.md"),
            registry_patterns=("cmd/*/missing.go",),
        )
        dead = linter.find_dead_map_entries(patterns, SAMPLE_TRACKED_FILES)
        self.assertEqual(
            dead,
            ("pkg/nonexistent/**", "docs/deleted.md", "cmd/*/missing.go"),
        )

    def test_check_2_detects_unmapped_doc_file(self) -> None:
        """Verifies Check 2 flags tracked `.md` files missing from doc_patterns."""
        patterns = linter.load_topography_map(SAMPLE_VALID_MAP_JSON)
        files_with_extra_doc = (
            *SAMPLE_TRACKED_FILES,
            "deploy/new_profile/README.md",
        )
        report = linter.validate_topography_map(patterns, files_with_extra_doc)
        self.assertFalse(report.is_valid())
        self.assertEqual(
            report.unmapped_doc_files,
            ("deploy/new_profile/README.md",),
        )

    def test_check_3_detects_unmapped_source_dir(self) -> None:
        """Verifies Check 3 flags component directories missing from source_patterns."""
        patterns = linter.load_topography_map(SAMPLE_VALID_MAP_JSON)
        files_with_new_component = (
            *SAMPLE_TRACKED_FILES,
            "internal/new_subsystem/service.go",
            "sdks/python/new_sdk/client.py",
        )
        report = linter.validate_topography_map(patterns, files_with_new_component)
        self.assertFalse(report.is_valid())
        self.assertEqual(
            report.unmapped_source_dirs,
            ("internal/new_subsystem/", "sdks/python/new_sdk/"),
        )

    @patch.object(subprocess, "run")
    def test_list_tracked_files_invokes_git_ls_files(
        self, mock_run: unittest.mock.MagicMock
    ) -> None:
        """Verifies `list_tracked_files` parses `git ls-files` stdout lines."""
        mock_run.return_value = subprocess.CompletedProcess(
            args=["git", "ls-files"],
            returncode=0,
            stdout="README.md\ncmd/actuator/main.go\n\n",
            stderr="",
        )
        result = linter.list_tracked_files(Path("/repo"))
        self.assertEqual(result, ["README.md", "cmd/actuator/main.go"])
        mock_run.assert_called_once_with(
            ["git", "ls-files"],
            cwd=Path("/repo"),
            check=True,
            capture_output=True,
            text=True,
        )

    @patch.object(linter, "list_tracked_files")
    def test_main_returns_zero_on_valid_map(self, mock_list_files: unittest.mock.MagicMock) -> None:
        """Verifies `main` exits 0 when all bi-directional checks pass."""
        mock_list_files.return_value = list(SAMPLE_TRACKED_FILES)
        with tempfile.TemporaryDirectory() as tmp_dir:
            map_file = Path(tmp_dir) / "map.json"
            map_file.write_text(SAMPLE_VALID_MAP_JSON, encoding="utf-8")
            exit_code = linter.main(["--repo-root", tmp_dir, "--map-file", "map.json"])
            self.assertEqual(exit_code, 0)

    @patch.object(linter, "list_tracked_files")
    def test_main_prints_diagnostics_and_returns_one_on_failure(
        self, mock_list_files: unittest.mock.MagicMock
    ) -> None:
        """Verifies `main` prints all 3 diagnostic tags to stderr on failure."""
        mock_list_files.return_value = [
            "README.md",
            "unmapped_doc.md",
            "pkg/unmapped_pkg/file.go",
        ]
        with tempfile.TemporaryDirectory() as tmp_dir:
            map_file = Path(tmp_dir) / "map.json"
            map_file.write_text(SAMPLE_VALID_MAP_JSON, encoding="utf-8")
            stderr_buf = io.StringIO()
            with patch("sys.stderr", stderr_buf):
                exit_code = linter.main(["--repo-root", tmp_dir, "--map-file", str(map_file)])
            self.assertEqual(exit_code, 1)
            output = stderr_buf.getvalue()
            self.assertIn("[DEAD_MAP_ENTRY]", output)
            self.assertIn("[UNMAPPED_DOC_FILE]", output)
            self.assertIn("[UNMAPPED_SOURCE_DIR]", output)

    def test_main_returns_one_when_map_file_missing(self) -> None:
        """Verifies `main` exits 1 when the target map file does not exist."""
        with tempfile.TemporaryDirectory() as tmp_dir:
            stderr_buf = io.StringIO()
            with patch("sys.stderr", stderr_buf):
                exit_code = linter.main(["--repo-root", tmp_dir, "--map-file", "missing.json"])
            self.assertEqual(exit_code, 1)
            self.assertIn("[DEAD_MAP_ENTRY]", stderr_buf.getvalue())


if __name__ == "__main__":
    unittest.main()
