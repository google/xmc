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

"""Lint enforcing UI template, UI test, and GitHub workflow test file naming."""

from __future__ import annotations

import argparse
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

_TS_EXT_RE = re.compile(r"\.[cm]?tsx?$")
_SCREENSHOT_BAD_SPEC_RE = re.compile(r"(?:_test|_spec|\.test|\.spec)\.[cm]?tsx?$")
_NON_SCREENSHOT_BAD_TS_RE = re.compile(r"(?:\.spec|_spec|\.test)\.[cm]?tsx?$")
_KARMA_TS_TEST_RE = re.compile(r"_test\.[cm]?tsx?$")
_BAD_WORKFLOW_TEST_RE = re.compile(
    r"(?:_test|_spec|\.spec)\.[cm]?[jt]sx?$"
    r"|(?:\.test)\.(?:[cm]js|[cm]?tsx?)$"
    r"|(?:^|/)test_[^/]*\.[cm]?[jt]sx?$"
)


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
    """Validate UI and workflow file naming across tracked repository files.

    Args:
        args: Parsed CLI namespace containing repo_root.

    Returns:
        Summary dictionary with files_scanned and violations count.

    Raises:
        HandledError: If repo_root is invalid, allowlist entries are stale, or
            any tracked file violates naming conventions.
    """
    repo_root = Path(args.repo_root).resolve()
    tracked_paths = _list_tracked_paths(repo_root)
    tracked_set = set(tracked_paths)

    stale_allowlist = [path for path in sorted(ALLOWLIST) if path not in tracked_set]
    violations = [
        {"path": path, "reason": reason}
        for path in tracked_paths
        if (reason := classify_path_violation(path)) is not None
    ]
    if stale_allowlist or violations:
        raise HandledError(
            f"Found {len(violations)} file-naming violation(s) and "
            f"{len(stale_allowlist)} stale allowlist entry/entries.",
            "Rename misnamed template/test files to match UI & workflow naming "
            "conventions (.ng.html, *_test.ts, *.screenshot.spec.ts, *.test.js) "
            "and prune stale ALLOWLIST entries.",
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
