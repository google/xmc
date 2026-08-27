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

"""Test Scope Resolver for XMC CI Workflows.

Analyzes modified files from git diff or CLI arguments, enforces Inverted Default
Safety (ensuring new/unknown directories default to running backend tests),
scopes UI vs Backend test suites, and optionally runs Bazel rdeps query to discover
specific affected backend test targets.
"""

import argparse
import json
import subprocess
import sys
from pathlib import Path

CORE_INFRA_FILES: set[str] = {
    "go.mod",
    "go.sum",
    "WORKSPACE",
    "WORKSPACE.bazel",
    "MODULE.bazel",
    "MODULE.bazel.lock",
    "BUILD.bazel",
    "compat.bzl",
}

NON_CODE_PREFIXES: tuple[str, ...] = (
    "docs/",
    "deploy/",
    ".githooks/",
    ".agents/",
)

NON_CODE_FILES: set[str] = {
    "LICENSE",
    "OWNERS",
    ".gitignore",
    ".tflint.hcl",
    ".golangci.yml",
    "pyproject.toml",
    "test/__init__.py",
}


EXCLUDED_TEST_TARGETS: set[str] = {
    "//cmd/ui:ui_unit_tests",
}

CODE_EXTENSIONS: tuple[str, ...] = (
    ".go",
    ".py",
    ".proto",
    ".bzl",
    ".sql",
)

BUILD_FILE_NAMES: set[str] = {
    "BUILD.bazel",
    "BUILD",
}


def is_non_code_file(path_str: str) -> bool:
    """Returns True if the file path is a pure documentation, legal, or deployment artifact."""
    p = Path(path_str)
    if p.name in NON_CODE_FILES or p.suffix == ".md":
        return True
    if p.name in BUILD_FILE_NAMES or p.suffix in CODE_EXTENSIONS:
        return False
    return any(path_str.startswith(prefix) for prefix in NON_CODE_PREFIXES)


def classify_modified_files(files: list[str]) -> dict[str, bool]:
    """Classifies a list of modified relative file paths into domain flags.

    Enforces Inverted Default Safety: Any code file that is not explicitly inside
    cmd/ui/ is treated as a backend file. Unknown newly created backend directories
    automatically trigger backend tests. Non-frontend code files inside cmd/ui/
    (e.g., Python server, shell scripts, Bazel builds) trigger both UI and backend tests.
    """
    has_core_infra = False
    has_ui = False
    has_backend = False

    for f in files:
        clean_f = f.strip()
        if not clean_f:
            continue

        if clean_f in CORE_INFRA_FILES:
            has_core_infra = True

        if is_non_code_file(clean_f):
            continue

        if clean_f.startswith("cmd/ui/"):
            has_ui = True
            # Non-frontend code files in cmd/ui/ (Python server, shell, Bazel) require backend tests
            if (
                clean_f.endswith(CODE_EXTENSIONS)
                or clean_f.endswith(".sh")
                or Path(clean_f).name in BUILD_FILE_NAMES
            ):
                has_backend = True
        else:
            # Inverted Default Safety: Anything else is backend code!
            has_backend = True

    # Core infrastructure modifications trigger both backend and UI verification
    if has_core_infra:
        has_ui = True
        has_backend = True

    return {
        "has_core_infra": has_core_infra,
        "has_ui": has_ui,
        "has_backend": has_backend,
    }


def get_git_modified_files(base_ref: str | None = None, head_ref: str = "HEAD") -> list[str]:
    """Retrieves modified files using git diff against base_ref, falling back to git status."""
    try:
        if base_ref:
            cmd = ["git", "diff", "--name-only", f"{base_ref}...{head_ref}"]
        else:
            # Compare against HEAD~1 if no base provided
            cmd = ["git", "diff", "--name-only", "HEAD~1...HEAD"]

        result = subprocess.run(cmd, capture_output=True, text=True, check=False, timeout=15)
        if result.returncode == 0:
            return [line.strip().strip('"') for line in result.stdout.splitlines() if line.strip()]

        # Fallback to status for untracked/dirty files
        status_res = subprocess.run(
            ["git", "status", "--porcelain"],
            capture_output=True,
            text=True,
            check=True,
            timeout=10,
        )
        files = []
        for line in status_res.stdout.splitlines():
            if len(line) > 3:
                path = line[3:].strip()
                if " -> " in path:
                    path = path.split(" -> ", 1)[1]
                files.append(path.strip('"'))
        return files
    except Exception as e:
        print(f"Critical: Failed to determine modified files from git: {e}", file=sys.stderr)
        raise


def _parse_build_file_test_targets(build_path: Path) -> list[str]:
    """Parses non-manual test target names from a BUILD file using AST."""
    import ast

    try:
        content = build_path.read_text(encoding="utf-8", errors="ignore")
        tree = ast.parse(content, filename=str(build_path))
    except Exception:
        return []

    targets: list[str] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        func_name = getattr(node.func, "id", "") or getattr(node.func, "attr", "")
        if not func_name.endswith("_test"):
            continue

        name: str | None = None
        is_manual = False
        for kw in node.keywords:
            if kw.arg == "name" and isinstance(kw.value, ast.Constant):
                name = str(kw.value.value)
            elif kw.arg == "tags" and isinstance(kw.value, (ast.List, ast.Tuple, ast.Set)):
                for elt in kw.value.elts:
                    if isinstance(elt, ast.Constant) and elt.value == "manual":
                        is_manual = True

        if name and not is_manual:
            targets.append(name)
    return targets


def find_enclosing_bazel_package(file_path: str, cwd: Path | None = None) -> str | None:
    """Finds the nearest enclosing Bazel package directory containing a BUILD or BUILD.bazel file.

    Returns the package label relative to workspace root (e.g. '//internal/storage/spanner/migrations'
    or '//' for the root package). If no BUILD file is found on disk (e.g. synthetic or mocked paths),
    falls back to the file's parent directory label.
    """
    base_dir = (cwd if cwd else Path.cwd()).resolve()
    target_path = Path(file_path)

    if not target_path.is_absolute():
        target_path = base_dir / target_path
    try:
        rel_target = target_path.resolve().relative_to(base_dir)
    except ValueError:
        return None

    # Start searching from the file's directory upwards
    curr_dir = rel_target.parent

    while True:
        candidate_dir = base_dir / curr_dir

        if (candidate_dir / "BUILD.bazel").is_file() or (candidate_dir / "BUILD").is_file():
            rel_pkg = curr_dir.as_posix()
            if rel_pkg in {".", ""}:
                return "//"
            return f"//{rel_pkg}"

        if curr_dir in {Path("."), Path("")}:
            break
        curr_dir = curr_dir.parent

    if (base_dir / "BUILD.bazel").is_file() or (base_dir / "BUILD").is_file():
        return "//"

    # Fallback for synthetic/mock paths or environments where repository tree is not on disk
    parent_posix = rel_target.parent.as_posix()
    if parent_posix in {".", ""}:
        return "//"
    return f"//{parent_posix}"


def _discover_static_rdeps_targets(files: list[str], cwd: Path | None = None) -> list[str]:
    """Fallback static parser for reverse dependency test targets from BUILD.bazel files."""
    import os

    base_dir = (cwd if cwd else Path.cwd()).resolve()
    changed_dirs: set[str] = set()
    for f in files:
        clean_f = f.strip()
        if not clean_f or is_non_code_file(clean_f):
            continue
        if not clean_f.endswith(CODE_EXTENSIONS) and Path(clean_f).name not in BUILD_FILE_NAMES:
            continue

        pkg = find_enclosing_bazel_package(clean_f, cwd=base_dir)
        if pkg:
            pkg_dir = pkg.lstrip("/").strip("/")
            changed_dirs.add(pkg_dir)

    if not changed_dirs:
        return []

    test_targets: set[str] = set()
    for root, _, filenames in os.walk(base_dir):
        if "BUILD.bazel" in filenames:
            rel_root = Path(root).relative_to(base_dir).as_posix()
            if "node_modules" in rel_root:
                continue
            build_path = Path(root) / "BUILD.bazel"
            target_names = _parse_build_file_test_targets(build_path)

            for target_name in target_names:
                pkg_path = "" if rel_root == "." else rel_root
                target_label = f"//{pkg_path}:{target_name}" if pkg_path else f"//:{target_name}"
                if target_label in EXCLUDED_TEST_TARGETS:
                    continue
                for cd in changed_dirs:
                    if (
                        pkg_path == cd
                        or (cd and pkg_path.startswith(f"{cd}/"))
                        or (pkg_path and cd.startswith(f"{pkg_path}/"))
                    ):
                        test_targets.add(target_label)
    return sorted(test_targets)


def discover_bazel_rdeps_targets(files: list[str], cwd: Path | None = None) -> list[str]:
    """Runs Bazel query to discover unit test targets that transitively depend on changed packages.

    Returns empty list if Bazel is unavailable or query fails.
    """
    changed_pkgs: set[str] = set()
    for f in files:
        clean_f = f.strip()
        if not clean_f or is_non_code_file(clean_f):
            continue
        if not clean_f.endswith(CODE_EXTENSIONS) and Path(clean_f).name not in BUILD_FILE_NAMES:
            continue

        pkg = find_enclosing_bazel_package(clean_f, cwd=cwd)
        if pkg:
            pkg_target = f"{pkg}:all" if pkg != "//" else "//:all"
            changed_pkgs.add(pkg_target)

    if not changed_pkgs:
        return []

    pkg_set_expr = " ".join(sorted(changed_pkgs))
    backend_universe = "//... - //cmd/ui/..."
    query_expr = (
        f"let t = kind('.*_test', rdeps({backend_universe}, set({pkg_set_expr}))) "
        f"in $t except attr('tags', 'manual', $t)"
    )

    try:
        res = subprocess.run(
            [
                "bazel",
                "query",
                "--fetch=false",
                "--tool_deps",
                "--noimplicit_deps",
                "--keep_going",
                query_expr,
            ],
            capture_output=True,
            text=True,
            check=False,
            timeout=30,
            cwd=cwd,
        )
        if res.stdout.strip():
            targets = [t.strip() for t in res.stdout.splitlines() if t.strip().startswith("//")]
            return [t for t in targets if t not in EXCLUDED_TEST_TARGETS]
    except Exception:
        pass

    return _discover_static_rdeps_targets(files, cwd=cwd)


def resolve_test_scope(
    files: list[str],
    event_name: str = "pull_request",
    ref: str = "",
    use_rdeps: bool = True,
    cwd: Path | None = None,
) -> dict[str, object]:
    """Main entry point for test scope resolution."""
    # Main branch push events always run full regression suites
    if event_name == "push" and ref in {"refs/heads/main", "main"}:
        return {
            "run_backend": True,
            "run_ui": True,
            "backend_mode": "all",
            "bazel_targets": "//... -//cmd/ui/...",
            "reason": "main branch push full regression",
        }

    classification = classify_modified_files(files)
    has_core_infra = classification["has_core_infra"]
    has_ui = classification["has_ui"]
    has_backend = classification["has_backend"]

    if not has_ui and not has_backend:
        return {
            "run_backend": False,
            "run_ui": False,
            "backend_mode": "none",
            "bazel_targets": "",
            "reason": "only non-code or documentation files modified",
        }

    run_ui = has_ui
    run_backend = has_backend

    if not run_backend:
        return {
            "run_backend": False,
            "run_ui": run_ui,
            "backend_mode": "none",
            "bazel_targets": "",
            "reason": "isolated frontend UI modifications",
        }

    if has_core_infra or not use_rdeps:
        return {
            "run_backend": True,
            "run_ui": run_ui,
            "backend_mode": "all",
            "bazel_targets": "//... -//cmd/ui/...",
            "reason": "core infra modified or rdeps disabled",
        }

    # Attempt fine-grained rdeps scoping for backend changes
    rdeps_targets = discover_bazel_rdeps_targets(files, cwd=cwd)
    if rdeps_targets:
        return {
            "run_backend": True,
            "run_ui": run_ui,
            "backend_mode": "rdeps",
            "bazel_targets": " ".join(rdeps_targets),
            "reason": f"discovered {len(rdeps_targets)} affected bazel test targets",
        }

    # Safe fallback if rdeps query returns empty or fails
    return {
        "run_backend": True,
        "run_ui": run_ui,
        "backend_mode": "all",
        "bazel_targets": "//... -//cmd/ui/...",
        "reason": "fallback to full backend suite for modified files",
    }


def write_github_output(github_output_path: str, result: dict[str, object]) -> None:
    """Formats key-value outputs to $GITHUB_OUTPUT file for GitHub Actions."""
    path = Path(github_output_path)
    with path.open("a", encoding="utf-8") as f:
        f.write(f"run_backend={str(result['run_backend']).lower()}\n")
        f.write(f"run_ui={str(result['run_ui']).lower()}\n")
        f.write(f"backend_mode={result['backend_mode']}\n")
        f.write(f"bazel_targets={result['bazel_targets']}\n")
        f.write(f"reason={result['reason']}\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--files", nargs="*", help="Optional explicit list of modified file paths.")
    parser.add_argument("--base", help="Git diff base reference.")
    parser.add_argument("--head", default="HEAD", help="Git diff head reference.")
    parser.add_argument("--event", default="pull_request", help="GitHub event name.")
    parser.add_argument("--ref", default="", help="GitHub ref name.")
    parser.add_argument(
        "--no-rdeps",
        action="store_true",
        default=False,
        dest="disable_rdeps",
        help="Disable Bazel rdeps scoping.",
    )
    parser.add_argument("--github-output", help="Path to $GITHUB_OUTPUT file to write flags.")
    args = parser.parse_args()

    if args.files is not None:
        modified_files = args.files
    else:
        modified_files = get_git_modified_files(base_ref=args.base, head_ref=args.head)

    print(f"DEBUG modified_files: {modified_files}", file=sys.stderr)

    result = resolve_test_scope(
        files=modified_files,
        event_name=args.event,
        ref=args.ref,
        use_rdeps=not args.disable_rdeps,
    )

    print(f"DEBUG result: {result}", file=sys.stderr)

    if args.github_output:
        write_github_output(args.github_output, result)

    # Output human-readable JSON summary to stdout
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
