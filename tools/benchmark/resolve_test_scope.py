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
    ".bazelrc",
    ".bazelversion",
    ".github/workflows/unit-tests.yml",
    ".github/actions/run-backend-tests/action.yml",
    "tools/benchmark/resolve_test_scope.py",
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
    ".sh",
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

        if clean_f.startswith(".github/actions/run-ui-tests/"):
            has_ui = True
            continue

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


def _get_merge_base(ref_a: str, ref_b: str) -> str | None:
    """Returns the merge-base commit SHA between ref_a and ref_b, or None on failure."""
    res = subprocess.run(
        ["git", "merge-base", ref_a, ref_b],
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    if res.returncode != 0:
        return None
    return res.stdout.strip() or None


def _normalize_subject(subject: str) -> str:
    """Strips trailing PR number suffixes like ' (#123)' from commit subjects."""
    s = subject.strip()
    if s.endswith(")") and " (#" in s:
        prefix, _, suffix = s.rpartition(" (#")
        if suffix[:-1].isdigit():
            return prefix.strip()
    return s


def _correlate_rebased_parent_tip(
    parent_base: str,
    target_ref: str,
    candidate_base: str,
    head_ref: str,
) -> str:
    """Advances candidate_base past rebased/squash-merged parent commits matching target_ref."""
    try:
        parent_log = subprocess.run(
            ["git", "log", "--format=%s", f"{parent_base}..{target_ref}"],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        if parent_log.returncode != 0 or not parent_log.stdout.strip():
            return candidate_base

        parent_subjects = {
            _normalize_subject(line) for line in parent_log.stdout.splitlines() if line.strip()
        }
        head_log = subprocess.run(
            ["git", "log", "--reverse", "--format=%H %s", f"{candidate_base}..{head_ref}"],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        if head_log.returncode != 0 or not head_log.stdout.strip():
            return candidate_base
    except Exception:
        return candidate_base

    commits = [line.strip().split(" ", 1) for line in head_log.stdout.splitlines() if line.strip()]
    correlated = candidate_base
    for parts in commits[:-1]:
        if len(parts) == 2 and _normalize_subject(parts[1]) in parent_subjects:
            correlated = parts[0]
        else:
            break
    return correlated


def resolve_effective_base_ref(
    base_ref: str,
    head_ref: str = "HEAD",
    base_branch: str | None = None,
) -> str:
    """Resolves the closest merge-base across origin/<base_branch>, base_ref, and origin/main.

    Prioritizes the live target branch ref (origin/<base_branch>) over webhook base_ref so
    commits moved down a stack are not dropped, excludes merge-bases equal to head_ref, and
    correlates rewritten parent commits across gh-stack rebases and squash-merges.
    """
    clean_branch = (base_branch or "").strip()
    if not clean_branch and base_ref in {"origin/main", "main", "HEAD~1"}:
        return base_ref

    is_stacked_branch = bool(clean_branch and clean_branch not in {"main", "origin/main"})
    target_ref = base_ref
    mb_target = None
    if is_stacked_branch:
        branch_ref = f"origin/{clean_branch}"
        mb_target = _get_merge_base(branch_ref, head_ref)
        if mb_target:
            target_ref = branch_ref
    if not mb_target:
        mb_target = _get_merge_base(base_ref, head_ref)

    mb_main = (
        _get_merge_base("origin/main", head_ref)
        if base_ref not in {"origin/main", "main"}
        else None
    )
    merge_bases = list(dict.fromkeys(mb for mb in (mb_target, mb_main) if mb and mb != head_ref))
    if not merge_bases:
        return base_ref

    candidate = merge_bases[0]
    if len(merge_bases) > 1:
        indep = subprocess.run(
            ["git", "merge-base", "--independent", *merge_bases],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        indep_set = (
            {line.strip() for line in indep.stdout.splitlines() if line.strip()}
            if indep.returncode == 0
            else set()
        )
        for mb in merge_bases:
            if mb in indep_set:
                candidate = mb
                break

    if is_stacked_branch and candidate == mb_main:
        return _correlate_rebased_parent_tip(
            mb_target or candidate, target_ref, candidate, head_ref
        )
    if clean_branch in {"main", "origin/main"} and candidate != base_ref:
        return _correlate_rebased_parent_tip(candidate, base_ref, candidate, head_ref)
    return candidate


def get_git_modified_files(
    base_ref: str | None = None,
    head_ref: str = "HEAD",
    base_branch: str | None = None,
) -> list[str]:
    """Retrieves modified files using git diff against base_ref, falling back to git status."""
    try:
        effective_base = "HEAD~1"
        if base_ref:
            effective_base = resolve_effective_base_ref(base_ref, head_ref, base_branch)
            cmd = ["git", "diff", "--name-only", f"{effective_base}...{head_ref}"]
        else:
            cmd = ["git", "diff", "--name-only", "HEAD~1...HEAD"]

        result = subprocess.run(cmd, capture_output=True, text=True, check=False, timeout=15)
        if result.returncode != 0 and base_ref and head_ref != "HEAD":
            result = subprocess.run(
                ["git", "diff", "--name-only", f"{effective_base}...HEAD"],
                capture_output=True,
                text=True,
                check=False,
                timeout=15,
            )
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


def discover_bazel_rdeps_targets(files: list[str], cwd: Path | None = None) -> list[str]:
    """Runs Bazel query to discover unit test targets that transitively depend on changed packages.

    Returns empty list if Bazel is unavailable or query fails.
    """
    changed_pkgs: set[str] = set()
    for f in files:
        clean_f = f.strip()
        if not clean_f or is_non_code_file(clean_f):
            continue
        is_code_or_build = (
            clean_f.endswith(CODE_EXTENSIONS) or Path(clean_f).name in BUILD_FILE_NAMES
        )
        if clean_f.startswith("cmd/ui/") and not is_code_or_build:
            continue

        pkg = find_enclosing_bazel_package(clean_f, cwd=cwd)
        if not is_code_or_build and pkg in {None, "//"}:
            return []
        if pkg:
            pkg_target = f"{pkg}:all" if pkg != "//" else "//:all"
            changed_pkgs.add(pkg_target)

    if not changed_pkgs:
        return []

    pkg_set_expr = " ".join(sorted(changed_pkgs))
    backend_universe = "//... except //cmd/ui/..."
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
        if res.returncode == 0 and res.stdout.strip():
            targets = [t.strip() for t in res.stdout.splitlines() if t.strip().startswith("//")]
            return [t for t in targets if t not in EXCLUDED_TEST_TARGETS]
        if res.returncode != 0:
            print(
                f"Warning: bazel query failed with exit code {res.returncode}: {res.stderr.strip()}",
                file=sys.stderr,
            )
    except Exception as e:
        print(f"Warning: bazel query encountered exception: {e}", file=sys.stderr)

    # Return empty list on failure so resolve_test_scope safely falls back to the full test suite
    return []


def resolve_test_scope(
    files: list[str],
    event_name: str = "pull_request",
    ref: str = "",
    use_rdeps: bool = True,
    cwd: Path | None = None,
) -> dict[str, object]:
    """Main entry point for test scope resolution."""
    # Main branch push / dispatch / schedule events always run full regression suites
    if event_name in {"push", "workflow_dispatch", "schedule"} and ref in {
        "refs/heads/main",
        "main",
        "",
    }:
        return {
            "run_backend": True,
            "run_ui": True,
            "backend_mode": "all",
            "bazel_targets": "//... -//cmd/ui/...",
            "reason": "main branch push full regression",
        }

    if not any(f.strip() for f in files):
        return {
            "run_backend": True,
            "run_ui": True,
            "backend_mode": "all",
            "bazel_targets": "//... -//cmd/ui/...",
            "reason": "no modified files detected; defaulting to full test suite for safety",
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
    parser.add_argument("--base-branch", default="", help="Git diff base branch name.")
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
        modified_files = get_git_modified_files(
            base_ref=args.base,
            head_ref=args.head,
            base_branch=args.base_branch,
        )

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
