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

"""Ratchet lint that tracks remaining google3 migration leftovers.

Counts google3-era constructs (legacy Go import roots, unresolved module
import paths, compat.bzl targets, gazelle resolve overrides, third_party proto
paths, google3 runtime flags in Helm, and legacy UI/Python paths) and compares
them against a checked-in baseline. The check fails when any count grows, and
also when a count shrinks without the baseline being lowered, so that progress
is locked in. See https://github.com/google/xmc/issues/1066.
"""

import argparse
import dataclasses
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

DEFAULT_BASELINE = Path(__file__).resolve().parent / "legacy_imports_baseline.json"
GO_MODULE_PREFIX = "xmanager_cloud/"

# First path segments of google3-style Go import paths (none are Go stdlib).
LEGACY_GO_ROOTS = frozenset(
    {
        "api",
        "cloud_spanner_emulator",
        "golang",
        "googlesql",
        "grpc_proto",
        "longrunning",
        "prometheus_model",
        "protobuf",
        "rpc",
        "spanner",
        "task",
        "tink",
    }
)
# Exact google3 import paths whose root collides with Go stdlib (`go/...`).
LEGACY_GO_EXACT = frozenset({"go/flag", "go/google"})

# Files that legitimately contain the patterns this tool searches for.
SELF_EXCLUDED = frozenset(
    {
        "tools/lint/check_legacy_imports.py",
        "tools/lint/check_legacy_imports_test.py",
        "tools/lint/legacy_imports_baseline.json",
    }
)

_IMPORT_SPEC = re.compile(r'^\s*(?:[A-Za-z_][\w]*\s+|[._]\s+)?"([^"]+)"')


@dataclasses.dataclass(frozen=True)
class LineMetric:
    """A metric counting regex matches on lines of files matching path_regex."""

    name: str
    path_regex: str
    line_regex: str


LINE_METRICS = (
    LineMetric(
        name="compat_go_targets",
        path_regex=r"(^|/)(BUILD|BUILD\.bazel)$",
        line_regex=r"^\s*compat_go_(library|test)\(",
    ),
    LineMetric(
        name="gazelle_resolve_directives",
        path_regex=r"(^|/)(BUILD|BUILD\.bazel)$",
        line_regex=r"^#\s*gazelle:resolve\s",
    ),
    LineMetric(
        name="custom_repo_rules",
        path_regex=r"(^|/)(MODULE\.bazel|WORKSPACE|[^/]+\.bzl)$",
        line_regex=r"repository_rule\(|use_extension\(\"//",
    ),
    LineMetric(
        name="proto_third_party_paths",
        path_regex=r"(\.proto|(^|/)BUILD\.bazel)$",
        line_regex=r'(^import\s+"third_party/|import_prefix\s*=\s*"third_party/)',
    ),
    LineMetric(
        name="helm_google3_flags",
        path_regex=r"^deploy/.*\.(ya?ml|tpl)$",
        line_regex=(
            r'"--(logtostderr|alsologtostderr|stderrthreshold|vmodule|'
            r"enforce_kernel_\w+|enable_streamz_\w+|streamz_\w+|securewrapper_\w+)"
        ),
    ),
    LineMetric(
        name="ts_legacy_module_paths",
        path_regex=r"^cmd/ui/.*\.ts$",
        line_regex=r"['\"](javascript/closure/|third_party/|xmanager_cloud/|longrunning/)",
    ),
    LineMetric(
        name="python_legacy_paths",
        path_regex=r"\.py$",
        line_regex=r"third_party[./](xmanager_cloud|javascript)",
    ),
)


def extract_go_imports(source: str) -> list[str]:
    """Returns import paths declared in a Go source file."""
    imports: list[str] = []
    in_block = False
    for line in source.splitlines():
        stripped = line.strip()
        if in_block:
            if stripped.startswith(")"):
                in_block = False
                continue
            match = _IMPORT_SPEC.match(stripped)
            if match:
                imports.append(match.group(1))
            continue
        if stripped.startswith("import ("):
            in_block = True
            continue
        if stripped.startswith("import "):
            match = _IMPORT_SPEC.match(stripped.removeprefix("import "))
            if match:
                imports.append(match.group(1))
    return imports


def is_legacy_root_import(path: str) -> bool:
    """Reports whether a Go import path uses a google3-style root."""
    if path in LEGACY_GO_EXACT:
        return True
    return path.split("/", 1)[0] in LEGACY_GO_ROOTS


def is_unresolved_module_import(path: str, repo_root: Path) -> bool:
    """Reports whether a module-prefixed import does not map to a repo directory."""
    if not path.startswith(GO_MODULE_PREFIX):
        return False
    return not (repo_root / path.removeprefix(GO_MODULE_PREFIX)).is_dir()


def count_go_imports(repo_root: Path, files: list[str]) -> dict[str, int]:
    """Counts legacy-root and unresolved module imports across Go files."""
    counts = {"go_legacy_root_imports": 0, "go_unresolved_module_imports": 0}
    for rel in files:
        if not rel.endswith(".go"):
            continue
        for path in extract_go_imports(_read_text(repo_root / rel)):
            if is_legacy_root_import(path):
                counts["go_legacy_root_imports"] += 1
            elif is_unresolved_module_import(path, repo_root):
                counts["go_unresolved_module_imports"] += 1
    return counts


def count_line_metric(metric: LineMetric, repo_root: Path, files: list[str]) -> int:
    """Counts lines matching a LineMetric across the given files."""
    path_re = re.compile(metric.path_regex)
    line_re = re.compile(metric.line_regex)
    total = 0
    for rel in files:
        if not path_re.search(rel):
            continue
        total += sum(1 for line in _read_text(repo_root / rel).splitlines() if line_re.search(line))
    return total


def collect_metrics(repo_root: Path, files: list[str]) -> dict[str, int]:
    """Computes all ratchet metrics for the given tracked files."""
    scanned = [f for f in files if f not in SELF_EXCLUDED]
    metrics = count_go_imports(repo_root, scanned)
    metrics["third_party_files"] = sum(1 for f in scanned if f.startswith("third_party/"))
    for metric in LINE_METRICS:
        metrics[metric.name] = count_line_metric(metric, repo_root, scanned)
    return dict(sorted(metrics.items()))


def compare_to_baseline(current: dict[str, int], baseline: dict[str, int]) -> dict[str, Any]:
    """Classifies each metric as regressed, improved (stale baseline), or unchanged."""
    regressed, improved = {}, {}
    for name, value in current.items():
        allowed = baseline.get(name, 0)
        if value > allowed:
            regressed[name] = {"baseline": allowed, "current": value}
        elif value < allowed:
            improved[name] = {"baseline": allowed, "current": value}
    return {"regressed": regressed, "improved": improved}


def list_tracked_files(repo_root: Path) -> list[str]:
    """Lists git-tracked files relative to the repository root."""
    result = subprocess.run(
        ["git", "ls-files", "-z"], cwd=repo_root, capture_output=True, check=True
    )
    return [f for f in result.stdout.decode().split("\0") if f]


def load_baseline(path: Path) -> dict[str, int]:
    """Loads the baseline JSON, failing with guidance when it is missing."""
    if not path.is_file():
        raise HandledError(
            f"Baseline file not found: {path}",
            "Run with --update-baseline to create it.",
        )
    return json.loads(path.read_text())


def write_baseline(path: Path, current: dict[str, int], baseline: dict[str, int]) -> None:
    """Writes the baseline, refusing to raise any existing count."""
    regressed = compare_to_baseline(current, baseline)["regressed"] if baseline else {}
    if regressed:
        raise HandledError(
            f"Refusing to raise baseline for: {sorted(regressed)}",
            "Remove the newly added google3-style constructs instead of raising the baseline.",
        )
    path.write_text(json.dumps(current, indent=2, sort_keys=True) + "\n")


def check_legacy_imports(args: argparse.Namespace) -> dict[str, Any]:
    """Runs the ratchet check or updates the baseline."""
    repo_root = Path(args.repo_root).resolve()
    baseline_path = Path(args.baseline)
    current = collect_metrics(repo_root, list_tracked_files(repo_root))
    if args.update_baseline:
        existing = load_baseline(baseline_path) if baseline_path.is_file() else {}
        write_baseline(baseline_path, current, existing)
        return {"updated": True, "metrics": current}
    diff = compare_to_baseline(current, load_baseline(baseline_path))
    if diff["regressed"]:
        raise HandledError(
            f"google3 legacy constructs increased: {json.dumps(diff['regressed'])}",
            "Use OSS import paths and standard Bazel rules; see github.com/google/xmc/issues/1066.",
        )
    if diff["improved"]:
        raise HandledError(
            f"Legacy counts dropped but baseline is stale: {json.dumps(diff['improved'])}",
            "Run tools/lint/check_legacy_imports.py --update-baseline and commit the result.",
        )
    return {"metrics": current, "total": sum(current.values())}


def _read_text(path: Path) -> str:
    return path.read_text(encoding="utf-8", errors="ignore") if path.is_file() else ""


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    parser.add_argument("--baseline", default=str(DEFAULT_BASELINE), help="Baseline JSON path.")
    parser.add_argument(
        "--update-baseline",
        action="store_true",
        help="Rewrite the baseline with current counts (never raises a count).",
    )
    run_tool(parser, check_legacy_imports)


if __name__ == "__main__":
    main()
