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

"""In-container smoke verifier for XMC GHCR CI runner images."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

VALID_ROLES: tuple[str, ...] = ("base", "lint")
_CMD_TIMEOUT_SECONDS = 60


@dataclass(frozen=True)
class CommandExpectation:
    """Declarative command assertion against combined stdout/stderr."""

    name: str
    argv: tuple[str, ...]
    expected_substrings: tuple[str, ...] = ()


def _tool_version(manifest: Mapping[str, Any], tool_key: str) -> str:
    """Extracts a required version string from the manifest tools section.

    Args:
        manifest: Parsed versions.json mapping.
        tool_key: Key under ``manifest["tools"]``.

    Returns:
        Non-empty version string.

    Raises:
        ValueError: If the tool entry or version field is absent.
    """
    tools = manifest.get("tools")
    if not isinstance(tools, Mapping):
        raise ValueError("Manifest is missing 'tools' mapping.")
    entry = tools.get(tool_key)
    if not isinstance(entry, Mapping) or not isinstance(entry.get("version"), str):
        raise ValueError(f"Manifest is missing version for tool '{tool_key}'.")
    return str(entry["version"])


def _run_command_expectation(
    spec: CommandExpectation,
    runner: Callable[..., subprocess.CompletedProcess[str]],
) -> str | None:
    """Runs one command assertion and returns an error string on failure.

    Args:
        spec: Declarative command expectation.
        runner: Subprocess runner callable (injectable for testing).

    Returns:
        Failure description string, or ``None`` when the assertion passes.
    """
    try:
        proc = runner(
            list(spec.argv),
            capture_output=True,
            text=True,
            timeout=_CMD_TIMEOUT_SECONDS,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        return f"{spec.name}: failed to execute {spec.argv[0]}: {exc}"

    combined = f"{proc.stdout}\n{proc.stderr}".strip()
    if proc.returncode != 0:
        return f"{spec.name}: exited {proc.returncode}: {combined[:300]}"
    for expected in spec.expected_substrings:
        if expected not in combined:
            return f"{spec.name}: expected '{expected}' in output: {combined[:300]}"
    return None


def _base_expectations(manifest: Mapping[str, Any]) -> tuple[CommandExpectation, ...]:
    """Builds command expectations common to all CI runner images.

    Args:
        manifest: Parsed versions.json mapping.

    Returns:
        Tuple of base ``CommandExpectation`` objects.
    """
    py_ver = _tool_version(manifest, "python")
    node_ver = _tool_version(manifest, "node")
    return (
        CommandExpectation("python3", ("python3", "--version"), (py_ver,)),
        CommandExpectation("node", ("node", "--version"), (f"v{node_ver}",)),
        CommandExpectation("git", ("git", "--version"), ("git version",)),
        CommandExpectation(
            "git-safe-dir",
            ("git", "config", "--system", "--get-all", "safe.directory"),
            ("*",),
        ),
        CommandExpectation("jq", ("jq", "--version"), ("jq-",)),
        CommandExpectation("curl", ("curl", "--version"), ("curl ",)),
    )


def _lint_expectations(manifest: Mapping[str, Any]) -> tuple[CommandExpectation, ...]:
    """Builds command expectations specific to the ci-lint image.

    Args:
        manifest: Parsed versions.json mapping.

    Returns:
        Tuple of lint ``CommandExpectation`` objects.
    """
    go_ver = _tool_version(manifest, "go")
    gl_ver = _tool_version(manifest, "golangci_lint")
    return (
        CommandExpectation("go", ("go", "version"), (f"go{go_ver}",)),
        CommandExpectation("ruff", ("ruff", "--version"), (_tool_version(manifest, "ruff"),)),
        CommandExpectation("mypy", ("mypy", "--version"), (_tool_version(manifest, "mypy"),)),
        CommandExpectation(
            "golangci-lint", ("golangci-lint", "--version"), (gl_ver, f"go{go_ver}")
        ),
        CommandExpectation(
            "actionlint", ("actionlint", "-version"), (_tool_version(manifest, "actionlint"),)
        ),
        CommandExpectation(
            "buildifier", ("buildifier", "-version"), (_tool_version(manifest, "buildifier"),)
        ),
        CommandExpectation(
            "shellcheck", ("shellcheck", "--version"), (_tool_version(manifest, "shellcheck"),)
        ),
        CommandExpectation(
            "terraform", ("terraform", "version"), (f"v{_tool_version(manifest, 'terraform')}",)
        ),
        CommandExpectation("tflint", ("tflint", "--version"), (_tool_version(manifest, "tflint"),)),
        CommandExpectation(
            "helm", ("helm", "version", "--short"), (f"v{_tool_version(manifest, 'helm')}",)
        ),
        CommandExpectation("trivy", ("trivy", "--version"), (_tool_version(manifest, "trivy"),)),
    )


def _check_lint_extras(manifest: Mapping[str, Any], env: Mapping[str, str]) -> list[str]:
    """Verifies addlicense binary presence and pre-initialized TFLint Google plugin.

    Args:
        manifest: Parsed versions.json mapping.
        env: Process environment mapping.

    Returns:
        List of failure messages (empty when all checks pass).
    """
    failures: list[str] = []
    if shutil.which("addlicense") is None:
        failures.append("addlicense: binary not found on PATH")

    plugin_dir = env.get("TFLINT_PLUGIN_DIR", "")
    plugin_ver = _tool_version(manifest, "tflint_google_plugin")
    expected_plugin = (
        Path(plugin_dir) / "github.com" / "terraform-linters" / "tflint-ruleset-google" / plugin_ver
    )
    if not plugin_dir or not expected_plugin.is_dir():
        failures.append(f"tflint-google-plugin: missing pre-cached plugin at {expected_plugin}")
    return failures


def run_smoke_checks(
    role: str,
    manifest: Mapping[str, Any],
    env: Mapping[str, str] | None = None,
    runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
) -> list[str]:
    """Executes all smoke assertions for a CI runner image role.

    Args:
        role: Target role (``base``, ``lint``).
        manifest: Parsed versions.json mapping.
        env: Optional environment mapping (defaults to ``os.environ``).
        runner: Subprocess runner callable.

    Returns:
        List of failure messages; empty list indicates all checks passed.

    Raises:
        ValueError: If ``role`` is not in ``VALID_ROLES``.
    """
    if role not in VALID_ROLES:
        raise ValueError(f"Unsupported role '{role}'. Expected one of: {', '.join(VALID_ROLES)}")

    active_env = os.environ if env is None else env
    specs: list[CommandExpectation] = list(_base_expectations(manifest))
    failures: list[str] = []

    if role == "lint":
        specs.extend(_lint_expectations(manifest))
        failures.extend(_check_lint_extras(manifest, active_env))

    for spec in specs:
        err = _run_command_expectation(spec, runner)
        if err:
            failures.append(err)
    return failures


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """Parses CLI arguments for container smoke tests.

    Args:
        argv: Optional CLI argument sequence.

    Returns:
        Parsed ``argparse.Namespace``.
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--role", choices=VALID_ROLES, required=True, help="Image role to verify.")
    parser.add_argument("--versions", type=Path, required=True, help="Path to versions.json.")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entrypoint for in-container image smoke verification.

    Args:
        argv: Optional CLI arguments.

    Returns:
        Exit code 0 if all assertions pass, 1 otherwise.
    """
    args = parse_args(argv)
    try:
        manifest = json.loads(args.versions.read_text(encoding="utf-8"))
        if not isinstance(manifest, Mapping):
            raise ValueError("Manifest root must be a JSON object.")
        failures = run_smoke_checks(args.role, manifest)
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(f"SMOKE TEST ERROR: {exc}", file=sys.stderr)
        return 1

    if failures:
        for failure in failures:
            print(f"FAIL: {failure}", file=sys.stderr)
        return 1
    print(f"PASS: all '{args.role}' image smoke checks succeeded.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
