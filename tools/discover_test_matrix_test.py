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

from tools.discover_test_matrix import build_test_matrix, extract_root_folders


class DiscoverTestMatrixTest(unittest.TestCase):
    def test_extract_root_folders(self) -> None:
        targets = [
            "//:root_test",
            "//cmd/actuator:workunittaskhandler_test",
            "//internal/auth:auth_test",
            "//pkg/auth/oauth2:oauth2_test",
            "//tools:build_oci_test",
        ]
        roots = extract_root_folders(targets)
        self.assertEqual(roots, {"root", "cmd", "internal", "pkg", "tools"})

    def test_build_test_matrix_splits_cmd_and_internal(self) -> None:
        targets = [
            "//:root_test",
            "//cmd/actuator:workunittaskhandler_test",
            "//cmd/ui:server_test",
            "//deploy/cli:install_xmanager_test",
            "//internal/auth:auth_test",
            "//internal/storage/spanner:spanner_test",
            "//pkg/auth/oauth2:oauth2_test",
            "//tools:build_oci_test",
        ]
        matrix = build_test_matrix(targets)
        expected = [
            {"target": "root", "bazel_args": "//:all"},
            {"target": "cmd/ui", "bazel_args": "//cmd/ui/..."},
            {"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."},
            {"target": "deploy", "bazel_args": "//deploy/..."},
            {"target": "internal/storage", "bazel_args": "//internal/storage/..."},
            {
                "target": "internal/rest",
                "bazel_args": "//internal/... -//internal/storage/...",
            },
            {"target": "pkg", "bazel_args": "//pkg/..."},
            {"target": "tools", "bazel_args": "//tools/..."},
        ]
        self.assertEqual(matrix, expected)


if __name__ == "__main__":
    unittest.main()
