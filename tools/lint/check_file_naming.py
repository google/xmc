#!/usr/bin/env python3
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

"""Lint enforcing UI/workflow file naming, 1-to-1 unit test pairing, and Bazel test wiring."""

from __future__ import annotations

import argparse
import ast
import fnmatch
import re
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_command, run_tool  # noqa: E402

ALLOWLIST: dict[str, str] = {
    "cmd/ui/index.html": "Angular SPA root entry HTML configured in cmd/ui/angular.json.",
}

# Downward-only ratchet of pre-existing source files that predated the 1-to-1 unit test
# gate. Adding a paired *_test.* file (or deleting the source file) makes the entry stale
# and requires removing it from this set. New files may NEVER be added here.
LEGACY_UNPAIRED_ALLOWLIST: frozenset[str] = frozenset(
    {
        "api/filter/expression_helper.go",
        "api/filter/go/filter/parser.go",
        "api/filter/go/filtervisitor/visitor.go",
        "cmd/actuator/config.go",
        "cmd/actuator/metadata.go",
        "cmd/log_monitor/database.go",
        "cmd/reconciler/config.go",
        "cmd/reconciler/metadata.go",
        "cmd/ui/app/common/components/artifact_card/artifact_card.ts",
        "cmd/ui/app/common/components/artifacts_list/artifacts_column/artifacts_column.ts",
        "cmd/ui/app/common/components/confirmation_dialog/confirmation_dialog.ts",
        "cmd/ui/app/common/components/confirmation_dialog/confirmation_dialog_service.ts",
        "cmd/ui/app/common/components/status_chip/status_chip.ts",
        "cmd/ui/app/common/components/status_message/status_message.ts",
        "cmd/ui/app/common/components/status_message_filter/status_message_filter.ts",
        "cmd/ui/app/common/components/urls_widget/urls_widget.ts",
        "cmd/ui/app/common/components/work_units_status_bar/work_units_status_bar.ts",
        "cmd/ui/app/common/components/xid_badge/xid_badge.ts",
        "cmd/ui/app/experiment_page/work_units_table/work_units_table_store.ts",
        "cmd/ui/app/service/snackbar_service.ts",
        "cmd/ui/app/work_unit_page/work_unit_page_store.ts",
        "deploy/cli/launch.py",
        "internal/broadcast/events.go",
        "internal/common/health/spanner_health.go",
        "internal/kubernetes/common/constants/constants.go",
        "internal/query_generator/dialect/postgres/postgres.go",
        "internal/query_generator/dialect/spanner/spanner.go",
        "internal/query_generator/filter/statement.go",
        "internal/query_generator/normalizer/lexer.go",
        "internal/query_generator/predicate/predicate.go",
        "internal/query_generator/querytools/unnest.go",
        "internal/storage/pause.go",
        "internal/storage/postgres/data/proto.go",
        "internal/storage/spanner/artifact/repository.go",
        "internal/storage/spanner/component/mutations.go",
        "internal/storage/spanner/dashboard/repository.go",
        "internal/storage/spanner/experiment/repository.go",
        "internal/storage/spanner/migrations/version/generate_version.py",
        "internal/storage/spanner/operation/repository.go",
        "internal/storage/spanner/spanner.go",
        "internal/storage/spanner/statusmessage/history.go",
        "internal/storage/spanner/statusmessage/repository.go",
        "internal/storage/spanner/workunit/createworkunit.go",
        "internal/storage/spanner/xid/repository.go",
        "internal/task_queue_redis/redisclient/publisher.go",
        "internal/task_queue_redis/validator/validator.go",
        "pkg/auth/oauth2/gateway.go",
        "pkg/auth/oauth2/handlers.go",
        "pkg/auth/oauth2/middleware.go",
        "pkg/auth/oauth2/provider.go",
        "pkg/components/bridge/component_rate_limiter.go",
        "pkg/components/bridge/statusdetector/status_detector.go",
        "pkg/components/store/ownership/ownership_metrics.go",
        "scripts/validate_redpanda_setup.py",
        "tools/common/workspace_status.py",
        "tools/lint/dev_binary_guard.py",
    }
)

_TS_EXT_RE = re.compile(r"\.[cm]?tsx?$")
_SCREENSHOT_BAD_SPEC_RE = re.compile(r"(?:_test|_spec|\.test|\.spec)\.[cm]?tsx?$")
_NON_SCREENSHOT_BAD_TS_RE = re.compile(r"(?:\.spec|_spec|\.test)\.[cm]?tsx?$")
_KARMA_TS_TEST_RE = re.compile(r"_test\.[cm]?tsx?$")
_BAD_WORKFLOW_TEST_RE = re.compile(
    r"(?:_test|_spec|\.spec)\.[cm]?[jt]sx?$"
    r"|(?:\.test)\.(?:[cm]js|[cm]?tsx?)$"
    r"|(?:^|/)test_[^/]*\.[cm]?[jt]sx?$"
)
_GO_EXEMPT_DIRS = frozenset(
    {
        "vendor",
        "third_party",
        "node_modules",
        "testing",
        "testdata",
        "securitytest",
        "healthprobetest",
        "profilecontract",
    }
)
_GO_EXEMPT_NAMES = frozenset({"main.go", "test_utils.go", "test_mocks.go", "harness.go"})
_GO_FUNC_RE = re.compile(r"^func\s+", re.MULTILINE)
_PY_DEF_RE = re.compile(r"^\s*(?:async\s+)?def\s+\w+", re.MULTILINE)
_TS_LOGIC_RE = re.compile(
    r"@(?:Component|Injectable|Directive|Pipe)\b"
    r"|\bexport\s+(?:default\s+)?(?:(?:abstract\s+)?class|(?:async\s+)?function)\b"
    r"|\bexport\s+(?:const|let)\s+\w+(?:\s*:\s*[^=]+)?=\s*(?:async\s+)?"
    r"(?:<[^>]+>\s*)?(?:\([^)]*\)|[A-Za-z_]\w*)(?:\s*:\s*[^=]+)?\s*=>"
)
_UI_TS_EXEMPT_SUFFIXES = (
    "_test.ts",
    ".spec.ts",
    "_spec.ts",
    ".test.ts",
    ".d.ts",
    "_harness.ts",
)
_NON_BAZEL_PY_TEST_DIRS = ("test/e2e/", "test/regression/")
_NIGHTLY_EXAMPLES_BUILD = "tools/nightly_tests/examples_verification/BUILD.bazel"


def _check_ui_html(path: str) -> str | None:
    """Validate HTML file naming under cmd/ui/."""
    if not (path.startswith("cmd/ui/") and path.endswith(".html")):
        return None
    if path.startswith("cmd/ui/app/") and path.endswith(".ng.html"):
        return None
    return (
        "Angular component templates under cmd/ui/ must live under cmd/ui/app/ and "
        "use *.ng.html (only cmd/ui/index.html is exempt)."
    )


def _check_screenshot_ts(path: str) -> str | None:
    """Validate Playwright TypeScript spec naming under cmd/ui/screenshot_tests/."""
    if not (path.startswith("cmd/ui/screenshot_tests/") and _TS_EXT_RE.search(path)):
        return None
    if path.endswith(".screenshot.spec.ts"):
        return None
    if _SCREENSHOT_BAD_SPEC_RE.search(path):
        return "Playwright specs under cmd/ui/screenshot_tests/ must use *.screenshot.spec.ts."
    return None


def _check_ts_test_outside_screenshots(path: str) -> str | None:
    """Validate TypeScript test file naming outside cmd/ui/screenshot_tests/."""
    if path.startswith("cmd/ui/screenshot_tests/") or not _TS_EXT_RE.search(path):
        return None
    if _NON_SCREENSHOT_BAD_TS_RE.search(path):
        return (
            "TypeScript unit tests must use *_test.ts under cmd/ui/app/ "
            "(Playwright *.screenshot.spec.ts belongs under cmd/ui/screenshot_tests/)."
        )
    if not path.startswith("cmd/ui/"):
        return None
    if _KARMA_TS_TEST_RE.search(path) and not (
        path.startswith("cmd/ui/app/") and path.endswith("_test.ts")
    ):
        return (
            "Angular Karma unit test specs must live under cmd/ui/app/ and use "
            "*_test.ts (cmd/ui/angular.json runs only app/**/*_test.ts)."
        )
    return None


def _check_github_scripts(path: str) -> str | None:
    """Validate Node workflow test file naming under .github/scripts/."""
    if not path.startswith(".github/scripts/"):
        return None
    rel = path.removeprefix(".github/scripts/")
    if "/" not in rel and rel.endswith(".test.js"):
        return None
    if "/" in rel and rel.endswith(".test.js"):
        return (
            "Node workflow script tests must be top-level .github/scripts/*.test.js "
            "files (CI runs non-recursive 'node --test .github/scripts/*.test.js')."
        )
    if _BAD_WORKFLOW_TEST_RE.search(rel):
        return "Node workflow script tests under .github/scripts/ must use *.test.js."
    return None


def classify_path_violation(path: str) -> str | None:
    """Return a violation message if path breaks file-naming rules, else None.

    Args:
        path: Repository-relative POSIX path.

    Returns:
        Human-readable violation reason, or None if the path is compliant.
    """
    if path in ALLOWLIST:
        return None
    for checker in (
        _check_ui_html,
        _check_screenshot_ts,
        _check_github_scripts,
        _check_ts_test_outside_screenshots,
    ):
        reason = checker(path)
        if reason is not None:
            return reason
    return None


def _expected_go_test(path: str) -> str | None:
    """Return the expected *_test.go path for a Go source file, or None if exempt."""
    if not path.endswith(".go") or path.endswith("_test.go"):
        return None
    p = Path(path)
    if p.name in _GO_EXEMPT_NAMES or p.name.endswith("_test_utils.go"):
        return None
    if any(part in _GO_EXEMPT_DIRS for part in p.parts):
        return None
    return f"{path[:-3]}_test.go"


def _expected_py_test(path: str) -> tuple[str, str] | None:
    """Return candidate (*_test.py, test_*.py) paths for a Python module, or None if exempt."""
    if not path.endswith(".py") or path.endswith("_test.py"):
        return None
    p = Path(path)
    if p.name in {"__init__.py", "conftest.py"} or p.name.startswith("test_"):
        return None
    if path.startswith(("examples/", "test/")) or "/examples/" in path or "/testdata/" in path:
        return None
    return p.with_name(f"{p.stem}_test.py").as_posix(), p.with_name(f"test_{p.name}").as_posix()


def _expected_ui_ts_test(path: str) -> str | None:
    """Return the expected *_test.ts path for a UI TypeScript module, or None if exempt."""
    if not (path.startswith("cmd/ui/app/") and path.endswith(".ts")):
        return None
    if path.endswith(_UI_TS_EXEMPT_SUFFIXES) or "/testing/" in path:
        return None
    return f"{path[:-3]}_test.ts"


def _expected_github_script_test(path: str) -> str | None:
    """Return the expected *.test.js path for a top-level .github/scripts/*.js module."""
    if not (path.startswith(".github/scripts/") and path.endswith(".js")):
        return None
    rel = path.removeprefix(".github/scripts/")
    if "/" in rel or rel.endswith(".test.js") or _BAD_WORKFLOW_TEST_RE.search(rel):
        return None
    return f"{path[:-3]}.test.js"


def _read_tracked_text(repo_root: Path, path: str) -> str | None:
    """Read a tracked file from disk or fall back to its staged index blob."""
    file_path = repo_root / path
    if file_path.is_file():
        return file_path.read_text(encoding="utf-8", errors="replace")
    proc = run_command(
        ["git", "show", f":{path}"],
        cwd=repo_root,
        capture_output=True,
        check=False,
        log_cmd=False,
        errors="replace",
    )
    return proc.stdout if proc.returncode == 0 else None


def _has_executable_declarations(repo_root: Path, path: str, pattern: re.Pattern[str]) -> bool:
    """Return True if repo_root/path matches pattern (or fail closed if unreadable)."""
    content = _read_tracked_text(repo_root, path)
    if content is None:
        return True
    return bool(pattern.search(content))


def _paired_test_spec(path: str) -> tuple[tuple[str, ...], re.Pattern[str] | None, str] | None:
    """Return (candidate_tests, logic_pattern, error_message) for a source file, or None."""
    if js_test := _expected_github_script_test(path):
        msg = f"Node workflow scripts under .github/scripts/ must have a paired {js_test}."
        return (js_test,), None, msg
    if go_test := _expected_go_test(path):
        msg = f"Go source files with executable functions must have a paired {go_test}."
        return (go_test,), _GO_FUNC_RE, msg
    if py_tests := _expected_py_test(path):
        msg = f"Python modules with functions/methods must have a paired {py_tests[0]}."
        return py_tests, _PY_DEF_RE, msg
    if ts_test := _expected_ui_ts_test(path):
        msg = f"Angular UI modules under cmd/ui/app/ must have a paired {ts_test}."
        return (ts_test,), _TS_LOGIC_RE, msg
    return None


def _check_missing_paired_test(path: str, tracked_set: set[str], repo_root: Path) -> str | None:
    """Return a violation if a source file lacks its required 1-to-1 unit test file."""
    if path in LEGACY_UNPAIRED_ALLOWLIST or (spec := _paired_test_spec(path)) is None:
        return None
    candidates, pattern, message = spec
    if any(candidate in tracked_set for candidate in candidates):
        return None
    if pattern is not None and not _has_executable_declarations(repo_root, path, pattern):
        return None
    return message


def _find_nearest_build_file(path: str, tracked_set: set[str]) -> tuple[str, str] | None:
    """Return (build_file_rel_path, target_rel_to_build_dir) for path, or None."""
    parent = Path(path).parent
    while True:
        for bname in ("BUILD.bazel", "BUILD"):
            candidate = bname if parent == Path(".") else f"{parent.as_posix()}/{bname}"
            if candidate in tracked_set:
                rel = path if parent == Path(".") else Path(path).relative_to(parent).as_posix()
                return candidate, rel
        if parent == Path("."):
            return None
        parent = parent.parent


def _extract_string_literals(node: ast.AST) -> list[str]:
    """Return string constants from an AST List or Tuple node."""
    if not isinstance(node, (ast.List, ast.Tuple)):
        return []
    return [
        elt.value
        for elt in node.elts
        if isinstance(elt, ast.Constant) and isinstance(elt.value, str)
    ]


def _matches_bazel_glob(call: ast.Call, rel_target: str) -> bool:
    """Return True if a Starlark glob(...) call matches rel_target."""
    includes = _extract_string_literals(call.args[0]) if call.args else []
    excludes: list[str] = []
    for kw in call.keywords:
        if kw.arg == "include":
            includes.extend(_extract_string_literals(kw.value))
        elif kw.arg == "exclude":
            excludes.extend(_extract_string_literals(kw.value))
    return any(fnmatch.fnmatchcase(rel_target, pat) for pat in includes) and not any(
        fnmatch.fnmatchcase(rel_target, pat) for pat in excludes
    )


def _srcs_expr_matches_target(expr: ast.AST, rel_target: str) -> bool:
    """Return True if a Starlark srcs expression includes rel_target."""
    if isinstance(expr, (ast.List, ast.Tuple)):
        return any(lit in {rel_target, f":{rel_target}"} for lit in _extract_string_literals(expr))
    if isinstance(expr, ast.Call) and isinstance(expr.func, ast.Name) and expr.func.id == "glob":
        return _matches_bazel_glob(expr, rel_target)
    if isinstance(expr, ast.BinOp) and isinstance(expr.op, ast.Add):
        return _srcs_expr_matches_target(expr.left, rel_target) or _srcs_expr_matches_target(
            expr.right, rel_target
        )
    return False


def _is_wired_in_build_ast(
    build_rel: str, rel_target: str, content: str, allowed_rules: frozenset[str]
) -> bool:
    """Return True if rel_target is wired in srcs of an allowed test rule in BUILD.bazel."""
    try:
        tree = ast.parse(content, filename=build_rel)
    except SyntaxError:
        return False
    for node in ast.walk(tree):
        if (
            isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and node.func.id in allowed_rules
        ):
            for kw in node.keywords:
                if kw.arg == "srcs" and _srcs_expr_matches_target(kw.value, rel_target):
                    return True
    return False


def _has_any_tracked_build_file(tracked_set: set[str]) -> bool:
    """Return True if tracked_set contains at least one BUILD or BUILD.bazel file."""
    return any(
        p in {"BUILD.bazel", "BUILD"} or p.endswith(("/BUILD.bazel", "/BUILD")) for p in tracked_set
    )


def _check_unwired_bazel_test(path: str, tracked_set: set[str], repo_root: Path) -> str | None:
    """Return a violation if a *_test.go or Python test file is not wired in BUILD.bazel."""
    name = Path(path).name
    is_go_test = path.endswith("_test.go") and not path.startswith(("vendor/", "third_party/"))
    is_py_test = (
        path.endswith(".py")
        and (path.endswith("_test.py") or name.startswith("test_"))
        and not path.startswith(_NON_BAZEL_PY_TEST_DIRS)
    )
    if not (is_go_test or is_py_test) or not _has_any_tracked_build_file(tracked_set):
        return None
    found = _find_nearest_build_file(path, tracked_set)
    if found is None:
        return f"Test file {path} has no enclosing BUILD.bazel file."
    build_rel, rel_target = found
    content = _read_tracked_text(repo_root, build_rel)
    if content is None:
        return f"Tracked BUILD file {build_rel} is missing from the working tree."
    if is_go_test:
        allowed_rules = frozenset({"go_test"})
    elif build_rel == _NIGHTLY_EXAMPLES_BUILD:
        allowed_rules = frozenset({"py_test", "py_binary"})
    else:
        allowed_rules = frozenset({"py_test"})
    if not _is_wired_in_build_ast(build_rel, rel_target, content, allowed_rules):
        return f"Test file {path} is not registered in {build_rel} (add to go_test/py_test srcs)."
    return None


def _is_paired_in_tracked(path: str, tracked_set: set[str]) -> bool:
    """Return True if a legacy allowlisted source file now has a paired unit test."""
    spec = _paired_test_spec(path)
    return spec is not None and any(candidate in tracked_set for candidate in spec[0])


def _find_stale_allowlist_entries(tracked_set: set[str]) -> list[str]:
    """Return sorted allowlist entries that no longer exist or now have paired unit tests."""
    stale_allow = [path for path in ALLOWLIST if path not in tracked_set]
    stale_legacy = [
        path
        for path in LEGACY_UNPAIRED_ALLOWLIST
        if path not in tracked_set or _is_paired_in_tracked(path, tracked_set)
    ]
    return sorted(set(stale_allow + stale_legacy))


def _classify_repo_violation(path: str, tracked_set: set[str], repo_root: Path) -> str | None:
    """Validate path naming, 1-to-1 unit test pairing, and BUILD.bazel test registration."""
    if (naming_error := classify_path_violation(path)) is not None:
        return naming_error
    if (pairing_error := _check_missing_paired_test(path, tracked_set, repo_root)) is not None:
        return pairing_error
    return _check_unwired_bazel_test(path, tracked_set, repo_root)


def _list_tracked_paths(repo_root: Path) -> list[str]:
    """List git-tracked repository paths rooted at repo_root."""
    if not repo_root.is_dir() or not (repo_root / ".git").exists():
        raise HandledError(
            f"Invalid git repository root: {repo_root}",
            "Pass --repo-root pointing to the top-level git working tree.",
        )
    proc = run_command(
        ["git", "ls-files", "-z"],
        cwd=repo_root,
        capture_output=True,
        log_cmd=False,
        errors="replace",
        handled_instruction="Ensure git is installed and --repo-root is a valid git repository.",
    )
    return [entry for entry in proc.stdout.split("\0") if entry]


def check_file_naming(args: argparse.Namespace) -> dict[str, Any]:
    """Validate UI/workflow file naming, 1-to-1 test pairing, and Bazel test wiring.

    Args:
        args: Parsed CLI namespace containing repo_root.

    Returns:
        Summary dictionary with files_scanned and violations count.

    Raises:
        HandledError: If repo_root is invalid, allowlist entries are stale, or
            any tracked file violates naming or test-pairing conventions.
    """
    repo_root = Path(args.repo_root).resolve()
    tracked_paths = _list_tracked_paths(repo_root)
    tracked_set = set(tracked_paths)

    stale_allowlist = _find_stale_allowlist_entries(tracked_set)
    violations = [
        {"path": path, "reason": reason}
        for path in tracked_paths
        if (reason := _classify_repo_violation(path, tracked_set, repo_root)) is not None
    ]
    if stale_allowlist or violations:
        raise HandledError(
            f"Found {len(violations)} file-naming/test-cohesion violation(s) and "
            f"{len(stale_allowlist)} stale allowlist entry/entries.",
            "Ensure templates and tests follow naming and 1-to-1 test-pairing conventions "
            "(.ng.html, *_test.go, *_test.py, *_test.ts, *.screenshot.spec.ts, *.test.js), "
            "register test files in BUILD.bazel, and prune stale ALLOWLIST entries.",
            details={
                "violations": violations,
                "stale_allowlist": stale_allowlist,
            },
        )
    return {"files_scanned": len(tracked_paths), "violations": 0}


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser.

    Returns:
        Configured ArgumentParser instance.
    """
    parser = argparse.ArgumentParser(
        description="Lint UI template, UI test, and .github/scripts test file naming.",
    )
    parser.add_argument(
        "--repo-root",
        default=str(_REPO_ROOT),
        help="Repository root directory.",
    )
    return parser


def main() -> None:
    """Run the file-naming linter CLI."""
    run_tool(build_parser(), check_file_naming)


if __name__ == "__main__":
    main()
