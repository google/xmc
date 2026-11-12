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

"""Lint verifying relative links and OSS URL hygiene in repository Markdown docs.

Scans tracked `*.md` files outside `.agents/` (which `check_agent_doc_paths.py`
checks separately) and fails when:
- A relative Markdown link target does not exist in the git index or escapes the repo.
- A non-code Markdown line contains a `file://` URI, `corp.google.com` host, or
  `http(s)://go/...` / `//go/...` corporate shortlink.
"""

from __future__ import annotations

import argparse
import dataclasses
import posixpath
import re
import sys
from collections.abc import Iterator
from pathlib import Path
from typing import Any
from urllib.parse import unquote

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.lint.check_agent_doc_paths import (  # noqa: E402
    INLINE_CODE_RE,
    closes_fence,
    list_tracked_files,
    opens_fence,
    tracked_paths,
)

_LINK_TAIL_RE = re.compile(r'^\s*(?:(?:"[^"]*"|\'[^\']*\')\s*)?\)')
_REF_DEF_RE = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*(<[^>]+>|[^\s]+)")
_SCHEME_RE = re.compile(r"^[a-z][a-z0-9+.-]+:", re.IGNORECASE)
_FORBIDDEN_URL_RE = re.compile(
    r"file://[^\s)>'\"]+|[A-Za-z0-9.-]*corp\.google\.com\b|(?:https?://|(?<![A-Za-z0-9:/])//)go/[A-Za-z0-9_./-]+",
    re.IGNORECASE,
)


@dataclasses.dataclass(frozen=True)
class DocViolation:
    """A broken relative link or forbidden internal URL in a Markdown document."""

    doc: str
    line: int
    kind: str
    target: str


def is_checked_doc(path: str) -> bool:
    """Return whether a tracked path is a Markdown doc covered by this linter."""
    return path.endswith(".md") and not path.startswith(".agents/")


def iter_prose_lines(text: str) -> Iterator[tuple[int, str]]:
    """Yield (1-based line number, line without inline code) outside fenced blocks."""
    opening = ""
    for lineno, line in enumerate(text.splitlines(), start=1):
        if opening:
            if closes_fence(line, opening):
                opening = ""
            continue
        opened = opens_fence(line)
        if opened:
            opening = opened
            continue
        yield lineno, INLINE_CODE_RE.sub("", line)


def _parse_link_destination(prose: str, start: int) -> str | None:
    """Parse an inline Markdown link destination immediately following `](`."""
    i = start
    while i < len(prose) and prose[i] in " \t":
        i += 1
    if i < len(prose) and prose[i] == "<":
        end = prose.find(">", i + 1)
        if end == -1 or not _LINK_TAIL_RE.match(prose[end + 1 :]):
            return None
        return prose[i + 1 : end]
    depth = 0
    j = i
    while j < len(prose):
        ch = prose[j]
        if ch in " \t":
            break
        if ch == "(":
            depth += 1
        elif ch == ")":
            if depth == 0:
                return prose[i:j] if j > i else None
            depth -= 1
        j += 1
    if j > i and depth == 0 and _LINK_TAIL_RE.match(prose[j:]):
        return prose[i:j]
    return None


def extract_link_targets(prose: str) -> list[str]:
    """Extract inline link, linked-image, and reference-definition targets from a line."""
    targets: list[str] = []
    pos = 0
    while (idx := prose.find("](", pos)) != -1:
        dest = _parse_link_destination(prose, idx + 2)
        if dest is not None:
            targets.append(dest)
        pos = idx + 2
    ref_match = _REF_DEF_RE.match(prose)
    if ref_match:
        targets.append(ref_match.group(1))
    return targets


def resolve_repo_target(doc: str, raw_target: str) -> tuple[str, str | None]:
    """Resolve a Markdown link target relative to doc.

    Returns:
        Tuple of (resolved_repo_path, error_kind) where error_kind is None if
        the link is external, anchor-only, or resolves cleanly within the repo.
    """
    stripped = raw_target.strip("<>")
    if not stripped or stripped.startswith("#"):
        return "", None
    if stripped.lower().startswith("file://"):
        return stripped.rstrip(".,;:"), "forbidden_url"
    if stripped.startswith("//") or _SCHEME_RE.match(stripped):
        return "", None
    path_part = unquote(stripped.split("#", 1)[0].split("?", 1)[0])
    if not path_part:
        return "", None
    if path_part.startswith("/"):
        joined = posixpath.normpath(path_part.lstrip("/"))
    else:
        doc_dir = posixpath.dirname(doc)
        joined = posixpath.normpath(posixpath.join(doc_dir, path_part) if doc_dir else path_part)
    if joined == ".." or joined.startswith("../"):
        return joined, "escapes_repo"
    return ("" if joined == "." else joined), None


def find_line_violations(
    doc: str, lineno: int, prose: str, existing: frozenset[str]
) -> list[DocViolation]:
    """Collect broken-link and forbidden-URL violations from a single prose line."""
    violations: list[DocViolation] = []
    for raw_target in extract_link_targets(prose):
        resolved, err_kind = resolve_repo_target(doc, raw_target)
        if err_kind is not None:
            violations.append(DocViolation(doc=doc, line=lineno, kind=err_kind, target=resolved))
        elif resolved and resolved not in existing:
            violations.append(
                DocViolation(doc=doc, line=lineno, kind="missing_target", target=resolved)
            )
    for url_match in _FORBIDDEN_URL_RE.finditer(prose):
        url = url_match.group(0).rstrip(".,;:")
        if not any(v.target == url and v.line == lineno for v in violations):
            violations.append(DocViolation(doc=doc, line=lineno, kind="forbidden_url", target=url))
    return violations


def scan_docs(repo_root: Path, docs: list[str], existing: frozenset[str]) -> list[DocViolation]:
    """Scan all tracked Markdown docs outside `.agents/` for link and URL issues."""
    violations: list[DocViolation] = []
    for doc in docs:
        doc_path = repo_root / doc
        if not doc_path.is_file():
            continue
        text = doc_path.read_text(encoding="utf-8", errors="replace")
        for lineno, prose in iter_prose_lines(text):
            violations.extend(find_line_violations(doc, lineno, prose, existing))
    return violations


def check_doc_links(args: argparse.Namespace) -> dict[str, Any]:
    """Validate relative links and public URL hygiene in repository Markdown docs."""
    repo_root = Path(args.repo_root).resolve()
    files = list_tracked_files(repo_root)
    docs = sorted(f for f in files if is_checked_doc(f))
    violations = scan_docs(repo_root, docs, tracked_paths(files))
    if violations:
        raise HandledError(
            f"Found {len(violations)} broken Markdown link(s) or internal URL(s).",
            "Fix broken relative Markdown links against tracked files/directories and remove "
            "local file:// or corporate URLs.",
            details={"violations": [dataclasses.asdict(v) for v in violations]},
        )
    return {"docs_scanned": len(docs), "violations": 0}


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    run_tool(parser, check_doc_links)


if __name__ == "__main__":
    main()
