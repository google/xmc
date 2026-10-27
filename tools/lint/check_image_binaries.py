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

"""Fails when a published container image packages a development-only Go binary.

Every `oci_image_for_service` target must package a `go_binary` from this repository that is
neither a `*_dev` binary nor built with a development build tag such as `mockauth`, which
compiles in authentication bypasses. Images are what production deploys, so they must always
be built from the untagged binary.
"""

import argparse
import ast
import dataclasses
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

DEV_BUILD_TAGS = frozenset({"mockauth"})
DEV_BINARY_SUFFIX = "_dev"
IMAGE_MACRO = "oci_image_for_service"
_BUILD_FILE_NAMES = frozenset({"BUILD", "BUILD.bazel"})
_IMAGE_CALL = re.compile(rf"\b{IMAGE_MACRO}\s*\(")
_IMAGE_ALIAS = re.compile(rf"\w\s*=\s*[\"']{IMAGE_MACRO}[\"']")


@dataclasses.dataclass(frozen=True)
class NonLiteral:
    """A keyword argument value that is not a literal (for example a select() expression)."""

    expression: str


@dataclasses.dataclass(frozen=True)
class Call:
    """A rule or macro call in a BUILD file with its keyword arguments."""

    rule: str
    kwargs: dict[str, Any]


def parse_calls(source: str) -> list[Call]:
    """Returns top-level rule calls in a BUILD file; non-literal arguments become NonLiteral."""
    calls = []
    for node in ast.parse(source).body:
        if not (isinstance(node, ast.Expr) and isinstance(node.value, ast.Call)):
            continue
        func = node.value.func
        if not isinstance(func, ast.Name):
            continue
        kwargs = {kw.arg: _literal_or_marker(kw.value) for kw in node.value.keywords if kw.arg}
        calls.append(Call(rule=func.id, kwargs=kwargs))
    return calls


def _literal_or_marker(value: ast.expr) -> Any:
    try:
        return ast.literal_eval(value)
    except (ValueError, TypeError, SyntaxError, RecursionError):
        return NonLiteral(ast.unparse(value))


def resolve_label(package: str, label: Any) -> str:
    """Returns the absolute `//package:name` form of a label relative to package."""
    if isinstance(label, NonLiteral):
        return f"<non-literal: {label.expression}>"
    if not isinstance(label, str):
        return f"<invalid: {label!r}>"
    if label.startswith(":"):
        return f"//{package}{label}"
    if label.startswith("//") and ":" not in label:
        return f"{label}:{label.rsplit('/', 1)[-1]}"
    return label


@dataclasses.dataclass
class Targets:
    """Targets collected from BUILD files.

    Attributes:
        go_binaries: gotags (a list or NonLiteral) keyed by go_binary label.
        images: (image label, resolved binary label) pairs.
        unparsed_images: BUILD files whose textual image call count differs from the parsed count,
            for example because the macro is called from a loop, under an alias or with a
            non-literal name.
    """

    go_binaries: dict[str, Any] = dataclasses.field(default_factory=dict)
    images: list[tuple[str, str]] = dataclasses.field(default_factory=list)
    unparsed_images: list[str] = dataclasses.field(default_factory=list)


def collect_targets(build_files: dict[str, str]) -> Targets:
    """Returns go_binary gotags, image/binary pairs and unparsed image calls from BUILD sources."""
    targets = Targets()
    for path, source in build_files.items():
        parent = Path(path).parent.as_posix()
        package = "" if parent == "." else parent
        parsed_images = 0
        for call in parse_calls(source):
            name = call.kwargs.get("name")
            if not isinstance(name, str):
                continue
            if call.rule == "go_binary":
                targets.go_binaries[f"//{package}:{name}"] = call.kwargs.get("gotags", [])
            if call.rule == IMAGE_MACRO:
                binary = resolve_label(package, call.kwargs.get("binary"))
                targets.images.append((f"//{package}:{name}", binary))
                parsed_images += 1
        if _IMAGE_ALIAS.search(source) or len(_IMAGE_CALL.findall(source)) != parsed_images:
            targets.unparsed_images.append(path)
    return targets


def find_violations(targets: Targets) -> list[dict[str, str]]:
    """Returns unparsed image calls and images whose binary is unresolved or development-only."""
    violations = [
        {"build_file": path, "reason": f"{IMAGE_MACRO} is called in a form this check cannot parse"}
        for path in targets.unparsed_images
    ]
    for image, binary in targets.images:
        reason = _violation_reason(binary, targets.go_binaries)
        if reason:
            violations.append({"image": image, "binary": binary, "reason": reason})
    return violations


def _violation_reason(binary: str, go_binaries: dict[str, Any]) -> str:
    if binary not in go_binaries:
        return "binary is not a go_binary declared in this repository"
    if binary.endswith(DEV_BINARY_SUFFIX):
        return f"binary name ends with {DEV_BINARY_SUFFIX}"
    gotags = go_binaries[binary]
    if isinstance(gotags, NonLiteral):
        return f"binary gotags are not a literal list: {gotags.expression}"
    dev_tags = sorted(DEV_BUILD_TAGS.intersection(gotags))
    if dev_tags:
        return f"binary is built with development gotags {dev_tags}"
    return ""


def list_build_files(repo_root: Path) -> dict[str, str]:
    """Returns the sources of git-tracked BUILD files keyed by repository-relative path."""
    result = subprocess.run(
        ["git", "ls-files", "-z"], cwd=repo_root, capture_output=True, check=True
    )
    paths = [p for p in result.stdout.decode().split("\0") if Path(p).name in _BUILD_FILE_NAMES]
    return {
        p: (repo_root / p).read_text(encoding="utf-8") for p in paths if (repo_root / p).is_file()
    }


def check_image_binaries(args: argparse.Namespace) -> dict[str, Any]:
    """Checks every oci_image_for_service target and raises on any violation."""
    build_files = list_build_files(Path(args.repo_root).resolve())
    targets = collect_targets(build_files)
    violations = find_violations(targets)
    if violations:
        raise HandledError(
            f"{len(violations)} image(s) package a development-only binary or cannot be checked",
            "Point oci_image_for_service at the untagged go_binary; keep *_dev binaries for local use only.",
            details={"violations": violations},
        )
    if not targets.images:
        raise HandledError(
            f"no {IMAGE_MACRO} targets found",
            "Run from the repository root or pass --repo-root; the check must inspect at least one image.",
        )
    return {"images_checked": len(targets.images)}


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    run_tool(parser, check_image_binaries)


if __name__ == "__main__":
    main()
