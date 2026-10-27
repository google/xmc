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
specific affected backend test targets. It also reports whether the Helm chart suite
is affected. With --local it scopes the local checkout (uncommitted work included)
the way a pull request is scoped.
"""

import argparse
import json
import os
import subprocess
import sys
from collections import Counter
from collections.abc import Mapping
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.changed_files import ChangedFilesError, list_changed_files  # noqa: E402

CORE_INFRA_FILES: set[str] = {
    "go.mod",
    "go.sum",
    "WORKSPACE",
    "WORKSPACE.bazel",
    "MODULE.bazel",
    "MODULE.bazel.lock",
    "BUILD.bazel",
    ".bazelrc",
    ".bazelversion",
    ".github/workflows/unit-tests.yml",
    ".github/actions/run-backend-tests/action.yml",
    # The backend CI action runs its Bazel tests through this entrypoint and its shared modules.
    "tools/run_affected_tests.py",
    "tools/benchmark/resolve_test_scope.py",
    "tools/common/changed_files.py",
    "tools/common/tool_utils.py",
    # Generates the dev_binary_guard_test for every container image.
    "oci.bzl",
}

# The Helm chart suite (.github/actions/run-chart-tests) renders templates with a host helm
# binary rather than building Bazel targets, so its scope is resolved by path rather than by
# the Bazel dependency graph.
HELM_SUITE_PREFIXES: tuple[str, ...] = (
    "deploy/helm/",
    "test/helm/",
    ".github/actions/run-chart-tests/",
)

HELM_SUITE_FILES: set[str] = {
    # The suite also asserts the chart agrees with the deployment blueprint, so a
    # blueprint-only edit can break it just as a chart-only edit can.
    "deploy/ctk_v2/xmanager-cloud.yaml",
    ".github/workflows/unit-tests.yml",
    # The chart job gates on this resolver's run_helm output.
    "tools/benchmark/resolve_test_scope.py",
}

DEFAULT_LOCAL_BASE_REF = "origin/main"

# A fresh CI runner fetches every external repository during its first query; the restored
# repository cache keeps that well under this bound.
RDEPS_QUERY_TIMEOUT_SECONDS = 600

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


def touches_helm_suite(files: list[str]) -> bool:
    """Returns True if any modified file can affect the Helm chart configuration suite."""
    return any(
        f.strip() in HELM_SUITE_FILES or f.strip().startswith(HELM_SUITE_PREFIXES) for f in files
    )


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


def _is_ancestor(ancestor_ref: str, descendant_ref: str) -> bool:
    """Returns True if ancestor_ref is an ancestor of descendant_ref."""
    res = subprocess.run(
        ["git", "merge-base", "--is-ancestor", ancestor_ref, descendant_ref],
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    return res.returncode == 0


def _normalize_subject(subject: str) -> str:
    """Strips trailing PR number suffixes like ' (#123)' from commit subjects."""
    s = subject.strip()
    if s.endswith(")") and " (#" in s:
        prefix, _, suffix = s.rpartition(" (#")
        if suffix[:-1].isdigit():
            return prefix.strip()
    return s


def _compute_patch_id(diff_text: str) -> str | None:
    """Computes a stable git patch-id from unified diff text."""
    if not diff_text.strip():
        return None
    res = subprocess.run(
        ["git", "patch-id", "--stable"],
        input=diff_text,
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    if res.returncode != 0 or not res.stdout.strip():
        return None
    return res.stdout.strip().split()[0]


def _compute_bulk_patch_ids(shas: list[str]) -> dict[str, str]:
    """Computes stable git patch-ids in bulk for a list of commit SHAs."""
    if not shas:
        return {}
    log_res = subprocess.run(
        ["git", "log", "-p", "--no-walk", *shas],
        capture_output=True,
        text=True,
        check=False,
        timeout=15,
    )
    if log_res.returncode != 0 or not log_res.stdout.strip():
        return {}
    patch_res = subprocess.run(
        ["git", "patch-id", "--stable"],
        input=log_res.stdout,
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    if patch_res.returncode != 0:
        return {}
    return {
        parts[1]: parts[0]
        for line in patch_res.stdout.splitlines()
        if len(parts := line.split()) >= 2
    }


def _index_parent_commits(
    parent_base: str,
    target_ref: str,
    allow_subject_fallback: bool,
) -> tuple[set[str], Counter[str]]:
    """Indexes active (non-reverted) parent commits by patch-id and 1-to-1 subject count."""
    parent_log = subprocess.run(
        ["git", "log", "--reverse", "--format=%H %s", f"{parent_base}..{target_ref}"],
        capture_output=True,
        text=True,
        check=False,
        timeout=10,
    )
    if parent_log.returncode != 0 or not parent_log.stdout.strip():
        return set(), Counter()

    active_commits: list[tuple[str, str]] = []
    for line in parent_log.stdout.splitlines():
        parts = line.strip().split(" ", 1)
        if not parts or not parts[0]:
            continue
        sha = parts[0]
        raw_subj = parts[1].strip() if len(parts) > 1 else ""
        if raw_subj.startswith('Revert "') and raw_subj.endswith('"'):
            reverted = _normalize_subject(raw_subj[len('Revert "') : -1])
            active_commits = [c for c in active_commits if c[1] != reverted]
            continue
        active_commits.append((sha, _normalize_subject(raw_subj)))

    parent_shas = [sha for sha, _ in active_commits]
    patch_ids = set(_compute_bulk_patch_ids(parent_shas).values())
    subjects = (
        Counter(subj for _, subj in active_commits if subj) if allow_subject_fallback else Counter()
    )
    return patch_ids, subjects


def _match_cumulative_squash_prefix(
    candidate_base: str,
    prefix_commits: list[list[str]],
    parent_patch_ids: set[str],
    current_correlated: str,
) -> str:
    """Checks if a multi-commit prefix matches a squash-merge commit patch-id."""
    if not parent_patch_ids or current_correlated == prefix_commits[-1][0]:
        return current_correlated
    for sha, _ in reversed(prefix_commits):
        if sha == current_correlated:
            break
        diff_res = subprocess.run(
            ["git", "diff", candidate_base, sha],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        if diff_res.returncode == 0 and _compute_patch_id(diff_res.stdout) in parent_patch_ids:
            return sha
    return current_correlated


def _correlate_rebased_parent_tip(
    parent_base: str,
    target_ref: str,
    candidate_base: str,
    head_ref: str,
    allow_subject_fallback: bool = False,
) -> str:
    """Advances candidate_base past rebased/squash-merged parent commits matching target_ref."""
    try:
        parent_patch_ids, parent_subjects = _index_parent_commits(
            parent_base, target_ref, allow_subject_fallback
        )
        if not parent_patch_ids and not parent_subjects:
            return candidate_base

        head_log = subprocess.run(
            ["git", "log", "--reverse", "--format=%H %s", f"{candidate_base}..{head_ref}"],
            capture_output=True,
            text=True,
            check=False,
            timeout=10,
        )
        if head_log.returncode != 0 or not head_log.stdout.strip():
            return candidate_base

        commits = [
            line.strip().split(" ", 1) for line in head_log.stdout.splitlines() if line.strip()
        ]
        prefix_commits = [parts for parts in commits[:-1] if len(parts) == 2]
        if not prefix_commits:
            return candidate_base

        head_patch_ids = (
            _compute_bulk_patch_ids([c[0] for c in prefix_commits]) if parent_patch_ids else {}
        )
        correlated = candidate_base
        for sha, subj in prefix_commits:
            norm_subj = _normalize_subject(subj)
            patch_id = head_patch_ids.get(sha)
            if patch_id and patch_id in parent_patch_ids:
                correlated = sha
                if parent_subjects[norm_subj] > 0:
                    parent_subjects[norm_subj] -= 1
            elif allow_subject_fallback and parent_subjects[norm_subj] > 0:
                correlated = sha
                parent_subjects[norm_subj] -= 1
            else:
                break

        return _match_cumulative_squash_prefix(
            candidate_base, prefix_commits, parent_patch_ids, correlated
        )
    except Exception:
        return candidate_base


def resolve_effective_base_ref(
    base_ref: str,
    head_ref: str = "HEAD",
    base_branch: str | None = None,
) -> str:
    """Resolves the closest merge-base across origin/<base_branch>, base_ref, and origin/main.

    Includes both mb_branch and mb_base (suppressing mb_base only when origin/<base_branch> is
    an ancestor of base_ref due to a rewound stack parent), excludes merge-bases equal to
    head_ref, and correlates rewritten parent commits across gh-stack rebases and squash-merges.
    """
    clean_branch = (base_branch or "").strip()
    if not clean_branch and base_ref in {"origin/main", "main", "HEAD~1"}:
        return base_ref

    is_stacked_branch = bool(clean_branch and clean_branch not in {"main", "origin/main"})
    branch_ref = f"origin/{clean_branch}" if is_stacked_branch else ""
    mb_branch = _get_merge_base(branch_ref, head_ref) if is_stacked_branch else None
    mb_base = _get_merge_base(base_ref, head_ref)
    if mb_branch and mb_base and mb_branch != mb_base and _is_ancestor(branch_ref, base_ref):
        mb_base = None

    mb_main = (
        _get_merge_base("origin/main", head_ref)
        if base_ref not in {"origin/main", "main"}
        else None
    )
    merge_bases = list(
        dict.fromkeys(mb for mb in (mb_branch, mb_base, mb_main) if mb and mb != head_ref)
    )
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
        target_ref = branch_ref if mb_branch else base_ref
        parent_base = mb_branch or mb_base or candidate
        return _correlate_rebased_parent_tip(
            parent_base, target_ref, candidate, head_ref, allow_subject_fallback=True
        )
    if clean_branch in {"main", "origin/main"} and candidate != base_ref:
        return _correlate_rebased_parent_tip(
            candidate, base_ref, candidate, head_ref, allow_subject_fallback=False
        )
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
            effective_base = resolve_effective_base_ref(base_ref, "HEAD", base_branch)
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


def warn_full_backend_fallback(reason: str) -> None:
    """Prints a GitHub Actions warning that backend scoping fell back to the full suite.

    The warning goes to stderr so that stdout stays JSON-only; the Actions runner turns
    workflow commands on either stream into annotations.
    """
    print(f"::warning::Running the full backend suite: {reason}", file=sys.stderr)


def _changed_bazel_packages(files: list[str], cwd: Path | None) -> set[str] | None:
    """Maps modified files to `<package>:all` patterns, or None if one has no Bazel package."""
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
        if pkg is None or (not is_code_or_build and pkg == "//"):
            warn_full_backend_fallback(f"{clean_f} is not inside a Bazel package")
            return None
        pkg_target = f"{pkg}:all" if pkg != "//" else "//:all"
        changed_pkgs.add(pkg_target)
    return changed_pkgs


def _query_rdeps_tests(
    changed_pkgs: set[str], cwd: Path | None, repository_cache: str | None
) -> list[str]:
    """Returns the non-manual backend test targets that depend on changed_pkgs ([] on failure)."""
    pkg_set_expr = " ".join(sorted(changed_pkgs))
    backend_universe = "//... except //cmd/ui/..."
    query_expr = (
        f"let t = kind('.*_test', rdeps({backend_universe}, set({pkg_set_expr}))) "
        f"in $t except attr('tags', 'manual', $t)"
    )
    # Fetching stays enabled: a fresh CI runner has no external repositories yet, so an offline
    # (--fetch=false) query cannot load any package there. The repository cache that CI restores
    # supplies the downloads, and the fetched repositories are reused by the later bazel test.
    cmd = ["bazel", "query", "--tool_deps", "--noimplicit_deps", "--keep_going"]
    if repository_cache:
        cmd.append(f"--repository_cache={repository_cache}")
    try:
        res = subprocess.run(
            [*cmd, query_expr],
            capture_output=True,
            text=True,
            check=False,
            timeout=RDEPS_QUERY_TIMEOUT_SECONDS,
            cwd=cwd,
        )
    except Exception as e:
        warn_full_backend_fallback(f"bazel query encountered exception: {e}")
        return []
    if res.returncode != 0:
        warn_full_backend_fallback(f"bazel query failed with exit code {res.returncode}")
        print(res.stderr.strip(), file=sys.stderr)
        return []
    targets = [t.strip() for t in res.stdout.splitlines() if t.strip().startswith("//")]
    targets = [t for t in targets if t not in EXCLUDED_TEST_TARGETS]
    if not targets:
        warn_full_backend_fallback(f"bazel query found no tests that depend on {pkg_set_expr}")
    return targets


def discover_bazel_rdeps_targets(
    files: list[str], cwd: Path | None = None, repository_cache: str | None = None
) -> list[str]:
    """Runs Bazel query to discover unit test targets that transitively depend on changed packages.

    Returns an empty list, after printing a GitHub Actions warning, if a modified file has no
    Bazel package or the query fails or finds no tests.

    Args:
        files: Modified file paths relative to the workspace root.
        cwd: Workspace root (default: the current directory).
        repository_cache: Optional Bazel repository cache directory for the query.
    """
    changed_pkgs = _changed_bazel_packages(files, cwd)
    # Return empty list on failure so resolve_test_scope safely falls back to the full test suite
    if changed_pkgs is None:
        return []
    if not changed_pkgs:
        warn_full_backend_fallback("no modified file maps to a Bazel package")
        return []
    return _query_rdeps_tests(changed_pkgs, cwd, repository_cache)


def resolve_test_scope(
    files: list[str],
    event_name: str = "pull_request",
    ref: str = "",
    use_rdeps: bool = True,
    cwd: Path | None = None,
    repository_cache: str | None = None,
) -> dict[str, object]:
    """Main entry point for test scope resolution.

    Full regressions also select the Helm chart suite; otherwise run_helm follows
    touches_helm_suite, independently of the backend and UI scope.
    """
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
            "run_helm": True,
        }

    if not any(f.strip() for f in files):
        return {
            "run_backend": True,
            "run_ui": True,
            "backend_mode": "all",
            "bazel_targets": "//... -//cmd/ui/...",
            "reason": "no modified files detected; defaulting to full test suite for safety",
            "run_helm": True,
        }

    scope = _resolve_code_scope(files, use_rdeps, cwd, repository_cache)
    return scope | {"run_helm": touches_helm_suite(files)}


def _resolve_code_scope(
    files: list[str], use_rdeps: bool, cwd: Path | None, repository_cache: str | None
) -> dict[str, object]:
    """Scopes the backend Bazel and UI suites for a non-empty list of modified files."""
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
    rdeps_targets = discover_bazel_rdeps_targets(files, cwd=cwd, repository_cache=repository_cache)
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
        f.write(f"run_helm={str(result['run_helm']).lower()}\n")
        f.write(f"backend_mode={result['backend_mode']}\n")
        f.write(f"bazel_targets={result['bazel_targets']}\n")
        f.write(f"reason={result['reason']}\n")


def load_event_payload(event_path: str | None) -> dict:
    """Loads the GitHub event payload JSON, returning {} if missing or malformed."""
    if not event_path:
        return {}
    try:
        payload = json.loads(Path(event_path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    return payload if isinstance(payload, dict) else {}


def _sha_field(obj: object, key: str) -> str:
    """Returns obj[key] as a string if obj is a dict, else an empty string."""
    return str(obj.get(key) or "") if isinstance(obj, dict) else ""


def _payload_shas(payload: dict) -> tuple[str, str]:
    """Extracts (base_sha, head_sha) from a pull_request or merge_group payload."""
    pr = payload.get("pull_request")
    if isinstance(pr, dict):
        return _sha_field(pr.get("base"), "sha"), _sha_field(pr.get("head"), "sha")
    group = payload.get("merge_group")
    return _sha_field(group, "base_sha"), _sha_field(group, "head_sha")


def apply_github_env_defaults(args: argparse.Namespace, env: dict[str, str]) -> argparse.Namespace:
    """Fills CLI options left unset (None) from GitHub Actions env vars and event payload.

    Explicit flags always win. The payload head SHA is preferred over GITHUB_SHA,
    which is a synthetic merge commit on pull_request events. Outside GitHub
    Actions (no GITHUB_EVENT_NAME), legacy local defaults are preserved.
    """
    in_actions = bool(env.get("GITHUB_EVENT_NAME"))
    base_sha, head_sha = _payload_shas(load_event_payload(env.get("GITHUB_EVENT_PATH")))
    if args.base is None and in_actions:
        args.base = base_sha or "origin/main"
    if args.head is None:
        args.head = head_sha or env.get("GITHUB_SHA") or "HEAD"
    if args.base_branch is None:
        args.base_branch = env.get("GITHUB_BASE_REF", "")
    if args.event is None:
        args.event = env.get("GITHUB_EVENT_NAME") or "pull_request"
    if args.ref is None:
        args.ref = env.get("GITHUB_REF", "")
    if args.github_output is None:
        args.github_output = env.get("GITHUB_OUTPUT") or None
    return args


def resolve_local_scope(
    base_ref: str,
    repo_root: Path,
    use_rdeps: bool = True,
    repository_cache: str | None = None,
) -> dict[str, object]:
    """Scopes the local checkout the way a pull request against base_ref is scoped.

    Modified files are those changed since the merge base of base_ref and HEAD, uncommitted
    and untracked files included (see tools.common.changed_files). Unlike CI, an empty change
    set selects no suites. The result also lists the files under "changed_files".

    Args:
        base_ref: Ref whose merge base with HEAD is the comparison point.
        repo_root: Top-level directory of the git checkout and Bazel workspace.
        use_rdeps: Whether to narrow backend tests with the Bazel rdeps query.
        repository_cache: Optional Bazel repository cache directory for the rdeps query.

    Raises:
        ChangedFilesError: If git cannot list the changed files.
    """
    files = list_changed_files(base_ref, repo_root)
    if not files:
        return {
            "run_backend": False,
            "run_ui": False,
            "backend_mode": "none",
            "bazel_targets": "",
            "reason": f"no files changed since the merge base with {base_ref}",
            "run_helm": False,
            "changed_files": [],
        }
    scope = resolve_test_scope(
        files, use_rdeps=use_rdeps, cwd=repo_root, repository_cache=repository_cache
    )
    return scope | {"changed_files": files}


def resolve_ci_scope(args: argparse.Namespace, env: Mapping[str, str]) -> dict[str, object]:
    """Scopes a CI run from explicit flags, GitHub Actions env vars, and the event payload.

    Args:
        args: Options parsed by build_parser(); options left unset are filled from env.
        env: Environment variables, e.g. os.environ.
    """
    args = apply_github_env_defaults(args, dict(env))

    if args.files is not None:
        modified_files = args.files
    else:
        modified_files = get_git_modified_files(
            base_ref=args.base,
            head_ref=args.head,
            base_branch=args.base_branch,
        )

    print(f"DEBUG modified_files: {modified_files}", file=sys.stderr)

    return resolve_test_scope(
        files=modified_files,
        event_name=args.event,
        ref=args.ref,
        use_rdeps=not args.disable_rdeps,
        repository_cache=args.repository_cache,
    )


def build_parser() -> argparse.ArgumentParser:
    """Returns the command-line parser of the resolver."""
    parser = argparse.ArgumentParser(description=__doc__)
    sources = parser.add_mutually_exclusive_group()
    sources.add_argument(
        "--files", nargs="*", help="Optional explicit list of modified file paths."
    )
    sources.add_argument(
        "--local",
        action="store_true",
        help=(
            "Scope the local checkout like a pull request: files changed since the merge base of "
            "--base and HEAD, uncommitted and untracked files included (default --base: "
            f"{DEFAULT_LOCAL_BASE_REF}). Ignores --base-branch, --head, --event and --ref."
        ),
    )
    parser.add_argument(
        "--base",
        "--base-ref",
        dest="base",
        help="Git diff base reference (default: event payload base SHA).",
    )
    parser.add_argument(
        "--base-branch", help="Git diff base branch name (default: $GITHUB_BASE_REF)."
    )
    parser.add_argument(
        "--head", help="Git diff head reference (default: payload head SHA, $GITHUB_SHA, HEAD)."
    )
    parser.add_argument("--event", help="GitHub event name (default: $GITHUB_EVENT_NAME).")
    parser.add_argument("--ref", help="GitHub ref name (default: $GITHUB_REF).")
    parser.add_argument(
        "--no-rdeps",
        action="store_true",
        default=False,
        dest="disable_rdeps",
        help="Disable Bazel rdeps scoping.",
    )
    parser.add_argument(
        "--repository-cache",
        help="Bazel repository cache directory for the rdeps query (CI passes its restored cache).",
    )
    parser.add_argument(
        "--github-output", help="Path to $GITHUB_OUTPUT file (default: $GITHUB_OUTPUT)."
    )
    return parser


def main() -> None:
    args = build_parser().parse_args()
    try:
        result = (
            resolve_local_scope(
                args.base or DEFAULT_LOCAL_BASE_REF,
                _REPO_ROOT,
                use_rdeps=not args.disable_rdeps,
                repository_cache=args.repository_cache,
            )
            if args.local
            else resolve_ci_scope(args, os.environ)
        )
    except ChangedFilesError as e:
        sys.exit(f"Error: {e.message}\n{e.instruction}")

    print(f"DEBUG result: {result}", file=sys.stderr)

    if args.github_output:
        write_github_output(args.github_output, result)

    # Output human-readable JSON summary to stdout
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
