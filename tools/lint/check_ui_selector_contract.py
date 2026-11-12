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

"""Lint keeping nightly UI verifier selectors in sync with the Angular templates.

Parses every tracked `*.py` under NIGHTLY_TESTS_DIR (minus EXCLUDED_SOURCES;
syntax errors fail). Selectors: string values of _SEL_NAME_RE names and the
first positional or `selector=` argument of _SELECTOR_CALLS (`{...}` = wildcard).
Rules: dom-testid (`[data-testid="..."]` values must exist in cmd/ui/app
templates), dom-tag (custom tags must exist in cmd/ui/app templates or component
selectors; FRAMEWORK_PREFIXES exempt), dom-class and dom-attribute (custom
classes and non-standard attributes require `# ui-contract: <reason>`;
FRAMEWORK_PREFIXES and STANDARD_ATTRIBUTES exempt), copy (`:has-text`/`has_text=`
need `# ui-contract: <reason>` on the string's line or a standalone comment line
above), unknown-selector-constant (`alias.NAME` / imported names from
SELECTOR_MODULE must be defined there).
"""

from __future__ import annotations

import argparse
import ast
import re
import subprocess
import sys
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

NIGHTLY_TESTS_DIR = "tools/nightly_tests"
SELECTOR_MODULE = "tools/nightly_tests/verification/ui_css_selectors.py"
_SELECTOR_MODULE_NAME = Path(SELECTOR_MODULE).stem
# Repo-relative files to skip, e.g. future unit tests full of mock selectors.
EXCLUDED_SOURCES: tuple[str, ...] = ()
UI_APP_DIR = "cmd/ui/app"
UI_INDEX_HTML = "cmd/ui/index.html"

# Angular Material / CDK render these tags and classes internally, not in our templates.
FRAMEWORK_PREFIXES: tuple[str, ...] = ("mat-", "mdc-", "cdk-")
# Rendered at runtime: routerLink renders href; Material sets [disabled] and aria-*.
STANDARD_ATTRIBUTES: frozenset[str] = frozenset({"href", "disabled"})
STANDARD_ATTRIBUTE_PREFIXES: tuple[str, ...] = ("aria-",)
# DOM names declared by the Angular UI, keyed by token kind: tag/testid.
TemplateIndex = dict[str, frozenset[str]]

_SELECTOR_CALLS = frozenset({"locator", "wait_for_selector"})
_SEL_NAME_RE = re.compile(r"_SEL(?:_TMPL)?$")
_ANNOTATION_RE = re.compile(r"#\s*ui-contract:\s*\S")
_HAS_TEXT_RE = re.compile(r":has-text\(")
_PLACEHOLDER = "__ph__"
_FORMAT_FIELD_RE = re.compile(r"\{[^{}]*\}")
_QUOTED_RE = re.compile(r"\"[^\"]*\"|'[^']*'")
_SEL_TESTID_RE = re.compile(r"""\[\s*data-testid\s*=\s*(['"])([\w-]+)\1\s*\]""")
_SEL_ATTR_RE = re.compile(r"\[\s*([\w-]+)[^\]]*\]")
_SEL_PSEUDO_RE = re.compile(r"::?[\w-]+")
_SEL_CLASS_RE = re.compile(r"\.([\w-]+)")
_SEL_ID_RE = re.compile(r"#[\w-]+")
_SEL_TAG_RE = re.compile(r"[A-Za-z_][\w-]*")
_TS_SELECTOR_RE = re.compile(r"""selector:\s*['"`]([^'"`]+)['"`]""")
_TPL_OPEN_TAG_RE = re.compile(r"<([A-Za-z][\w-]*)")
_TPL_TESTID_RE = re.compile(r"""(?<![\w.-])data-testid\s*=\s*(['"])([\w-]+)\1""")


@dataclass(frozen=True)
class Selector:
    """A selector string extracted from a Python source file."""

    file: str
    line: int
    end_line: int
    text: str
    name: str = ""  # Assigned constant name, if any.


def _string_value(node: ast.expr) -> str | None:
    """Return a string constant or an f-string with `{}` placeholders, else None."""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if not isinstance(node, ast.JoinedStr):
        return None
    return "".join(str(v.value) if isinstance(v, ast.Constant) else "{}" for v in node.values)


def _assigned_pairs(node: ast.Assign | ast.AnnAssign) -> Iterator[tuple[str, ast.expr]]:
    """Yield (target name, value) pairs, expanding tuple/list unpacking."""
    for target in node.targets if isinstance(node, ast.Assign) else [node.target]:
        if isinstance(target, ast.Name) and node.value is not None:
            yield target.id, node.value
        if isinstance(target, ast.Tuple | ast.List) and isinstance(
            node.value, ast.Tuple | ast.List
        ):
            for elt, val in zip(target.elts, node.value.elts, strict=False):
                if isinstance(elt, ast.Name):
                    yield elt.id, val


def _selector_nodes(tree: ast.AST) -> Iterator[tuple[str, ast.expr]]:
    """Yield (constant name or "", node) for selector strings per the module docstring."""
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign | ast.AnnAssign):
            yield from ((n, v) for n, v in _assigned_pairs(node) if _SEL_NAME_RE.search(n))
        if isinstance(node, ast.Call) and getattr(node.func, "attr", None) in _SELECTOR_CALLS:
            yield from (("", arg) for arg in node.args[:1])
            yield from (("", kw.value) for kw in node.keywords if kw.arg == "selector")


def parse_source(rel_path: str, source: str) -> ast.Module:
    """Parse Python source, raising HandledError on syntax errors."""
    try:
        return ast.parse(source, filename=rel_path)
    except SyntaxError as exc:
        raise HandledError(
            f"Cannot parse {rel_path}: {exc.msg} (line {exc.lineno}).",
            "Fix the Python syntax error before rerunning the lint.",
            details={"file": rel_path, "line": exc.lineno},
        ) from exc


def extract_selectors(rel_path: str, tree: ast.AST) -> list[Selector]:
    """Extract selector strings, ordered by line, from a parsed source."""
    selectors = [
        Selector(rel_path, node.lineno, node.end_lineno or node.lineno, text, name)
        for name, node in _selector_nodes(tree)
        if (text := _string_value(node)) is not None
    ]
    return sorted(selectors, key=lambda s: s.line)


def tokenize_selector(text: str) -> dict[str, set[str]]:
    """Split a selector into tag/testid/class/attribute tokens (drops pseudos, ids, `{}`)."""
    raw = _FORMAT_FIELD_RE.sub(_PLACEHOLDER, text)
    testids = {val for _, val in _SEL_TESTID_RE.findall(raw)}
    rest = _QUOTED_RE.sub('""', _SEL_TESTID_RE.sub(" ", raw))
    attributes = set(_SEL_ATTR_RE.findall(rest))
    if "data-testid" in attributes:
        attributes.remove("data-testid")
        testids.add("data-testid")
    rest = _SEL_PSEUDO_RE.sub(" ", _SEL_ATTR_RE.sub(" ", rest))
    classes = set(_SEL_CLASS_RE.findall(rest))
    rest = _SEL_ID_RE.sub(" ", _SEL_CLASS_RE.sub(" ", rest))
    tokens = {
        "tag": set(_SEL_TAG_RE.findall(rest)),
        "testid": testids,
        "class": classes,
        "attribute": attributes,
    }
    return {kind: {n for n in names if _PLACEHOLDER not in n} for kind, names in tokens.items()}


def _tracked_files(repo_root: Path, *paths: str) -> list[Path]:
    """Return sorted existing git-tracked files under `paths` in `repo_root`."""
    try:
        proc = subprocess.run(
            ["git", "-C", str(repo_root), "ls-files", "-z", "--", *paths],
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise HandledError(
            f"Failed to list tracked files in {repo_root}.",
            "Pass a valid git repository root via --repo-root.",
        ) from exc
    files = [repo_root / rel for rel in proc.stdout.split("\0") if rel]
    return [p for p in sorted(files) if p.is_file()]


def build_template_index(repo_root: Path) -> TemplateIndex:
    """Index tags and data-testid values from tracked cmd/ui/app files and index.html."""
    app_dir = repo_root / UI_APP_DIR
    if not app_dir.is_dir():
        raise HandledError(
            f"UI app directory not found: {app_dir}", "Pass the repository root via --repo-root."
        )
    tags, testids = set[str](), set[str]()
    for path in _tracked_files(repo_root, UI_APP_DIR, UI_INDEX_HTML):
        text = path.read_text(encoding="utf-8")
        if path.name.endswith(".ng.html") or path == repo_root / UI_INDEX_HTML:
            tags.update(_TPL_OPEN_TAG_RE.findall(text))
            testids.update(val for _, val in _TPL_TESTID_RE.findall(text))
        elif path.suffix == ".ts" and not path.name.endswith(("_test.ts", "_harness.ts")):
            for value in _TS_SELECTOR_RE.findall(text):
                tags.update(_SEL_TAG_RE.findall(value))
    return {"tag": frozenset(tags), "testid": frozenset(testids)}


def _is_known(kind: str, name: str, index: TemplateIndex, annotated: bool = False) -> bool:
    """Return whether a selector token is declared or exempt."""
    if kind == "testid":
        return name in index["testid"]
    if kind == "tag":
        return "-" not in name or name.startswith(FRAMEWORK_PREFIXES) or name in index["tag"]
    if annotated or name.startswith(FRAMEWORK_PREFIXES):
        return True
    is_standard = name in STANDARD_ATTRIBUTES or name.startswith(STANDARD_ATTRIBUTE_PREFIXES)
    return kind == "attribute" and is_standard


def _violation(sel: Selector, token: str, rule: str) -> dict[str, Any]:
    """Build a structured violation entry."""
    return {"file": sel.file, "line": sel.line, "selector": sel.text, "token": token, "rule": rule}


def find_dom_violations(
    sel: Selector, index: TemplateIndex, lines: list[str] | None = None
) -> list[dict[str, Any]]:
    """Report selector tokens that violate the DOM/data-testid contract."""
    annotated = bool(lines and has_annotation(lines, sel.line, sel.end_line))
    return [
        _violation(sel, name, f"dom-{kind}")
        for kind, names in tokenize_selector(sel.text).items()
        for name in sorted(names)
        if not _is_known(kind, name, index, annotated)
    ]


def has_annotation(lines: list[str], first: int, last: int) -> bool:
    """Return whether a `# ui-contract:` comment covers 1-based lines [first-1, last]."""
    above = lines[first - 2] if first >= 2 else ""
    if above.lstrip().startswith("#") and _ANNOTATION_RE.search(above):
        return True
    return any(_ANNOTATION_RE.search(line) for line in lines[first - 1 : last])


def _has_text_literals(tree: ast.AST) -> Iterator[tuple[ast.keyword, str]]:
    """Yield `has_text=` keywords whose value is a string or f-string."""
    for node in ast.walk(tree):
        if isinstance(node, ast.keyword) and node.arg == "has_text":
            if (text := _string_value(node.value)) is not None:
                yield node, text


def find_copy_violations(
    rel_path: str, tree: ast.AST, selectors: list[Selector], lines: list[str]
) -> list[dict[str, Any]]:
    """Report unannotated `:has-text(...)` selectors and `has_text=` literals."""
    violations = [
        _violation(sel, "has-text", "copy")
        for sel in selectors
        if _HAS_TEXT_RE.search(sel.text) and not has_annotation(lines, sel.line, sel.end_line)
    ]
    for node, text in _has_text_literals(tree):
        end = node.end_lineno or node.lineno
        if not has_annotation(lines, node.lineno, end):
            violations.append(_violation(Selector(rel_path, node.lineno, end, text), text, "copy"))
    return violations


def _is_selector_module(dotted: str) -> bool:
    """Return whether a dotted import path names the selector module."""
    return dotted == _SELECTOR_MODULE_NAME or dotted.endswith("." + _SELECTOR_MODULE_NAME)


def _selector_imports(tree: ast.AST) -> tuple[set[str], dict[str, str]]:
    """Return (module aliases, {local name: constant}) bound to the selector module."""
    modules: set[str] = set()
    names: dict[str, str] = {}
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom) and _is_selector_module(node.module or ""):
            names.update({a.asname or a.name: a.name for a in node.names})
        elif isinstance(node, ast.ImportFrom):  # from pkg import ui_css_selectors [as x]
            modules.update(a.asname or a.name for a in node.names if _is_selector_module(a.name))
        elif isinstance(node, ast.Import):  # import pkg.ui_css_selectors as x
            modules.update(a.asname for a in node.names if a.asname and _is_selector_module(a.name))
    return modules, names


def _referenced_constant(node: ast.AST, modules: set[str], names: dict[str, str]) -> str | None:
    """Return the selector-module constant a `alias.NAME` or imported name refers to."""
    if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name):
        return node.attr if node.value.id in modules else None
    return names.get(node.id) if isinstance(node, ast.Name) else None


def find_unknown_constants(rel_path: str, tree: ast.AST, known: set[str]) -> list[dict[str, Any]]:
    """Report references to selector-module constants that it does not define."""
    modules, names = _selector_imports(tree)
    return [
        _violation(Selector(rel_path, node.lineno, node.lineno, c), c, "unknown-selector-constant")
        for node in ast.walk(tree)
        if isinstance(node, ast.Attribute | ast.Name)
        and (c := _referenced_constant(node, modules, names)) is not None
        and c not in known
    ]


def _parse_nightly_files(repo_root: Path) -> dict[str, tuple[ast.Module, list[str]]]:
    """Parse non-excluded tracked `*.py` under NIGHTLY_TESTS_DIR, failing closed."""
    parsed = {}
    for path in _tracked_files(repo_root, NIGHTLY_TESTS_DIR):
        rel_path = path.relative_to(repo_root).as_posix()
        if path.suffix != ".py" or rel_path in EXCLUDED_SOURCES:
            continue
        source = path.read_text(encoding="utf-8")
        parsed[rel_path] = (parse_source(rel_path, source), source.splitlines())
    return parsed


def check_ui_selector_contract(args: argparse.Namespace) -> dict[str, Any]:
    """Run the lint over discovered sources; raise HandledError on any violation."""
    repo_root = Path(args.repo_root).resolve()
    index = build_template_index(repo_root)
    parsed = _parse_nightly_files(repo_root)
    if SELECTOR_MODULE not in parsed:
        raise HandledError(
            f"Selector module not found: {SELECTOR_MODULE}",
            "Update SELECTOR_MODULE in tools/lint/check_ui_selector_contract.py.",
        )
    known = {s.name for s in extract_selectors(SELECTOR_MODULE, parsed[SELECTOR_MODULE][0])}
    violations: list[dict[str, Any]] = []
    sources: list[str] = []
    count = 0
    for rel_path, (tree, lines) in parsed.items():
        selectors = extract_selectors(rel_path, tree)
        if selectors or any(_has_text_literals(tree)):
            sources.append(rel_path)
        count += len(selectors)
        violations += [v for sel in selectors for v in find_dom_violations(sel, index, lines)]
        violations += find_copy_violations(rel_path, tree, selectors, lines)
        violations += find_unknown_constants(rel_path, tree, known)
    if violations:
        raise HandledError(
            f"Found {len(violations)} UI selector contract violation(s).",
            "Point selectors at data-testid attributes or component tags rendered by "
            "cmd/ui/app templates; annotate unavoidable copy/class/attribute matches "
            "with '# ui-contract: <reason>'.",
            details={"violations": violations},
        )
    return {"sources": sources, "selectors_scanned": count, "violations": 0}


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser."""
    parser = argparse.ArgumentParser(description="Lint nightly UI selectors against Angular DOM.")
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root directory.")
    return parser


def main() -> None:
    """Run the UI selector contract linter CLI."""
    run_tool(build_parser(), check_ui_selector_contract)


if __name__ == "__main__":
    main()
