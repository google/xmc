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

Selectors in SELECTOR_SOURCES are string values assigned to names matching
_SEL_NAME_RE (incl. tuple/list unpacking) and the first positional or selector=
argument of _SELECTOR_CALLS;
`{...}` placeholders are wildcards. Rules:
  * dom-tag: custom tags (with `-`) must be a component selector or template tag.
  * dom-class / dom-attribute: names must appear in a cmd/ui/app template.
  * copy: `:has-text(...)` / `has_text=` need `# ui-contract: <reason>` on the
    string's line or the line above (not elsewhere in a multi-line statement).
FRAMEWORK_PREFIXES and STANDARD_ATTRIBUTES(_PREFIXES) are exempt.
Limitations: regex tokenizing (ids, escapes, attribute values such as href or
aria-* unchecked); Playwright non-CSS engines (`text=`, `role=`, `>>`) and
selectors passed through variables are not parsed; classes set only from
TypeScript (HostBinding, `host:` metadata, classList) are reported.
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

SELECTOR_SOURCES: tuple[str, ...] = (  # Glob patterns; each must match a file.
    "tools/nightly_tests/verification/ui_css_selectors.py",
    "tools/nightly_tests/verification/ui_entities_validator.py",
    "tools/nightly_tests/examples_verification/*_ui_test.py",
    "tools/nightly_tests/weekly_visual_snapshot.py",
)
UI_APP_DIR = "cmd/ui/app"
UI_INDEX_HTML = "cmd/ui/index.html"

# Angular Material / CDK render these tags and classes at runtime from component
# internals, so they never appear in our templates.
FRAMEWORK_PREFIXES: tuple[str, ...] = ("mat-", "mdc-", "cdk-")
# Attributes rendered by the browser or Angular rather than written verbatim:
# routerLink renders href, and [disabled]/aria-* are set by Material components.
STANDARD_ATTRIBUTES: frozenset[str] = frozenset({"href", "disabled"})
STANDARD_ATTRIBUTE_PREFIXES: tuple[str, ...] = ("aria-",)

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
_TPL_TAG_RE = re.compile(r"<([A-Za-z][\w-]*)")
_TPL_OPEN_TAG_RE = re.compile(r"""<[A-Za-z][\w-]*((?:"[^"]*"|'[^']*'|[^'">])*)>""")
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


@dataclass(frozen=True)
class TemplateIndex:
    """DOM names declared by the Angular UI."""

    tags: frozenset[str]
    classes: frozenset[str]
    attributes: frozenset[str]


def _string_value(node: ast.expr) -> str | None:
    """Return a string constant or an f-string with `{}` placeholders, else None."""
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    if not isinstance(node, ast.JoinedStr):
        return None
    parts = [
        v.value if isinstance(v, ast.Constant) and isinstance(v.value, str) else "{}"
        for v in node.values
    ]
    return "".join(parts)


def _assigned_pairs(node: ast.Assign) -> Iterator[tuple[str, ast.expr]]:
    """Yield (target name, value) pairs, expanding tuple/list unpacking."""
    for target in node.targets:
        if isinstance(target, ast.Name):
            yield target.id, node.value
        if isinstance(target, ast.Tuple | ast.List) and isinstance(
            node.value, ast.Tuple | ast.List
        ):
            for elt, val in zip(target.elts, node.value.elts, strict=False):
                if isinstance(elt, ast.Name):
                    yield elt.id, val


def _selector_nodes(tree: ast.AST) -> Iterator[ast.expr]:
    """Yield AST nodes holding selector strings per the module docstring rules."""
    for node in ast.walk(tree):
        if isinstance(node, ast.Assign):
            yield from (v for name, v in _assigned_pairs(node) if _SEL_NAME_RE.search(name))
        if (
            isinstance(node, ast.Call)
            and isinstance(node.func, ast.Attribute)
            and node.func.attr in _SELECTOR_CALLS
        ):
            yield from node.args[:1]
            yield from (kw.value for kw in node.keywords if kw.arg == "selector")


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
        Selector(rel_path, node.lineno, node.end_lineno or node.lineno, text)
        for node in _selector_nodes(tree)
        if (text := _string_value(node)) is not None
    ]
    return sorted(selectors, key=lambda s: s.line)


def tokenize_selector(text: str) -> dict[str, set[str]]:
    """Split a selector into tag, class, and attribute names.

    Quoted strings, pseudo-classes, and ids are dropped; tokens containing a
    format placeholder are skipped.
    """
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
        for attrs in _TPL_OPEN_TAG_RE.findall(html)
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
    tags: set[str] = set()
    classes: set[str] = set()
    attributes: set[str] = set()
    for path in htmls:
        html = path.read_text(encoding="utf-8")
        tags.update(_TPL_TAG_RE.findall(html))
        classes.update(_template_classes(html))
        attributes.update(_template_attributes(html))
    for path in sorted(app_dir.rglob("*.ts")):
        if not path.name.endswith("_test.ts"):
            for value in _TS_SELECTOR_RE.findall(path.read_text(encoding="utf-8")):
                tags.update(_SEL_TAG_RE.findall(value))
    return TemplateIndex(frozenset(tags), frozenset(classes), frozenset(attributes))


def _is_known(kind: str, name: str, index: TemplateIndex) -> bool:
    """Return whether a selector token is declared or exempt."""
    if name.startswith(FRAMEWORK_PREFIXES):
        return True
    if kind == "tag":
        return "-" not in name or name in index.tags
    if kind == "class":
        return name in index.classes
    if name in STANDARD_ATTRIBUTES or name.startswith(STANDARD_ATTRIBUTE_PREFIXES):
        return True
    return name in index.attributes


def _violation(sel: Selector, token: str, rule: str) -> dict[str, Any]:
    """Build a structured violation entry."""
    return {"file": sel.file, "line": sel.line, "selector": sel.text, "token": token, "rule": rule}


def find_dom_violations(sel: Selector, index: TemplateIndex) -> list[dict[str, Any]]:
    """Report selector tokens that no template declares."""
    tokens = tokenize_selector(sel.text)
    return [
        _violation(sel, name, f"dom-{kind}")
        for kind in ("tag", "class", "attribute")
        for name in sorted(tokens[kind])
        if not _is_known(kind, name, index)
    ]


def has_annotation(lines: list[str], first: int, last: int) -> bool:
    """Return whether a `# ui-contract:` comment covers 1-based lines [first-1, last]."""
    window = lines[max(first - 2, 0) : last]
    return any(_ANNOTATION_RE.search(line) for line in window)


def find_copy_violations(
    rel_path: str, tree: ast.AST, selectors: list[Selector], lines: list[str]
) -> list[dict[str, Any]]:
    """Report unannotated `:has-text(...)` selectors and `has_text=` literals."""
    violations = [
        _violation(sel, "has-text", "copy")
        for sel in selectors
        if _HAS_TEXT_RE.search(sel.text) and not has_annotation(lines, sel.line, sel.end_line)
    ]
    for node in ast.walk(tree):
        if not isinstance(node, ast.keyword) or node.arg != "has_text":
            continue
        text = _string_value(node.value)
        end = node.end_lineno or node.lineno
        if text is not None and not has_annotation(lines, node.lineno, end):
            violations.append(_violation(Selector(rel_path, node.lineno, end, text), text, "copy"))
    return violations


def check_ui_selector_contract(args: argparse.Namespace) -> dict[str, Any]:
    """Run the lint over SELECTOR_SOURCES; raise HandledError on any violation."""
    repo_root = Path(args.repo_root).resolve()
    index = build_template_index(repo_root)
    violations: list[dict[str, Any]] = []
    count = 0
    for pattern in SELECTOR_SOURCES:
        paths = sorted(repo_root.glob(pattern))
        if not paths:
            raise HandledError(
                f"Selector source {pattern} not found.",
                "Update SELECTOR_SOURCES in tools/lint/check_ui_selector_contract.py.",
            )
        for path in paths:
            rel_path, source = path.relative_to(repo_root).as_posix(), path.read_text("utf-8")
            tree = parse_source(rel_path, source)
            selectors = extract_selectors(rel_path, tree)
            count += len(selectors)
            violations += [v for sel in selectors for v in find_dom_violations(sel, index)]
            violations += find_copy_violations(rel_path, tree, selectors, source.splitlines())
    if violations:
        raise HandledError(
            f"Found {len(violations)} UI selector contract violation(s).",
            "Point selectors at tags/classes/attributes rendered by cmd/ui/app templates "
            "(prefer stable classes over copy); annotate unavoidable copy matches with "
            "'# ui-contract: <reason>'.",
            details={"violations": violations},
        )
    return {"selectors_scanned": count, "violations": 0}


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Lint nightly UI verifier selectors against Angular templates.",
    )
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root directory.")
    return parser


def main() -> None:
    """Run the UI selector contract linter CLI."""
    run_tool(build_parser(), check_ui_selector_contract)


if __name__ == "__main__":
    main()
