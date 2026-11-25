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

"""CLI runner orchestrating all XMC pre-commit linter and affected-test gates."""

from __future__ import annotations

import argparse
import os
import re
import shutil
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.lint import (  # noqa: E402
    precommit_deploy_checks as deploy_checks,
    precommit_gates as gates,
)

_LICENSE_IGNORE_PATTERNS = (
    "**/node_modules/** **/.angular/** **/dist/** **/venv/** **/ctk-out/** "
    "**.json **.lock pyproject.toml package.json go.mod go.sum"
)


_BAZEL_PATHSPECS = (
    "BUILD",
    ":(glob)**/BUILD",
    "BUILD.bazel",
    ":(glob)**/BUILD.bazel",
    "*.bzl",
    ":(glob)**/*.bzl",
    "WORKSPACE",
    "MODULE.bazel",
)
_GOOGLE_BUILDIFIER_WARNINGS = "-LANG_proto_library-single-deps,-inline-deps"


def check_bazel(ctx: gates.RunContext) -> None:
    """Formats and validates Bazel files via `buildifier`."""
    buildifier = gates.resolve_go_tool(
        ctx,
        "buildifier",
        "buildifier not found. Install: go install github.com/bazelbuild/buildtools/buildifier@latest",
    )
    if not buildifier:
        return
    print("--> Running Bazel Formatter & Linter (Buildifier)...")
    files = gates.git_ls_files(_BAZEL_PATHSPECS, include_untracked=True)
    if not files:
        return
    probe = [
        buildifier,
        "-lint=warn",
        f"-warnings={_GOOGLE_BUILDIFIER_WARNINGS}",
        "-mode=check",
        "-type=build",
    ]
    warn_args = (
        [f"-warnings={_GOOGLE_BUILDIFIER_WARNINGS}"]
        if gates.exec_cmd(probe, quiet_out=True, quiet_err=True, stdin_devnull=True).returncode == 0
        else []
    )
    for batch in gates.chunked(files, 500):
        if (
            ctx.mode == "fix"
            and gates.exec_cmd(
                [buildifier, "-lint=fix", *warn_args, "-mode=fix", *batch]
            ).returncode
            != 0
        ):
            ctx.failed = True
        if (
            gates.exec_cmd([buildifier, "-lint=warn", *warn_args, "-mode=check", *batch]).returncode
            != 0
        ):
            ctx.fail("Bazel buildifier check failed.")


def _check_gofmt(ctx: gates.RunContext) -> None:
    if not shutil.which("gofmt"):
        return
    print("--> Running Go Formatter (gofmt)...")
    if ctx.mode == "check":
        res = gates.exec_cmd(["gofmt", "-s", "-l", "."], capture=True)
        if res.returncode != 0:
            if res.stderr:
                print(res.stderr.strip(), file=sys.stderr)
            ctx.fail("gofmt check failed.")
        sep = os.sep
        if bad := [
            p for p in res.stdout.splitlines() if p and "vendor/" not in p.replace(sep, "/")
        ]:
            print("❌ Unformatted Go files found:\n" + "\n".join(bad))
            ctx.failed = True
        return
    for batch in gates.chunked(gates.collect_target_files("fix", staged_regex=r"\.go$"), 500):
        if gates.exec_cmd(["gofmt", "-s", "-w", *batch]).returncode != 0:
            ctx.fail("gofmt formatting failed.")


def check_go(ctx: gates.RunContext) -> None:
    """Runs `gofmt`, `golangci-lint`, Go determinism checker, and `go mod tidy`."""
    _check_gofmt(ctx)
    if linter := gates.resolve_go_tool(
        ctx,
        "golangci-lint",
        "golangci-lint not found. Go linting skipped.",
        "Install: go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.6",
    ):
        print("--> Running Golang Linter (golangci-lint)...")
        gates.run_mode_command(
            ctx,
            [linter],
            check_args=["run"],
            fix_args=["run", "--fix"],
            failure_msg="golangci-lint failed.",
        )
    if not shutil.which("go"):
        return
    if (gates.REPO_ROOT / "tools/lint/check_go_determinism").is_dir():
        print("--> Running Go Test Determinism & State Leakage Checker...")
        if gates.exec_cmd(["go", "run", "./tools/lint/check_go_determinism"]).returncode != 0:
            ctx.failed = True
    print("--> Checking Go Module Dependencies (go mod tidy)...")
    gates.run_mode_command(
        ctx,
        ["go", "mod", "tidy"],
        check_args=["-diff"],
        fix_args=[],
        failure_msg="Unused or missing Go module dependencies found. Run 'go mod tidy' to sync.",
    )


_UI_EXCLUDES = gates.COMMON_EXCLUDES | frozenset({"dist", "coverage", "out-tsc"})
_UI_TYPECHECK_PATTERN = (
    r"^cmd/ui/(.*\.ts|([^/]+/)*tsconfig[^/]*\.json|([^/]+/)*package(-lock)?\.json)$"
)


def _run_ui_eslint(ctx: gates.RunContext, ui_files: Sequence[str], *, run_all_eslint: bool) -> None:
    ui_lint_files = [
        f.removeprefix("cmd/ui/")
        for f in ui_files
        if f != "cmd/ui/index.html" and f.endswith((".ts", ".html"))
    ]
    if not run_all_eslint and not ui_lint_files:
        return
    eslint = gates.resolve_ui_binary(ctx, "eslint")
    if not eslint:
        return
    print("--> Running UI Linter (ESLint)...")
    targets = ["."] if run_all_eslint else ui_lint_files
    gates.run_mode_command(
        ctx,
        [eslint],
        check_args=[],
        fix_args=["--fix"],
        failure_msg="ESLint check failed.",
        cwd=gates.REPO_ROOT / "cmd/ui",
        files=targets,
        chunk_size=200,
    )


def check_ui(ctx: gates.RunContext) -> None:
    """Runs Prettier, ESLint (cwd=`cmd/ui`), and `tsc` typecheck on Angular UI sources."""
    if ctx.mode == "check":
        ui_files = gates.walk_repo_files(
            ("cmd/ui",), suffixes=(".ts", ".html", ".scss"), excluded_dirs=_UI_EXCLUDES
        )
    else:
        regex = re.compile(r"^cmd/ui/.*\.(ts|html|scss)$")
        ui_files = [p for p in gates.git_staged_paths("ACMR") if regex.search(p)]
    run_all_eslint = ctx.mode == "check" or gates.staged_paths_match(
        "AM", r"^cmd/ui/eslint\.config\.js$"
    )
    run_typecheck = ctx.mode == "check" or gates.staged_paths_match("AMD", _UI_TYPECHECK_PATTERN)
    if not ui_files and not run_all_eslint and not run_typecheck:
        return
    if ui_files and (prettier := gates.resolve_ui_binary(ctx, "prettier")):
        print("--> Running UI Formatter (Prettier)...")
        gates.run_mode_command(
            ctx,
            [prettier, "--config", "cmd/ui/.prettierrc"],
            check_args=["--check"],
            fix_args=["--write"],
            failure_msg="Prettier check failed.",
            files=ui_files,
            chunk_size=200,
        )
    _run_ui_eslint(ctx, ui_files, run_all_eslint=run_all_eslint)
    if run_typecheck and gates.resolve_ui_binary(ctx, "tsc"):
        print("--> Running UI Typechecker (tsc)...")
        if (
            gates.exec_cmd(
                ["npm", "run", "--silent", "typecheck"], cwd=gates.REPO_ROOT / "cmd/ui"
            ).returncode
            != 0
        ):
            ctx.fail("UI TypeScript typecheck failed.")


def check_license(ctx: gates.RunContext) -> None:
    """Verifies or adds Apache license headers using `addlicense`."""
    addlicense = gates.resolve_go_tool(
        ctx, "addlicense", "addlicense not found. License check skipped."
    )
    if not addlicense:
        return
    print("--> Running License Header Check (addlicense)...")
    flags = ["-c", "Google LLC", "-l", "apache"]
    for pattern in _LICENSE_IGNORE_PATTERNS.split():
        flags.extend(("-ignore", pattern))
    files = ["."] if ctx.mode == "check" else gates.git_staged_paths("ACMR")
    if files:
        gates.run_mode_command(
            ctx,
            [addlicense],
            check_args=["-check", *flags],
            fix_args=flags,
            failure_msg="License header check failed.",
            files=files,
        )


def check_python(ctx: gates.RunContext) -> None:
    """Runs Ruff lint and formatting locally or inside Docker."""
    files = (
        ["."] if ctx.mode == "check" else gates.collect_target_files("fix", staged_regex=r"\.py$")
    )
    if not files:
        return
    resolved = gates.resolve_local_or_docker(
        ctx,
        "ruff",
        docker_image="ghcr.io/astral-sh/ruff:latest",
        mount="/io",
        missing_label="Python linting skipped.",
        extra_dirs=(Path.home() / ".local/bin",),
    )
    if not resolved:
        return
    base, is_docker = resolved
    print(f"--> Running Ruff ({'Docker' if is_docker else 'Linter & Formatter'})...")
    extra = ["--no-cache"] if is_docker else []
    for sub, chk, fx, msg in (
        (["check", *extra], [], ["--fix"], "Ruff lint check failed."),
        (["format", *extra], ["--check"], [], "Ruff format check failed."),
    ):
        gates.run_mode_command(
            ctx, [*base, *sub], check_args=chk, fix_args=fx, failure_msg=msg, files=files
        )


def _resolve_mypy_cmd() -> list[str]:
    for venv_name in ("tools/common/venv", "tools/venv"):
        for exe in ("bin/mypy", "Scripts/mypy.exe"):
            if (candidate := gates.REPO_ROOT / venv_name / exe).is_file():
                return [str(candidate)]
    if found := gates.resolve_binary("mypy", (Path.home() / ".local/bin",)):
        return [found]
    mod = [sys.executable, "-m", "mypy"]
    if gates.exec_cmd([*mod, "--version"], quiet_out=True, quiet_err=True).returncode == 0:
        return mod
    print("--> mypy not found; attempting automatic installation into ./tools/common/venv...")
    venv = gates.REPO_ROOT / "tools/common/venv"
    if not venv.exists():
        gates.exec_cmd([sys.executable, "-m", "venv", str(venv)], quiet_out=True, quiet_err=True)
    py = venv / ("Scripts/python.exe" if (venv / "Scripts/python.exe").is_file() else "bin/python")
    install_cmd = [str(py), "-m", "pip", "install", "--quiet", "mypy"]
    if py.is_file() and gates.exec_cmd(install_cmd, quiet_out=True, quiet_err=True).returncode == 0:
        return [str(py), "-m", "mypy"]
    return []


def check_mypy(ctx: gates.RunContext) -> None:
    """Runs mypy static type checker when Python files or config change."""
    if ctx.mode == "fix" and not gates.staged_paths_match(
        "AMD", r"\.pyi?$|^pyproject\.toml$|(^|/)\.gitignore$"
    ):
        return
    if not (cmd := _resolve_mypy_cmd()):
        ctx.fail("ERROR: mypy not found and automatic install failed (run 'pip install mypy').")
        return
    print("--> Running Mypy Static Type Checker...")
    if gates.exec_cmd(cmd).returncode != 0:
        ctx.fail("Mypy type check failed.")


_ACTIONLINT_HINT = "Install: go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12"
_SHELLCHECK_HINT = "Install: sudo apt-get install shellcheck (or brew install shellcheck)"
_SHELLCHECK_EXTRA_DIRS = (Path.home() / ".local/bin",)


def check_workflows(ctx: gates.RunContext) -> None:
    """Runs `actionlint` across `.github/workflows`."""
    if ctx.mode == "check" and not gates.git_ls_files(
        [".github/workflows/*.yml", ".github/workflows/*.yaml"]
    ):
        return
    if ctx.mode == "fix" and not gates.staged_paths_match(
        "AMD", r"^\.github/(workflows/|actions/)"
    ):
        return
    actionlint = gates.resolve_go_tool(
        ctx, "actionlint", "actionlint not found. Workflow linting skipped.", _ACTIONLINT_HINT
    )
    if not actionlint:
        return
    shellcheck = gates.resolve_binary("shellcheck", _SHELLCHECK_EXTRA_DIRS)
    cmd = [actionlint, f"-shellcheck={shellcheck}"] if shellcheck else [actionlint]
    if not shellcheck:
        ctx.warn_missing(
            "shellcheck not found. Workflow run-block shellcheck skipped.", _SHELLCHECK_HINT
        )
    print("--> Running GitHub Actions Workflow Linter (actionlint)...")
    if gates.exec_cmd(cmd, cwd=gates.REPO_ROOT).returncode != 0:
        ctx.fail("actionlint failed.")


def check_shell(ctx: gates.RunContext) -> None:
    """Runs `shellcheck` across tracked or staged shell scripts."""
    if ctx.mode == "check":
        files = gates.git_ls_files(["*.sh", "*.bash"])
    else:
        files = [p for p in gates.git_staged_paths("ACMR") if p.endswith((".sh", ".bash"))]
    if not files:
        return
    shellcheck = gates.resolve_binary("shellcheck", _SHELLCHECK_EXTRA_DIRS)
    if not shellcheck:
        ctx.warn_missing("shellcheck not found. Shell script linting skipped.", _SHELLCHECK_HINT)
        return
    print("--> Running Shell Script Linter (shellcheck)...")
    for batch in gates.chunked(files, 500):
        if (
            gates.exec_cmd(
                [shellcheck, "-S", "style", "-e", "SC1091", "--", *batch], cwd=gates.REPO_ROOT
            ).returncode
            != 0
        ):
            ctx.fail("shellcheck failed.")


_NONUI_JS_GLOBS = (
    ":(glob).github/scripts/**/*.js",
    ":(glob)tools/nightly_tests/**/*.js",
    ":(glob)cmd/experiment_state_server/static/**/*.js",
)
_NONUI_JS_PATTERN = (
    r"^(\.github/scripts|tools/nightly_tests|cmd/experiment_state_server/static)/.*\.js$"
)


def check_nonui_js(ctx: gates.RunContext) -> None:
    """Runs ESLint on non-UI JavaScript files using `tools/lint/eslint.nonui.config.mjs`."""
    if ctx.mode == "check" or gates.staged_paths_match(
        "ACMR", r"^tools/lint/eslint\.nonui\.config\.mjs$"
    ):
        files = gates.git_ls_files(_NONUI_JS_GLOBS)
    else:
        regex = re.compile(_NONUI_JS_PATTERN)
        files = [p for p in gates.git_staged_paths("ACMR") if regex.search(p)]
    if not files:
        return
    eslint = gates.resolve_ui_binary(ctx, "eslint")
    if not eslint:
        return
    print("--> Running Non-UI JS Linter (ESLint)...")
    gates.run_mode_command(
        ctx,
        [eslint, "--no-config-lookup", "-c", "tools/lint/eslint.nonui.config.mjs"],
        check_args=[],
        fix_args=["--fix"],
        failure_msg="Non-UI JS ESLint check failed.",
        files=files,
    )


def _autostage(subcommand: str, state_path: str) -> bool:
    cmd = [sys.executable, "tools/lint/precommit_autostage.py", subcommand, "--state", state_path]
    return gates.exec_cmd(cmd, quiet_out=True).returncode == 0


def _run_linter_phase(mode: str) -> bool:
    ctx = gates.RunContext(mode=mode)
    autostage = mode == "fix" and os.environ.get("XMC_PRECOMMIT_NO_AUTOSTAGE", "0") != "1"
    with tempfile.TemporaryDirectory() as tmpdir:
        state_path = str(Path(tmpdir) / "autostage.json") if autostage else None
        if state_path is not None and not _autostage("snapshot", state_path):
            print("❌ PRE-COMMIT HOOK FAILED: Unable to snapshot staged state for autostage.")
            return False
        for fn in (check_bazel, check_go):
            fn(ctx)
        for gate in gates.PYTHON_GATES:
            gates.run_python_gate(ctx, gate)
        for fn in (
            check_ui,
            check_license,
            check_python,
            check_mypy,
            deploy_checks.check_deploy,
            check_workflows,
            check_shell,
            check_nonui_js,
        ):
            fn(ctx)
        if mode == "fix":
            if state_path is not None and not _autostage("restage", state_path):
                print('\n❌ PRE-COMMIT HOOK FAILED: Linters fixed unstaged files (see "blocked").')
                return False
            if state_path is None and (rc := gates.exec_cmd(["git", "diff", "--quiet"]).returncode):
                msg = (
                    "git diff failed."
                    if rc != 1
                    else "Linters fixed formatting! Stage and re-commit."
                )
                print(f"\n❌ PRE-COMMIT HOOK FAILED: {msg}")
                return False
    if ctx.failed:
        print("=== ❌ Linter checks failed! ===")
    return not ctx.failed


def _run_test_phase(mode: str) -> bool:
    ctx = gates.RunContext(mode=mode)
    if mode == "fix":
        staged = gates.git_staged_paths("ACDMRTUXB")
        if staged and all(p.endswith(".md") or p.startswith("docs/") for p in staged):
            print("--> Skipping Affected Bazel Unit Tests: every staged path is documentation.")
            return True
    if not shutil.which("bazel"):
        ctx.warn_missing("bazel not found. Affected Bazel unit tests skipped.")
        return not ctx.failed
    print("--> Running Affected Bazel Unit Tests...")
    lock_diff = gates.exec_cmd(["git", "diff", "--quiet", "--", "MODULE.bazel.lock"])
    test_cmd = [sys.executable, "./tools/run_affected_tests.py", "--skip-emulator-check"]
    if gates.exec_cmd(test_cmd).returncode != 0:
        ctx.fail("Affected Bazel unit tests failed.")
    if lock_diff.returncode == 0:
        gates.exec_cmd(["git", "checkout", "--", "MODULE.bazel.lock"], quiet_err=True)
    if ctx.failed:
        print("=== ❌ Pre-commit checks failed! ===")
    return not ctx.failed


def main(argv: Sequence[str] | None = None) -> int:
    """Runs all XMC pre-commit checks and returns 0 on success or 1 on failure."""
    os.environ.setdefault("XMC_BYPASS_VENV", "1")
    parser = argparse.ArgumentParser(description="Run XMC pre-commit hooks.")
    parser.add_argument("--check", action="store_true", help="Run in read-only check mode.")
    mode = "check" if parser.parse_args(argv).check else "fix"
    print(f"=== Running XMC Pre-Commit Linter Hooks (Mode: {mode}) ===")
    if not _run_linter_phase(mode) or not _run_test_phase(mode):
        return 1
    print("=== ✅ All pre-commit checks passed! ===")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
