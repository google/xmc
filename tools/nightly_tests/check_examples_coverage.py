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

"""Verification gate enforcing nightly E2E test coverage for XMC examples.

Scans `examples/` subdirectories and verifies that every example is either:
1. Fully wired into the Nightly Examples E2E suite:
   - `tools/nightly_tests/<name>_verify.py` exists,
   - `tools/nightly_tests/BUILD.bazel` defines `py_binary` `<name>_verify`,
   - `.github/workflows/nightly-examples-e2e.yml` includes `examples/<name>` and
     `tools.nightly_tests.<name>_verify`; OR
2. Explicitly opted out in `EXEMPT_EXAMPLES`.
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import pathlib
import re
import sys
from collections.abc import Collection, Sequence

TOTAL_ARTIFACTS_PER_EXAMPLE: int = 3

# Single source of truth for `examples/<name>` directories intentionally
# excluded from the Nightly Examples E2E pipeline (`.github/workflows/nightly-examples-e2e.yml`).
# During pre-commit and CI linting (`lint.yml`), every subdirectory under `examples/`
# must either provide all 3 nightly verification artifacts (`<name>_verify.py`,
# `BUILD.bazel` py_binary target, and workflow matrix entry) or be listed in
# `EXEMPT_EXAMPLES`. When adding an entry, please include a code comment
# explaining why nightly E2E verification does not apply.
# The gate cross-checks entries against existing `examples/` subdirectories to catch
# stale exemptions and fails if a covered or partially wired example remains listed here.
EXEMPT_EXAMPLES: tuple[str, ...] = (
    # Contains declarative component profile YAML manifests rather than a runnable workload example.
    "components",
    # Targets experimental xm_local Lite execution rather than the GKE control plane and Kueue ClusterQueue.
    "xmc_lite_job",
)


@dataclasses.dataclass(frozen=True)
class CoverageReport:
    """Structured result of auditing `examples/` nightly E2E test coverage."""

    covered: list[str]
    exempt: list[str]
    violations: list[str]


def collect_missing_artifacts(
    name: str,
    tests_dir: pathlib.Path,
    build_text: str,
    workflow_text: str,
) -> list[str]:
    """Returns which of the 3 nightly verification artifacts are missing for `name`."""
    missing: list[str] = []
    if not (tests_dir / f"{name}_verify.py").is_file():
        missing.append(f"tools/nightly_tests/{name}_verify.py")
    if not re.search(rf'name\s*=\s*["\']{re.escape(name)}_verify["\']', build_text):
        missing.append(f"py_binary '{name}_verify' in tools/nightly_tests/BUILD.bazel")
    if (
        f"examples/{name}" not in workflow_text
        or f"tools.nightly_tests.{name}_verify" not in workflow_text
    ):
        missing.append(
            f"matrix entry ('examples/{name}' & 'tools.nightly_tests.{name}_verify') "
            "in .github/workflows/nightly-examples-e2e.yml"
        )
    return missing


def validate_exempt_registry(
    exempt_examples: Collection[str],
    existing_names: set[str] | None = None,
) -> list[str]:
    """Validates that every entry in `exempt_examples` corresponds to an existing `examples/` directory.

    Args:
        exempt_examples: Collection of exempted example directory names.
        existing_names: Optional set of existing example directory names under `examples/`.

    Returns:
        List of violation messages for stale registry entries.
    """
    if existing_names is None:
        return []
    return [
        f"EXEMPT_EXAMPLES contains stale exemption for non-existent example '{name}'."
        for name in sorted(set(exempt_examples))
        if name not in existing_names
    ]


def evaluate_single_example(
    example_dir: pathlib.Path,
    tests_dir: pathlib.Path,
    build_text: str,
    workflow_text: str,
    exempt_examples: Collection[str],
) -> tuple[str, str | None]:
    """Evaluates nightly verification coverage for a single `examples/<name>` directory."""
    name = example_dir.name
    missing = collect_missing_artifacts(name, tests_dir, build_text, workflow_text)
    if not missing:
        if name in exempt_examples:
            msg = f"examples/{name} has a complete nightly verification suite; remove redundant '{name}' from EXEMPT_EXAMPLES."
            return "violation", msg
        return "covered", None
    if len(missing) < TOTAL_ARTIFACTS_PER_EXAMPLE:
        return (
            "violation",
            f"examples/{name} has partial nightly verification wiring (missing: {', '.join(missing)}).",
        )
    if name in exempt_examples:
        return "exempt", None
    return (
        "violation",
        f"examples/{name} is missing a nightly E2E verification suite (missing: "
        f"{', '.join(missing)}). Either add a verification suite (see "
        f"tools/nightly_tests/README.md) or add '{name}' to EXEMPT_EXAMPLES "
        "in tools/nightly_tests/check_examples_coverage.py with an explanatory comment.",
    )


def _list_example_dirs(examples_dir: pathlib.Path) -> list[pathlib.Path]:
    """Lists all non-hidden example subdirectories under `examples_dir`."""
    if not examples_dir.is_dir():
        return []
    return sorted(
        path
        for path in examples_dir.iterdir()
        if path.is_dir() and not path.name.startswith(".") and path.name != "__pycache__"
    )


def _read_repo_texts(repo_root: pathlib.Path) -> tuple[pathlib.Path, str, str]:
    """Loads `BUILD.bazel` and `nightly-examples-e2e.yml` text from `repo_root`."""
    tests_dir = repo_root / "tools" / "nightly_tests"
    build_path = tests_dir / "BUILD.bazel"
    workflow_path = repo_root / ".github" / "workflows" / "nightly-examples-e2e.yml"
    build_text = build_path.read_text(encoding="utf-8") if build_path.is_file() else ""
    workflow_text = workflow_path.read_text(encoding="utf-8") if workflow_path.is_file() else ""
    return tests_dir, build_text, workflow_text


def audit_examples_coverage(
    repo_root: pathlib.Path,
    exempt_examples: Collection[str] | None = None,
) -> CoverageReport:
    """Audits all `examples/` subdirectories for nightly E2E verification coverage."""
    examples_dir = repo_root / "examples"
    if not examples_dir.is_dir():
        msg = f"Invalid repository root: '{repo_root}' (examples/ directory not found)."
        return CoverageReport(covered=[], exempt=[], violations=[msg])
    registry = EXEMPT_EXAMPLES if exempt_examples is None else exempt_examples
    example_dirs = _list_example_dirs(examples_dir)
    violations = validate_exempt_registry(registry, {d.name for d in example_dirs})
    tests_dir, build_text, workflow_text = _read_repo_texts(repo_root)
    covered, exempt = [], []
    for example_dir in example_dirs:
        category, violation = evaluate_single_example(
            example_dir, tests_dir, build_text, workflow_text, registry
        )
        if category == "covered":
            covered.append(example_dir.name)
        elif category == "exempt":
            exempt.append(example_dir.name)
        elif violation:
            violations.append(violation)
    return CoverageReport(covered=covered, exempt=exempt, violations=violations)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    """Parses command-line arguments for the examples coverage gate CLI.

    Args:
        argv: Optional list of CLI arguments (excluding program name).

    Returns:
        Parsed `argparse.Namespace`.
    """
    default_root = pathlib.Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(
        description="Verify that every example under examples/ has a nightly E2E verification suite or explicit opt-out."
    )
    parser.add_argument(
        "--repo-root",
        type=pathlib.Path,
        default=default_root,
        help="Path to the repository root directory.",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entrypoint for checking `examples/` nightly E2E verification coverage.

    Args:
        argv: Optional sequence of command-line arguments.

    Returns:
        Exit code `0` if all examples are covered or explicitly opted out, `1` otherwise.
    """
    args = parse_args(argv)
    report = audit_examples_coverage(args.repo_root.resolve())
    if report.violations:
        payload = {
            "status": "error",
            "message": "Nightly E2E example verification coverage check failed.",
            "instruction": (
                "Follow tools/nightly_tests/README.md to add a verification suite "
                "or add the example to EXEMPT_EXAMPLES with an explanatory comment."
            ),
            "violations": report.violations,
        }
        print(json.dumps(payload, indent=2), file=sys.stderr)
        for violation in report.violations:
            print(f"❌ {violation}", file=sys.stderr)
        return 1
    print(json.dumps({"status": "ok", "result": dataclasses.asdict(report)}, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
