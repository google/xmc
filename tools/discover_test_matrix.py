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

"""Discovers unit test targets and generates a matrix for GitHub Actions."""

import json
import os
import subprocess
from typing import Dict, List, Set


def extract_root_folders(test_targets: List[str]) -> Set[str]:
    """Extracts unique top-level root folders from Bazel test target paths."""
    top_level: Set[str] = set()
    for target in test_targets:
        clean_target = target.removeprefix("//")
        pkg_path = clean_target.split(":")[0]
        parts = pkg_path.split("/")
        root_folder = parts[0] if parts[0] else "root"
        top_level.add(root_folder)
    return top_level


def build_test_matrix(test_targets: List[str]) -> List[Dict[str, str]]:
    """Builds matrix entries from Bazel test targets with custom splits."""
    top_level_folders = extract_root_folders(test_targets)
    matrix: List[Dict[str, str]] = []

    if "root" in top_level_folders:
        matrix.append({"target": "root", "bazel_args": "//:all"})

    for folder in sorted(top_level_folders):
        if folder == "root":
            continue
        elif folder == "cmd":
            # Isolate UI tests because only cmd/ui requires Node.js/npm dependencies.
            # cmd/rest contains pure Go binaries that skip Node.js installation.
            matrix.extend(
                [
                    {"target": "cmd/ui", "bazel_args": "//cmd/ui/..."},
                    {"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."},
                ]
            )
        elif folder == "internal":
            # Isolate storage tests because only internal/storage requires Cloud Spanner
            # emulator initialization. internal/rest contains fast in-memory Go tests.
            matrix.extend(
                [
                    {"target": "internal/storage", "bazel_args": "//internal/storage/..."},
                    {
                        "target": "internal/rest",
                        "bazel_args": "//internal/... -//internal/storage/...",
                    },
                ]
            )
        else:
            matrix.append({"target": folder, "bazel_args": f"//{folder}/..."})

    return matrix


def query_bazel_test_targets() -> List[str]:
    """Queries Bazel for all test targets in the workspace."""
    try:
        out = subprocess.check_output(["bazel", "query", 'kind(".*_test", //...)']).decode("utf-8")
        return [line.strip() for line in out.strip().split("\n") if line.strip()]
    except subprocess.CalledProcessError as e:
        print(f"Error querying Bazel test targets: {e}")
        return []


def main() -> None:
    """Main entry point for discovering test matrix."""
    targets = query_bazel_test_targets()
    matrix = build_test_matrix(targets)
    matrix_json = json.dumps(matrix)

    print(f"Discovered Matrix: {matrix_json}")
    github_output = os.getenv("GITHUB_OUTPUT")
    if github_output:
        with open(github_output, "a", encoding="utf-8") as f:
            f.write(f"matrix={matrix_json}\n")


if __name__ == "__main__":
    main()
