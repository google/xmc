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
2. Explicitly opted out with a non-empty justification via
   `examples/<name>/.no_nightly_e2e` or `EXEMPT_EXAMPLES`.
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import pathlib
import re
import sys
from collections.abc import Mapping, Sequence

OPT_OUT_FILENAME: str = ".no_nightly_e2e"
MIN_JUSTIFICATION_LENGTH: int = 10
TOTAL_ARTIFACTS_PER_EXAMPLE: int = 3

EXEMPT_EXAMPLES: dict[str, str] = {
    "components": (
        "Contains declarative component profile YAML manifests rather than a runnable workload example."
    ),
    "xmc_lite_job": (
        "Targets experimental xm_local Lite execution rather than the GKE control plane and Kueue ClusterQueue."
    ),
    "managed_xprof_artifact_job": (
        "Requires GCS Workload Identity Federation bucket provisioning in nightly E2E workflow (pending PR #1099)."
    ),
    "pathways_job": (
        "Targets Pathways TPU ClusterQueue and coordinator topology (pending control-plane verification in PR #1099)."
    ),
    "per_job_node_selector": (
        "Targets multi-pool CPU+GPU nodeSelector constraints on CPU-only nightly cluster (pending PR #1099)."
    ),
    "xdash_dashboard": (
        "Standalone xDash REST/gRPC dashboard builder script without Experiment XID (pending PR #1099)."
    ),
}


@dataclasses.dataclass(frozen=True)
class CoverageReport:
    """Structured result of auditing `examples/` nightly E2E test coverage."""

    covered: list[str]
    exempt: list[str]
    violations: list[str]


def read_opt_out_reason(example_dir: pathlib.Path) -> tuple[bool, str]:
    """Reads and strips comments from an example's `.no_nightly_e2e` marker file.

    Args:
        example_dir: Path to the `examples/<name>` directory.

    Returns:
        Tuple `(exists, reason)` with marker presence and stripped content.
    """
    marker_path = example_dir / OPT_OUT_FILENAME
    if not marker_path.is_file():
        return False, ""
    raw_lines = marker_path.read_text(encoding="utf-8").splitlines()
    lines = [s for line in raw_lines if (s := line.strip()) and not s.startswith("#")]
    return True, " ".join(lines).strip()


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
    exempt_examples: Mapping[str, str],
    existing_names: set[str] | None = None,
) -> list[str]:
    """Validates that every entry in `exempt_examples` exists and has a justification.

    Args:
        exempt_examples: Mapping from example name to exemption justification.
        existing_names: Optional set of existing example directory names under `examples/`.

    Returns:
        List of violation messages for invalid or stale registry entries.
    """
    violations = [
        f"EXEMPT_EXAMPLES['{name}'] must provide a justification of at least "
        f"{MIN_JUSTIFICATION_LENGTH} characters."
        for name, reason in sorted(exempt_examples.items())
        if len(reason.strip()) < MIN_JUSTIFICATION_LENGTH
    ]
    if existing_names is not None:
        violations.extend(
            f"EXEMPT_EXAMPLES contains stale exemption for non-existent example '{name}'."
            for name in sorted(exempt_examples)
            if name not in existing_names
        )
    return violations


def _classify_artifact_state(
    name: str,
    missing: Sequence[str],
    has_marker: bool,
    exempt_examples: Mapping[str, str],
) -> tuple[str, str | None]:
    """Classifies coverage state once marker text validity is verified."""
    if not missing:
        if has_marker:
            return (
                "violation",
                f"examples/{name} has a complete nightly verification suite; remove "
                f"redundant examples/{name}/{OPT_OUT_FILENAME}.",
            )
        return "covered", None
    if len(missing) < TOTAL_ARTIFACTS_PER_EXAMPLE:
        return (
            "violation",
            f"examples/{name} has partial nightly verification wiring (missing: {', '.join(missing)}).",
        )
    if has_marker or name in exempt_examples:
        return "exempt", None
    return (
        "violation",
        f"examples/{name} is missing a nightly E2E verification suite (missing: "
        f"{', '.join(missing)}). Either add a verification suite (see "
        f"tools/nightly_tests/README.md) or create examples/{name}/{OPT_OUT_FILENAME} "
        "with an explicit justification.",
    )


def evaluate_single_example(
    example_dir: pathlib.Path,
    tests_dir: pathlib.Path,
    build_text: str,
    workflow_text: str,
    exempt_examples: Mapping[str, str],
) -> tuple[str, str | None]:
    """Evaluates nightly verification coverage for a single `examples/<name>` directory."""
    name = example_dir.name
    has_marker, marker_reason = read_opt_out_reason(example_dir)
    if has_marker and len(marker_reason) < MIN_JUSTIFICATION_LENGTH:
        return (
            "violation",
            f"examples/{name}/{OPT_OUT_FILENAME} exists but lacks a valid justification "
            f"(>= {MIN_JUSTIFICATION_LENGTH} non-comment characters required).",
        )
    missing = collect_missing_artifacts(name, tests_dir, build_text, workflow_text)
    return _classify_artifact_state(name, missing, has_marker, exempt_examples)


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
    exempt_examples: Mapping[str, str] | None = None,
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
                f"or create examples/<name>/{OPT_OUT_FILENAME} with a justification."
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
