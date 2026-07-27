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

"""Unit tests for discover_test_matrix module."""

import unittest
from unittest.mock import patch

from tools.discover_test_matrix import (
    build_test_matrix,
    convert_files_to_bazel_targets,
    extract_root_folders,
    get_changed_files,
    is_global_change,
    map_test_targets_to_matrix,
    query_affected_test_targets,
    query_bazel_test_targets,
)


class DiscoverTestMatrixTest(unittest.TestCase):
    def setUp(self) -> None:
        self.sample_targets = [
            "//:root_test",
            "//cmd/actuator:workunittaskhandler_test",
            "//cmd/ui:server_test",
            "//deploy/cli:install_xmanager_test",
            "//internal/auth:auth_test",
            "//internal/storage/spanner:spanner_test",
            "//pkg/auth/oauth2:oauth2_test",
            "//tools:build_oci_test",
        ]

    def test_extract_root_folders(self) -> None:
        roots = extract_root_folders(self.sample_targets)
        self.assertEqual(roots, {"root", "cmd", "internal", "deploy", "pkg", "tools"})

    def test_convert_files_to_bazel_targets(self) -> None:
        targets = convert_files_to_bazel_targets(
            ["internal/auth/auth.go", "cmd/ui/app/main.ts", "README.md"]
        )
        self.assertEqual(targets, ["//:all", "//cmd/ui/app/...", "//internal/auth/..."])

    def test_is_global_change(self) -> None:
        self.assertTrue(is_global_change([".github/workflows/unit-tests.yml"]))
        self.assertTrue(is_global_change(["MODULE.bazel"]))
        self.assertTrue(is_global_change(["tools/discover_test_matrix.py"]))
        self.assertFalse(is_global_change(["pkg/auth/oauth2.go"]))
        self.assertFalse(is_global_change(["docs/README.md"]))

    def test_map_test_targets_to_matrix(self) -> None:
        matrix = map_test_targets_to_matrix(self.sample_targets)
        expected = [
            {"target": "root", "bazel_args": "//:all"},
            {"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."},
            {"target": "cmd/ui", "bazel_args": "//cmd/ui/..."},
            {"target": "deploy", "bazel_args": "//deploy/..."},
            {
                "target": "internal/rest",
                "bazel_args": "//internal/... -//internal/storage/...",
            },
            {"target": "internal/storage", "bazel_args": "//internal/storage/..."},
            {"target": "pkg", "bazel_args": "//pkg/..."},
            {"target": "tools", "bazel_args": "//tools/..."},
        ]
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_unfiltered(self) -> None:
        matrix = build_test_matrix(self.sample_targets, changed_files=None)
        expected = map_test_targets_to_matrix(self.sample_targets)
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_cross_dependencies(self) -> None:
        # When internal/auth/auth.go is changed, rdeps discovers both internal/auth and cmd/actuator tests.
        affected_override = [
            "//cmd/actuator:workunittaskhandler_test",
            "//internal/auth:auth_test",
        ]
        matrix = build_test_matrix(
            self.sample_targets,
            changed_files=["internal/auth/auth.go"],
            affected_targets_override=affected_override,
        )
        expected = [
            {"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."},
            {
                "target": "internal/rest",
                "bazel_args": "//internal/... -//internal/storage/...",
            },
        ]
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_pkg_only(self) -> None:
        matrix = build_test_matrix(
            self.sample_targets,
            changed_files=["pkg/auth/oauth2.go"],
            affected_targets_override=["//pkg/auth/oauth2:oauth2_test"],
        )
        expected = [{"target": "pkg", "bazel_args": "//pkg/..."}]
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_cmd_ui_only(self) -> None:
        matrix = build_test_matrix(
            self.sample_targets,
            changed_files=["cmd/ui/app/main.ts"],
            affected_targets_override=["//cmd/ui:server_test"],
        )
        expected = [{"target": "cmd/ui", "bazel_args": "//cmd/ui/..."}]
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_internal_storage_only(self) -> None:
        matrix = build_test_matrix(
            self.sample_targets,
            changed_files=["internal/storage/spanner/schema.go"],
            affected_targets_override=["//internal/storage/spanner:spanner_test"],
        )
        expected = [{"target": "internal/storage", "bazel_args": "//internal/storage/..."}]
        self.assertEqual(matrix, expected)

    def test_build_test_matrix_docs_only_returns_empty(self) -> None:
        matrix = build_test_matrix(
            self.sample_targets,
            changed_files=["docs/README.md"],
            affected_targets_override=[],
        )
        self.assertEqual(matrix, [])

    @patch("subprocess.check_output")
    def test_query_affected_test_targets(self, mock_subprocess) -> None:
        mock_subprocess.return_value = b"//cmd/actuator:test\n//internal/auth:test\n"
        targets = query_affected_test_targets(["internal/auth/auth.go"])
        self.assertEqual(targets, ["//cmd/actuator:test", "//internal/auth:test"])

    @patch("subprocess.check_output")
    def test_get_changed_files_fallback_to_none_on_git_error(self, mock_subprocess) -> None:
        mock_subprocess.side_effect = Exception("Git diff error")
        result = get_changed_files()
        self.assertIsNone(result)

    @patch("subprocess.check_output")
    def test_query_bazel_test_targets_exits_on_error(self, mock_subprocess) -> None:
        mock_subprocess.side_effect = Exception("Bazel query error")
        with self.assertRaises(SystemExit):
            query_bazel_test_targets()


if __name__ == "__main__":
    unittest.main()
