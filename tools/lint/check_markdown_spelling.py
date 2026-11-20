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

"""Lint flagging British spellings on lines a change adds to tracked Markdown files.

Go code already runs `misspell` with `locale: US` (see `.golangci.yml`); this check
applies the same convention to Markdown prose. Only lines added relative to the merge
base with `--base-ref` (staged changes included) are checked, so existing docs never
block a commit. Fenced code blocks, inline code spans, and URLs are ignored.
"""

from __future__ import annotations

import argparse
import dataclasses
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.lint.check_doc_links import iter_prose_lines  # noqa: E402

# British -our nouns and their US spelling stem.
_OUR_STEMS = ("behavi", "col", "fav", "flav", "hon", "lab", "neighb", "harb", "rum")

# Stems spelled -ise/-isation in British English and -ize/-ization in US English.
# Words that end in -ise in US English too (advertise, exercise, otherwise, precise,
# raise, noise, enterprise, expertise, compromise, ...) are deliberately absent.
_ISE_STEMS = (
    "apolog",
    "author",
    "capital",
    "categor",
    "central",
    "character",
    "container",
    "custom",
    "deserial",
    "digit",
    "emphas",
    "final",
    "general",
    "global",
    "harmon",
    "initial",
    "internal",
    "item",
    "local",
    "material",
    "maxim",
    "memor",
    "minim",
    "modern",
    "normal",
    "optim",
    "organ",
    "parallel",
    "parameter",
    "priorit",
    "real",
    "recogn",
    "sanit",
    "serial",
    "special",
    "stabil",
    "standard",
    "summar",
    "symbol",
    "synchron",
    "token",
    "util",
    "vector",
    "virtual",
    "visual",
)

# Other British forms and their US spelling.
_WORD_FIXES = {
    "analyse": "analyze",
    "analysed": "analyzed",
    "analysing": "analyzing",
    "artefact": "artifact",
    "artefacts": "artifacts",
    "catalogue": "catalog",
    "catalogues": "catalogs",
    "centre": "center",
    "centres": "centers",
    "defence": "defense",
    "fulfil": "fulfill",
    "licence": "license",
    "labelling": "labeling",
    "levelled": "leveled",
    "modelled": "modeled",
    "modelling": "modeling",
    "signalled": "signaled",
    "travelled": "traveled",
}

# Mirrors `misspell.ignore-words` in `.golangci.yml`, so Go and Markdown agree.
ALLOWED_WORDS = frozenset({"cancelled", "cancelling", "labelled", "commonhealth"})

_ISE_SUFFIXES = "e|es|ed|ing|ation|ations|er|ers"
_ISE_WORD_RE = re.compile(rf"({'|'.join(_ISE_STEMS)})is({_ISE_SUFFIXES})", re.IGNORECASE)
_BRITISH_RE = re.compile(
    r"\b(?:"
    rf"(?:{'|'.join(_OUR_STEMS)})our(?:s|ed|ing|able|ite|ites)?"
    rf"|(?:{'|'.join(_ISE_STEMS)})is(?:{_ISE_SUFFIXES})"
    rf"|{'|'.join(sorted(_WORD_FIXES, key=len, reverse=True))}"
    r")\b",
    re.IGNORECASE,
)
_URL_RE = re.compile(r"\]\([^)]*\)|<?https?://\S+")
_HUNK_RE = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@")


@dataclasses.dataclass(frozen=True)
class SpellingViolation:
    """A British spelling on an added Markdown line."""

    path: str
    line: int
    word: str
    suggestion: str


def suggest(word: str) -> str:
    """Returns the US spelling for a British word matched by the lint."""
    lower = word.lower()
    if lower in _WORD_FIXES:
        return _WORD_FIXES[lower]
    if ise := _ISE_WORD_RE.fullmatch(lower):
        return f"{ise.group(1)}iz{ise.group(2)}"
    return lower.replace("our", "or", 1)


def find_british_words(prose: str) -> list[str]:
    """Returns British spellings in a prose line, ignoring URLs and allowed words."""
    text = _URL_RE.sub(" ", prose)
    return [
        m.group(0) for m in _BRITISH_RE.finditer(text) if m.group(0).lower() not in ALLOWED_WORDS
    ]


def parse_added_lines(diff: str) -> dict[str, set[int]]:
    """Maps each file in a `git diff -U0` output to the new-side line numbers it adds."""
    added: dict[str, set[int]] = {}
    path = ""
    for line in diff.splitlines():
        if line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else ""
            continue
        hunk = _HUNK_RE.match(line)
        if hunk and path:
            start, count = int(hunk.group(1)), int(hunk.group(2) or "1")
            added.setdefault(path, set()).update(range(start, start + count))
    return added


def _git(args: list[str], repo_root: Path) -> str:
    """Returns the stdout of `git <args>`; raises HandledError on failure."""
    try:
        res = subprocess.run(
            ["git", *args], capture_output=True, text=True, check=True, cwd=repo_root
        )
    except (OSError, subprocess.CalledProcessError) as e:
        stderr = getattr(e, "stderr", "") or str(e)
        raise HandledError(
            f"git {args[0]} failed: {stderr.strip()}",
            "Run from inside the repository and make sure the base ref is fetched.",
        ) from e
    return res.stdout


def resolve_base(base_ref: str, repo_root: Path) -> str | None:
    """Returns the merge base of base_ref and HEAD, or None when base_ref is unavailable."""
    try:
        return _git(["merge-base", base_ref, "HEAD"], repo_root).strip() or None
    except HandledError:
        return None


def collect_added_lines(base: str | None, repo_root: Path) -> dict[str, set[int]]:
    """Returns added Markdown lines in the index relative to base (staged-only without base)."""
    cmd = [
        "-c",
        "core.quotePath=false",
        "diff",
        "--cached",
        "-U0",
        "--no-color",
        "--no-ext-diff",
        "--diff-filter=AMR",
    ]
    if base:
        cmd.append(base)
    return parse_added_lines(_git([*cmd, "--", "*.md"], repo_root))


def scan(added: dict[str, set[int]], repo_root: Path) -> list[SpellingViolation]:
    """Scans the staged content of each file for British words on its added prose lines."""
    violations: list[SpellingViolation] = []
    for path in sorted(added):
        text = _git(["show", f":{path}"], repo_root)
        for lineno, prose in iter_prose_lines(text):
            if lineno not in added[path]:
                continue
            violations.extend(
                SpellingViolation(path, lineno, word, suggest(word))
                for word in find_british_words(prose)
            )
    return violations


def check_markdown_spelling(args: argparse.Namespace) -> dict[str, Any]:
    """Fails when added Markdown lines use British spellings."""
    repo_root = Path(args.repo_root).resolve()
    base = resolve_base(args.base_ref, repo_root)
    added = collect_added_lines(base, repo_root)
    violations = scan(added, repo_root)
    if violations:
        raise HandledError(
            f"Found {len(violations)} British spelling(s) on added Markdown lines.",
            "Use US English spellings (the repo's misspell locale is US).",
            details={"violations": [dataclasses.asdict(v) for v in violations]},
        )
    return {"files_checked": len(added), "base": base or "staged changes only"}


def main() -> None:
    """Entrypoint for the Markdown US-spelling lint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--base-ref",
        default="origin/main",
        help="Ref whose merge base with HEAD defines added lines (default: origin/main).",
    )
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    run_tool(parser, check_markdown_spelling)


if __name__ == "__main__":
    main()
