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


def convert_files_to_bazel_targets(changed_files: list[str]) -> list[str]:
    """Converts git changed file paths to Bazel package pattern targets.

    For example:
    - 'internal/auth/auth.go' -> '//internal/auth/...'
    - 'cmd/ui/src/app.ts' -> '//cmd/ui/...'
    - 'README.md' -> '//:all'
    """
    targets: set[str] = set()
    for filepath in changed_files:
        filepath = filepath.strip()
        dirname = os.path.dirname(filepath)
        if not dirname:
            targets.add("//:all")
            continue

        curr = dirname
        found = False
        while curr and curr != ".":
            if os.path.exists(os.path.join(curr, "BUILD.bazel")) or os.path.exists(
                os.path.join(curr, "BUILD")
            ):
                targets.add(f"//{curr}/...")
                found = True
                break
            curr = os.path.dirname(curr)

        if not found:
            targets.add(f"//{dirname}/...")

    return sorted(targets)


def query_affected_test_targets(changed_files: list[str]) -> list[str] | None:
    """Queries Bazel for test targets affected by changed files using rdeps.

    Returns None if global changes are detected or if query execution fails
    (forcing fallback to full matrix discovery).
    """
    if is_global_change(changed_files):
        return None

    pkg_targets = convert_files_to_bazel_targets(changed_files)
    if not pkg_targets or "//:all" in pkg_targets:
        return None

    query_targets = " ".join(f'"{t}"' for t in pkg_targets)
    query_expr = f'kind(".*_test", rdeps(//..., set({query_targets})))'
    try:
        out = subprocess.check_output(["bazel", "query", query_expr]).decode("utf-8")
        return [line.strip() for line in out.strip().split("\n") if line.strip()]
    except Exception as e:
        print(
            f"Warning: Bazel rdeps query failed: {e}. Falling back to full matrix.", file=sys.stderr
        )
        return None


def map_test_targets_to_matrix(test_targets: list[str]) -> list[dict[str, str]]:
    """Maps a list of Bazel test targets to matrix execution entries."""
    categories: set[str] = set()
    for target in test_targets:
        clean_target = target.removeprefix("//")
        if clean_target.startswith("cmd/ui/") or clean_target.startswith("cmd/ui:"):
            categories.add("cmd/ui")
        elif clean_target.startswith("cmd/"):
            categories.add("cmd/rest")
        elif clean_target.startswith("internal/storage/"):
            categories.add("internal/storage")
        elif clean_target.startswith("internal/"):
            categories.add("internal/rest")
        else:
            pkg_path = clean_target.split(":")[0]
            root_folder = (
                pkg_path.split("/")[0] if "/" in pkg_path else (pkg_path if pkg_path else "root")
            )
            categories.add(root_folder)

    matrix: list[dict[str, str]] = []
    sorted_cats = sorted(categories)
    if "root" in sorted_cats:
        sorted_cats.remove("root")
        sorted_cats.insert(0, "root")

    for cat in sorted_cats:
        if cat == "cmd/ui":
            matrix.append({"target": "cmd/ui", "bazel_args": "//cmd/ui/..."})
        elif cat == "cmd/rest":
            matrix.append({"target": "cmd/rest", "bazel_args": "//cmd/... -//cmd/ui/..."})
        elif cat == "internal/storage":
            matrix.append({"target": "internal/storage", "bazel_args": "//internal/storage/..."})
        elif cat == "internal/rest":
            matrix.append(
                {"target": "internal/rest", "bazel_args": "//internal/... -//internal/storage/..."}
            )
        elif cat == "root":
            matrix.append({"target": "root", "bazel_args": "//:all"})
        else:
            matrix.append({"target": cat, "bazel_args": f"//{cat}/..."})

    return matrix


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


def build_test_matrix(
    test_targets: list[str],
    changed_files: list[str] | None = None,
    affected_targets_override: list[str] | None = None,
) -> list[dict[str, str]]:
    """Builds matrix entries for Bazel test targets, filtering out unaffected modules."""
    if changed_files is None:
        return map_test_targets_to_matrix(test_targets)

    if is_global_change(changed_files):
        return map_test_targets_to_matrix(test_targets)

    affected_test_targets: list[str] | None = None
    if affected_targets_override is not None:
        affected_test_targets = affected_targets_override
    else:
        affected_test_targets = query_affected_test_targets(changed_files)

    if affected_test_targets is None:
        return map_test_targets_to_matrix(test_targets)

    return map_test_targets_to_matrix(affected_test_targets)


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
