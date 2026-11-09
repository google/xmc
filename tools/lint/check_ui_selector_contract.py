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

Parses every `*.py` under NIGHTLY_TESTS_DIR (minus EXCLUDED_SOURCES and hidden or
`__pycache__` paths; syntax errors fail). Selectors: string values of _SEL_NAME_RE
names and the first positional or `selector=` argument of _SELECTOR_CALLS
(`{...}` = wildcard). Rules: dom-tag, dom-class, dom-attribute (custom tags,
classes, attributes must exist in cmd/ui/app; FRAMEWORK_PREFIXES and
STANDARD_ATTRIBUTES exempt); copy (`:has-text`/`has_text=` need `# ui-contract:
<reason>` on the string's line or the one above); unknown-selector-constant
(`alias.NAME` / imported names from SELECTOR_MODULE must be defined there).
Limitations: regex tokenizing (ids, escapes, href/aria-* values unchecked); no
Playwright `text=`/`role=`/`>>` engines, variable or unaliased dotted indirection;
classes set only in TypeScript (HostBinding, `host:`, classList) are reported.
"""

from __future__ import annotations

import argparse
import ast
import re
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
# DOM names declared by the Angular UI, keyed like tokenize_selector(): tag/class/attribute.
TemplateIndex = dict[str, frozenset[str]]

_SELECTOR_CALLS = frozenset({"locator", "wait_for_selector"})
_SEL_NAME_RE = re.compile(r"_SEL(?:_TMPL)?$")
_ANNOTATION_RE = re.compile(r"#\s*ui-contract:\s*\S")
_HAS_TEXT_RE = re.compile(r":has-text\(")
_PLACEHOLDER = "__ph__"
_FORMAT_FIELD_RE = re.compile(r"\{[^{}]*\}")
_QUOTED_RE = re.compile(r"\"[^\"]*\"|'[^']*'")
_SEL_ATTR_RE = re.compile(r"\[\s*([\w-]+)[^\]]*\]")
_SEL_PSEUDO_RE = re.compile(r"::?[\w-]+")
_SEL_CLASS_RE = re.compile(r"\.([\w-]+)")
_SEL_ID_RE = re.compile(r"#[\w-]+")
_SEL_TAG_RE = re.compile(r"[A-Za-z_][\w-]*")
_TS_SELECTOR_RE = re.compile(r"""selector:\s*['"`]([^'"`]+)['"`]""")
_TPL_OPEN_TAG_RE = re.compile(r"""<([A-Za-z][\w-]*)((?:"[^"]*"|'[^']*'|[^'">])*)>""")
_TPL_ATTR_NAME_RE = re.compile(r"(?<!\S)(?:\[attr\.([\w-]+)\]|([A-Za-z][\w-]*))(?=[\s=/]|$)")
_TPL_INTERPOLATION_RE = re.compile(r"\{\{.*?\}\}", re.DOTALL)
_TPL_STATIC_CLASS_RE = re.compile(r"(?<![\w.\[-])class\s*=\s*([\"'])(.*?)\1", re.DOTALL)
_TPL_CLASS_PROP_RE = re.compile(r"\[class\.([\w-]+)\]")
_TPL_CLASS_BINDING_RE = re.compile(r"\[(?:class|ngClass)\]\s*=\s*([\"'])(.*?)\1", re.DOTALL)
_TPL_BINDING_KEY_RE = re.compile(r"([\w-]+)\s*:")


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
    """Split a selector into tag/class/attribute names (drops quotes, pseudos, ids, `{}`)."""
    rest = _FORMAT_FIELD_RE.sub(_PLACEHOLDER, _QUOTED_RE.sub('""', text))
    attributes = set(_SEL_ATTR_RE.findall(rest))
    rest = _SEL_PSEUDO_RE.sub(" ", _SEL_ATTR_RE.sub(" ", rest))
    classes = set(_SEL_CLASS_RE.findall(rest))
    rest = _SEL_ID_RE.sub(" ", _SEL_CLASS_RE.sub(" ", rest))
    tokens = {"tag": set(_SEL_TAG_RE.findall(rest)), "class": classes, "attribute": attributes}
    return {kind: {n for n in names if _PLACEHOLDER not in n} for kind, names in tokens.items()}


def _template_classes(html: str) -> set[str]:
    """Collect class names from static attributes and class bindings."""
    classes: set[str] = set(_TPL_CLASS_PROP_RE.findall(html))
    for _, value in _TPL_STATIC_CLASS_RE.findall(html):
        classes.update(_TPL_INTERPOLATION_RE.sub(" ", value).split())
    for _, expr in _TPL_CLASS_BINDING_RE.findall(html):
        for literal in _QUOTED_RE.findall(expr):
            classes.update(literal[1:-1].split())
        classes.update(_TPL_BINDING_KEY_RE.findall(expr))
    return classes


def _template_attributes(html: str) -> set[str]:
    """Collect `[attr.x]`, static, and valueless attribute names from opening tags."""
    return {
        bound or plain
        for _, attrs in _TPL_OPEN_TAG_RE.findall(html)
        for bound, plain in _TPL_ATTR_NAME_RE.findall(_QUOTED_RE.sub('""', attrs))
    }


def build_template_index(repo_root: Path) -> TemplateIndex:
    """Index tags, classes, and attributes from cmd/ui/app templates and index.html."""
    app_dir = repo_root / UI_APP_DIR
    if not app_dir.is_dir():
        raise HandledError(
            f"UI app directory not found: {app_dir}", "Pass the repository root via --repo-root."
        )
    htmls = sorted(app_dir.rglob("*.ng.html"))
    index_html = repo_root / UI_INDEX_HTML
    htmls += [index_html] if index_html.is_file() else []
    tags, classes, attributes = set[str](), set[str](), set[str]()
    for path in htmls:
        html = path.read_text(encoding="utf-8")
        tags.update(tag for tag, _ in _TPL_OPEN_TAG_RE.findall(html))
        classes.update(_template_classes(html))
        attributes.update(_template_attributes(html))
    for path in sorted(app_dir.rglob("*.ts")):
        if not path.name.endswith("_test.ts"):
            for value in _TS_SELECTOR_RE.findall(path.read_text(encoding="utf-8")):
                tags.update(_SEL_TAG_RE.findall(value))
    return {"tag": frozenset(tags), "class": frozenset(classes), "attribute": frozenset(attributes)}


def _is_known(kind: str, name: str, index: TemplateIndex) -> bool:
    """Return whether a selector token is declared or exempt."""
    if name.startswith(FRAMEWORK_PREFIXES) or name in index[kind]:
        return True
    if kind == "tag":
        return "-" not in name
    is_standard = name in STANDARD_ATTRIBUTES or name.startswith(STANDARD_ATTRIBUTE_PREFIXES)
    return kind == "attribute" and is_standard


def _violation(sel: Selector, token: str, rule: str) -> dict[str, Any]:
    """Build a structured violation entry."""
    return {"file": sel.file, "line": sel.line, "selector": sel.text, "token": token, "rule": rule}


def find_dom_violations(sel: Selector, index: TemplateIndex) -> list[dict[str, Any]]:
    """Report selector tokens that no template declares."""
    return [
        _violation(sel, name, f"dom-{kind}")
        for kind, names in tokenize_selector(sel.text).items()
        for name in sorted(names)
        if not _is_known(kind, name, index)
    ]


def has_annotation(lines: list[str], first: int, last: int) -> bool:
    """Return whether a `# ui-contract:` comment covers 1-based lines [first-1, last]."""
    return any(_ANNOTATION_RE.search(line) for line in lines[max(first - 2, 0) : last])


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
    """Parse non-excluded, non-hidden `*.py` under NIGHTLY_TESTS_DIR, failing closed."""
    parsed = {}
    for path in sorted((repo_root / NIGHTLY_TESTS_DIR).rglob("*.py")):
        rel_path = path.relative_to(repo_root).as_posix()
        hidden = any(p.startswith(".") or p == "__pycache__" for p in rel_path.split("/"))
        if hidden or rel_path in EXCLUDED_SOURCES:
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
        violations += [v for sel in selectors for v in find_dom_violations(sel, index)]
        violations += find_copy_violations(rel_path, tree, selectors, lines)
        violations += find_unknown_constants(rel_path, tree, known)
    if violations:
        raise HandledError(
            f"Found {len(violations)} UI selector contract violation(s).",
            "Point selectors at DOM rendered by cmd/ui/app templates (prefer stable classes "
            "over copy); annotate unavoidable copy matches with '# ui-contract: <reason>'.",
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
