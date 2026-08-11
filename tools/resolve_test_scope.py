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
import subprocess
from pathlib import Path

CORE_INFRA_FILES: set[str] = {
    "go.mod",
    "go.sum",
    "WORKSPACE",
    "MODULE.bazel",
    "BUILD.bazel",
    ".github/workflows/unit-tests.yml",
    "tools/resolve_test_scope.py",
}

NON_CODE_PREFIXES: tuple[str, ...] = (
    "docs/",
    "deploy/",
    ".githooks/",
    ".agents/",
)

NON_CODE_FILES: set[str] = {
    "README.md",
    "INSTALL.md",
    "CONTRIBUTING.md",
    "LICENSE",
    "OWNERS",
    "ABSENCE.md",
    ".gitignore",
    ".tflint.hcl",
    ".golangci.yml",
    "pyproject.toml",
}


def is_non_code_file(path_str: str) -> bool:
    """Returns True if the file path is a pure documentation, legal, or deployment artifact."""
    p = Path(path_str)
    if p.name in NON_CODE_FILES or p.suffix in {".md", ".txt"}:
        return True
    return any(path_str.startswith(prefix) for prefix in NON_CODE_PREFIXES)


def classify_modified_files(files: list[str]) -> dict[str, bool]:
    """Classifies a list of modified relative file paths into domain flags.

    Enforces Inverted Default Safety: Any code file that is not explicitly inside
    cmd/ui/ is treated as a backend file. Unknown newly created backend directories
    automatically trigger backend tests.
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
            return [line.strip() for line in result.stdout.splitlines() if line.strip()]

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
                files.append(line[3:].strip())
        return files
    except Exception:
        return []


def discover_bazel_rdeps_targets(files: list[str], cwd: Path | None = None) -> list[str]:
    """Runs Bazel query to discover unit test targets that transitively depend on changed Go packages.

    Returns empty list if Bazel is unavailable or query fails.
    """
    go_packages: set[str] = set()
    for f in files:
        if f.endswith(".go") and not f.startswith("cmd/ui/"):
            pkg_dir = Path(f).parent.as_posix()
            if pkg_dir == "." or not pkg_dir:
                go_packages.add("//...")
            else:
                go_packages.add(f"//... -//{pkg_dir}/...")

    # More accurately: build set of packages for modified files
    changed_pkgs: set[str] = set()
    for f in files:
        if f.endswith(".go") and not f.startswith("cmd/ui/"):
            pkg_dir = Path(f).parent.as_posix()
            if pkg_dir and pkg_dir != ".":
                changed_pkgs.add(f"//{pkg_dir}")

    if not changed_pkgs:
        return []

    pkg_set_expr = " ".join(sorted(changed_pkgs))
    query_expr = f"kind('.*_test', rdeps(//..., set({pkg_set_expr})))"

    try:
        res = subprocess.run(
            ["bazel", "query", query_expr],
            capture_output=True,
            text=True,
            check=False,
            timeout=30,
            cwd=cwd,
        )
        if res.returncode == 0:
            targets = [t.strip() for t in res.stdout.splitlines() if t.strip()]
            # Filter out any accidental ui targets if present
            return [t for t in targets if not t.startswith("//cmd/ui")]
    except Exception:
        pass

    return []


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


def write_github_output(github_output_path: str, result: dict[str, object]):
    """Formats key-value outputs to $GITHUB_OUTPUT file for GitHub Actions."""
    path = Path(github_output_path)
    with path.open("a", encoding="utf-8") as f:
        f.write(f"run_backend={str(result['run_backend']).lower()}\n")
        f.write(f"run_ui={str(result['run_ui']).lower()}\n")
        f.write(f"backend_mode={result['backend_mode']}\n")
        f.write(f"bazel_targets={result['bazel_targets']}\n")
        f.write(f"reason={result['reason']}\n")


def main():
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

    result = resolve_test_scope(
        files=modified_files,
        event_name=args.event,
        ref=args.ref,
        use_rdeps=not args.disable_rdeps,
    )

    if args.github_output:
        write_github_output(args.github_output, result)

    # Output human-readable JSON summary to stdout
    import json

    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
