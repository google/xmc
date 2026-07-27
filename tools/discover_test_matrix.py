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

"""Discovers unit test targets and generates an affected test matrix for GitHub Actions."""

import fnmatch
import json
import os
import subprocess
import sys

# Global change patterns trigger full test suite runs because infrastructure, build rules,
# or shared tool changes can impact any module across the codebase.
GLOBAL_CHANGE_PATTERNS = [
    ".github/*",
    ".github/workflows/*",
    "WORKSPACE",
    "MODULE.bazel*",
    "go.mod",
    "go.sum",
    "BUILD.bazel",
    "tools/*",
    "*.bzl",
]


def is_global_change(changed_files: list[str]) -> bool:
    """Returns True if modified files include global CI or build system configurations."""
    for filepath in changed_files:
        for pattern in GLOBAL_CHANGE_PATTERNS:
            if fnmatch.fnmatch(filepath, pattern) or filepath.startswith("tools/"):
                return True
    return False


def get_changed_files(base_ref: str = "origin/main") -> list[str] | None:
    """Retrieves modified file paths relative to base_ref.

    Returns None if git diff fails or cannot be determined (forcing a full test run).
    """
    try:
        out = subprocess.check_output(["git", "diff", "--name-only", f"{base_ref}...HEAD"]).decode(
            "utf-8"
        )
        return [line.strip() for line in out.strip().split("\n") if line.strip()]
    except Exception as e:
        print(f"Warning: Failed to execute 'git diff {base_ref}...HEAD': {e}")

    try:
        out = subprocess.check_output(["git", "diff", "--name-only", "HEAD~1", "HEAD"]).decode(
            "utf-8"
        )
        return [line.strip() for line in out.strip().split("\n") if line.strip()]
    except Exception as e:
        print(f"Warning: Failed to execute 'git diff HEAD~1 HEAD': {e}")

    print(
        "Warning: Could not determine changed files via git diff. Falling back to full test matrix."
    )
    return None


def extract_root_folders(test_targets: list[str]) -> set[str]:
    """Extracts unique top-level root folders from Bazel test target paths."""
    top_level: set[str] = set()
    for target in test_targets:
        clean_target = target.removeprefix("//")
        pkg_path = clean_target.split(":")[0]
        parts = pkg_path.split("/")
        root_folder = parts[0] if parts[0] else "root"
        top_level.add(root_folder)
    return top_level


def _build_cmd_matrix_entries(
    folder_files: list[str] | None,
) -> list[dict[str, str]]:
    """Splits cmd into UI and backend runners to skip Node.js setup for backend Go binaries."""
    ui_affected = folder_files is None or any(f.startswith("cmd/ui/") for f in folder_files)
    rest_affected = folder_files is None or any(not f.startswith("cmd/ui/") for f in folder_files)

    entries: list[dict[str, str]] = []
    if ui_affected:
        entries.append({"target": "cmd/ui", "bazel_args": "//cmd/ui/..."})
    if rest_affected:
        entries.append({"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."})
    return entries


def _build_internal_matrix_entries(
    folder_files: list[str] | None,
) -> list[dict[str, str]]:
    """Splits internal into storage and rest to isolate Spanner emulator checks to DB tests."""
    storage_affected = folder_files is None or any(
        f.startswith("internal/storage/") for f in folder_files
    )
    rest_affected = folder_files is None or any(
        not f.startswith("internal/storage/") for f in folder_files
    )

    entries: list[dict[str, str]] = []
    if storage_affected:
        entries.append({"target": "internal/storage", "bazel_args": "//internal/storage/..."})
    if rest_affected:
        entries.append(
            {
                "target": "internal/rest",
                "bazel_args": "//internal/... -//internal/storage/...",
            }
        )
    return entries


def _filter_affected_files(
    changed_files: list[str] | None,
) -> set[str] | None:
    """Determines affected files set; returns None if a full test run is required."""
    if changed_files is not None and not is_global_change(changed_files):
        return set(changed_files)
    return None


def build_test_matrix(
    test_targets: list[str], changed_files: list[str] | None = None
) -> list[dict[str, str]]:
    """Builds matrix entries for Bazel test targets, filtering out unaffected modules."""
    top_level_folders = extract_root_folders(test_targets)
    affected_files_set = _filter_affected_files(changed_files)
    matrix: list[dict[str, str]] = []

    if "root" in top_level_folders and (
        affected_files_set is None
        or any(f.count("/") == 0 or f.startswith(".") for f in affected_files_set)
    ):
        matrix.append({"target": "root", "bazel_args": "//:all"})

    for folder in sorted(top_level_folders):
        if folder == "root":
            continue

        if affected_files_set is not None:
            folder_files = [f for f in affected_files_set if f.startswith(f"{folder}/")]
            if not folder_files:
                continue
        else:
            folder_files = None

        if folder == "cmd":
            matrix.extend(_build_cmd_matrix_entries(folder_files))
        elif folder == "internal":
            matrix.extend(_build_internal_matrix_entries(folder_files))
        else:
            matrix.append({"target": folder, "bazel_args": f"//{folder}/..."})

    return matrix


def query_bazel_test_targets() -> list[str]:
    """Queries Bazel for all test targets in the workspace."""
    try:
        out = subprocess.check_output(["bazel", "query", 'kind(".*_test", //...)']).decode("utf-8")
        targets = [line.strip() for line in out.strip().split("\n") if line.strip()]
        if not targets:
            print("ERROR: Bazel query returned zero test targets.", file=sys.stderr)
            sys.exit(1)
        return targets
    except Exception as e:
        print(f"ERROR: Bazel query failed: {e}", file=sys.stderr)
        sys.exit(1)


def main() -> None:
    """Main entry point for discovering test matrix."""
    targets = query_bazel_test_targets()
    changed_files = get_changed_files()
    if changed_files is None:
        print("Changed files: Undetermined (Git diff unavailable -> Running full test matrix)")
    else:
        print(f"Changed files count: {len(changed_files)}")

    matrix = build_test_matrix(targets, changed_files=changed_files)
    matrix_json = json.dumps(matrix)

    print(f"Discovered Matrix ({len(matrix)} targets): {matrix_json}")
    github_output = os.getenv("GITHUB_OUTPUT")
    if github_output:
        with open(github_output, "a", encoding="utf-8") as f:
            f.write(f"matrix={matrix_json}\n")


if __name__ == "__main__":
    main()
